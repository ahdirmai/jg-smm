// Command server is the API entrypoint. It wires configuration, logging, the
// database pool, the HTTP router and graceful shutdown, then serves until it
// receives SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"

	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter"
	analyticsprovider "github.com/ahdirmai/jg-smm/apps/api/internal/adapter/analytics"
	apifyadapter "github.com/ahdirmai/jg-smm/apps/api/internal/adapter/apify"
	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter/crypto"
	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter/dockerprovisioner"
	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter/k8s"
	smtpadapter "github.com/ahdirmai/jg-smm/apps/api/internal/adapter/smtp"
	storageadapter "github.com/ahdirmai/jg-smm/apps/api/internal/adapter/storage"
	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter/transport"
	"github.com/ahdirmai/jg-smm/apps/api/internal/config"
	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	apihttp "github.com/ahdirmai/jg-smm/apps/api/internal/http"
	"github.com/ahdirmai/jg-smm/apps/api/internal/obs"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/repository"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

func main() {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "healthcheck":
		os.Exit(healthcheck())
	case len(os.Args) > 1 && os.Args[1] == "seed":
		os.Exit(seed())
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	logger := obs.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	metrics := obs.NewMetrics()
	// Publish the monthly ceilings once at boot (P5-08): the budget alert
	// divides this instead of a constant in the rule file. A 0 budget publishes
	// no gauge, which disables the alert rather than making it fire on nothing.
	if cfg.ApifyBudgetUSD > 0 {
		metrics.BudgetCeiling.WithLabelValues("apify").Set(cfg.ApifyBudgetUSD)
	}
	if cfg.ProxyBudgetGB > 0 {
		metrics.BudgetCeiling.WithLabelValues("proxy").Set(cfg.ProxyBudgetGB * 1024 * 1024 * 1024)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	checkers := map[string]port.HealthChecker{}
	deps := apihttp.Dependencies{
		Health:             apihttp.NewHealthHandler(service.NewHealthService(checkers)),
		Metrics:            apihttp.NewMetricsHandler(),
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
	}

	// Database is optional at boot: if DATABASE_URL is unset the API still serves
	// (useful when migrations run separately). Failing to connect is fatal.
	if cfg.DatabaseURL != "" {
		pg, err := adapter.NewPostgres(ctx, cfg.DatabaseURL)
		if err != nil {
			logger.Error("database connect failed", "err", err)
			os.Exit(1)
		}
		defer pg.Close()
		checkers["postgres"] = pg
		logger.Info("database connected")

		// Proves the sqlc query path works end to end at boot: reads the
		// singleton team config (creating the default row on first run).
		teamSvc := service.NewTeamConfigService(repository.NewTeamConfigRepo(pg.Queries()))
		team, err := teamSvc.Get(ctx)
		if err != nil {
			logger.Error("team config bootstrap failed", "err", err)
			os.Exit(1)
		}
		logger.Info("team config loaded", "teamId", team.ID, "teamName", team.Name)

		// Auth: JWT access tokens + DB-backed refresh sessions.
		issuer, err := adapter.NewJWTIssuer(cfg.JWTSecret, cfg.JWTIssuer)
		if err != nil {
			logger.Error("jwt issuer init failed", "err", err)
			os.Exit(1)
		}
		authRepo := repository.NewAuthRepo(pg.Queries())
		authSvc := service.NewAuthService(authRepo, authRepo, issuer, adapter.SystemClock{})
		deps.Auth = apihttp.NewAuthHandler(authSvc, cfg.SecureCookies)

		// Audit trail (P6-10): records who changed what, and serves the
		// dashboard audit page. Mounted as middleware on the /api group so
		// one place covers every mutation, present and future.
		auditSvc := service.NewAuditService(repository.NewAuditRepo(pg.Queries()), service.AuditConfig{
			Clock:  adapter.SystemClock{},
			Logger: logger,
		})
		deps.Audit = apihttp.NewAuditHandler(auditSvc)

		// Team CRUD (P6-11): the single-team MVP roster. OWNER-administered;
		// the handler gates writes with the `admin` permission.
		userSvc := service.NewUserService(authRepo, authRepo, service.UserConfig{
			Clock:  adapter.SystemClock{},
			Logger: logger,
		})
		deps.Users = apihttp.NewUserHandler(userSvc)

		// Credential encryption (P1-07): accounts cannot be stored without it.
		sealer, err := crypto.NewAESGCMFromBase64(cfg.CredentialKeyBase64)
		if err != nil {
			logger.Error("credential key init failed", "err", err)
			os.Exit(1)
		}

		// The hub fans lifecycle events to dashboards over SSE (P1-17). Created
		// before the services that publish through it.
		hub := adapter.NewHub(cfg.SSEBuffer)

		// Worker callbacks (heartbeat / auth outcome / action verdict). The
		// worker container POSTs here; it never touches the DB directly.
		workerRepo := repository.NewWorkerRepo(pg.Queries(), pg.Pool())
		accountRepo := repository.NewAccountRepo(pg.Queries())
		logRepo := repository.NewProvisionLogRepo(pg.Queries())
		actionRepo := repository.NewActionRepo(pg.Queries())
		jobSvc := service.NewJobService(workerRepo, accountRepo, logRepo, actionRepo, adapter.SystemClock{}, hub, metrics, logger)
		deps.Internal = apihttp.NewInternalHandler(jobSvc)

		// One live-view port allocator for both paths: the manual container API
		// and the packer's AUTO fallback. Sharing it is what keeps a manual
		// create and an auto-spawn from picking the same port.
		novnc := service.NewNovncAllocator(workerRepo, cfg.DockerPublicHost, cfg.NovncPortMin, cfg.NovncPortMax)

		// Bin-packing: accounts land in the first container with a free platform
		// slot; PROVISION_AUTO_CREATE is the fallback that spawns an AUTO
		// container when the fleet is full (P1-05).
		packer := service.NewPacker(workerRepo, accountRepo, service.PackerConfig{
			MaxPerContainer: cfg.MaxAccountsPerContainer,
			AutoCreate:      cfg.ProvisionAutoCreate,
			Clock:           adapter.SystemClock{},
			Logger:          logger,
			Novnc:           novnc,
		})

		// The provisioner driver: k8s in a cluster, docker on a workstation,
		// static for bookkeeping-only. Built here (before the container service
		// and the reconciler) because the delete path needs it directly — the
		// row is the reconciler's only view of desired state, so a delete must
		// tear the container down while the row still says STOPPED.
		raw, err := provisionDriver(ctx, cfg, workerRepo, logRepo, logger)
		if err != nil {
			logger.Error("provisioner init failed", "err", err)
			os.Exit(1)
		}
		driver := service.NewLoggingDriver(raw, logRepo, adapter.SystemClock{}, logger)

		// Container API (P1-19): create/list/delete MANUAL containers. The
		// reconciler provisions the pod from the row this service writes.
		containerSvc := service.NewContainerService(workerRepo, accountRepo, packer, service.ContainerConfig{
			Clock:  adapter.SystemClock{},
			Logger: logger,
			Stream: hub,
			Logs:   logRepo,
			Driver: driver,
			Novnc:  novnc,
		})
		deps.Containers = apihttp.NewContainerHandler(containerSvc)

		// Redis is the transport for action queues and worker control
		// channels. It is optional: an unset URL leaves both the action
		// scheduler and the operator login flow off, but the dashboard still
		// serves.
		var publisher port.Publisher
		var redisClient *redis.Client
		if cfg.RedisURL != "" {
			rc, err := adapter.NewRedis(ctx, cfg.RedisURL)
			if err != nil {
				logger.Error("redis init failed", "err", err)
				os.Exit(1)
			}
			defer rc.Close()
			checkers["redis"] = rc
			redisClient = rc.Client()
			publisher = transport.NewPublisher(redisClient)
		}

		// Account API (P1-15 / P1-16): add/list/pause/resume/remove. The
		// sealer is injected so the plaintext password never reaches the store.
		// Control carries the auth-login/auth-input flow (P1-11 / P1-12).
		accountSvc := service.NewAccountService(accountRepo, workerRepo, packer, service.AccountConfig{
			Sealer:  sealer,
			Clock:   adapter.SystemClock{},
			Stream:  hub,
			Control: publisher,
			Logger:  logger,
		})
		deps.Accounts = apihttp.NewAccountHandler(accountSvc)
		// The account service is the sink for worker session-export callbacks:
		// it holds the in-memory waiter that relays the dumped session to the
		// operator's export request.
		if deps.Internal != nil {
			deps.Internal.SetSessionExportSink(accountSvc)
		}
		deps.Stream = apihttp.NewStreamHandler(hub)

		// Proxy groups (P1-14): residential pools, region-matched. The same
		// sealer covers the pool key and the account password.
		proxyGroupRepo := repository.NewProxyGroupRepo(pg.Queries())
		proxyGroupSvc := service.NewProxyGroupService(proxyGroupRepo, accountRepo, workerRepo, service.ProxyGroupConfig{
			Sealer: sealer,
			Clock:  adapter.SystemClock{},
			Logger: logger,
		})
		deps.ProxyGroups = apihttp.NewProxyGroupHandler(proxyGroupSvc)

		// P2 — scrape + official-account analytics. The scrape path (Apify) and
		// the analytics path (3rd-party provider) are disjoint: worker accounts
		// scrape/act, official accounts are monitored read-only.
		scrapeRepo := repository.NewScrapeRepo(pg.Queries())
		analyticsRepo := repository.NewAnalyticsRepo(pg.Queries())

		// Raw payload store (MinIO). Present in every environment; the apify
		// adapter writes dataset items here and the ingestor reads them back.
		var rawStorage port.RawStorage
		if cfg.MinioEndpoint != "" {
			storage, err := storageadapter.New(ctx, storageadapter.Config{
				Endpoint:   cfg.MinioEndpoint,
				AccessKey:  cfg.MinioUser,
				SecretKey:  cfg.MinioPassword,
				Bucket:     cfg.MinioBucket,
				UseSSL:     cfg.MinioUseSSL,
				MakeBucket: true,
			})
			if err != nil {
				logger.Error("object storage init failed", "err", err)
				os.Exit(1)
			}
			rawStorage = storage
			checkers["minio"] = storageadapter.NewHealth(storage)
		}

		// Analytics provider (stub until the real contract is signed, P2-11).
		var analyticsProvider port.AnalyticsProvider
		if cfg.AnalyticsProviderKey != "" {
			stub, err := analyticsprovider.New(cfg.AnalyticsProviderBaseURL, cfg.AnalyticsProviderKey)
			if err != nil {
				logger.Error("analytics provider init failed", "err", err)
				os.Exit(1)
			}
			analyticsProvider = stub
		}

		// Ingestor cron (P2-12): pulls official-account metrics on an interval.
		// Off by default locally; the API's refresh endpoint can trigger a run
		// regardless of the cron.
		analyticsIngestor := service.NewAnalyticsIngestor(analyticsRepo, analyticsProvider, service.AnalyticsIngestorConfig{
			Scope:  "all",
			Clock:  time.Now,
			Logger: logger,
		})
		if cfg.AnalyticsIngestIntervalSeconds > 0 {
			go analyticsIngestor.Run(ctx, time.Duration(cfg.AnalyticsIngestIntervalSeconds)*time.Second)
		}

		// P3-13 — action queue + comment templates. The enqueue path turns pasted
		// permalinks into PENDING jobs; the composer that renders comment text for
		// the scheduler is the same instance this CRUD handler serves, so a
		// template edit is visible to dispatch without a restart.
		templateSvc := service.NewTemplateService(repository.NewTemplateRepo(pg.Queries()), nil, nil, logger)
		actionSvc := service.NewActionService(actionRepo, accountRepo, scrapeRepo, nil, logger)
		deps.Actions = apihttp.NewActionHandler(actionSvc)
		deps.Templates = apihttp.NewTemplateHandler(templateSvc)

		// Scrape scheduler (P2-02): claims due jobs FIFO, runs the Apify actor,
		// records the outcome with jitter + rate-limit backoff.
		if cfg.ScrapeIntervalSeconds > 0 && cfg.ApifyToken != "" {
			runner, err := apifyadapter.New(apifyadapter.Config{
				BaseURL:  cfg.ApifyBaseURL,
				Token:    cfg.ApifyToken,
				Storage:  rawStorage,
				ActorFor: actorForPlatform(cfg),
				Log:      logger.Info,
			})
			if err != nil {
				logger.Error("apify runner init failed", "err", err)
				os.Exit(1)
			}
			scheduler := service.NewScrapeScheduler(scrapeRepo, runner, service.ScrapeSchedulerConfig{
				JitterMin:   time.Duration(cfg.ScrapeJitterMinSeconds) * time.Second,
				JitterMax:   time.Duration(cfg.ScrapeJitterMaxSeconds) * time.Second,
				MaxAttempts: cfg.ScrapeMaxAttempts,
				TickBudget:  cfg.ActionBatchParallelism * 10,
				Clock:       time.Now,
				Logger:      logger,
			})
			go scheduler.Run(ctx, time.Duration(cfg.ScrapeIntervalSeconds)*time.Second)
		}

		// Action scheduler (P3-07): claims due action jobs, enforces the
		// cooldown (P3-09) + per-platform rate budget (P3-10), and publishes
		// survivors onto the owning worker's Redis queue. Opt-in via
		// ACTION_INTERVAL_SECONDS, mirroring the scrape scheduler: off by
		// default locally so `make up` never depends on Redis being wired.
		// redisClient is non-nil whenever publisher is (both gate on RedisURL).
		if cfg.ActionIntervalSeconds > 0 && redisClient != nil {
			// The composer is the template engine (P3-02/P3-03): it picks an unused
			// variant for the target, renders it, and denylist-screens the result,
			// so the only text a worker ever receives is already safe to post.
			// Same instance the template handler serves above.
			actionSched := service.NewActionScheduler(
				actionRepo,
				accountRepo,
				scrapeRepo,
				templateSvc,
				adapter.NewCooldownGate(redisClient, "smm:cooldown"),
				adapter.NewRateLimiter(redisClient, "smm:ratelimit"),
				publisher,
				service.ActionSchedulerConfig{
					TickBudget: cfg.ActionBatchParallelism * 10,
					Cooldown:   time.Duration(cfg.ActionCooldownSeconds) * time.Second,
					RateWindow: time.Hour,
					RateLimits: cfg.ActionRateLimits,
					Clock:      time.Now,
					Logger:     logger,
				},
			)
			go actionSched.Run(ctx, time.Duration(cfg.ActionIntervalSeconds)*time.Second)
		}

		// Official-accounts API + analytics read models (P2-13 / P2-14).
		analyticsSvc := service.NewAnalyticsService(analyticsRepo, service.AnalyticsConfig{
			Ingestor: analyticsIngestor,
			Clock:    time.Now,
			Logger:   logger,
		})
		deps.Analytics = apihttp.NewAnalyticsHandler(analyticsSvc)

		// Report builder + export (P4-04 / P4-05): read-only pivots over the
		// action queue and the analytics hypertable. The export route carries
		// its own `export` permission gate in the handler.
		reportRepo := repository.NewReportRepo(pg.Queries())
		reportSvc := service.NewReportService(reportRepo, analyticsRepo, logger)
		deps.Reports = apihttp.NewReportHandler(reportSvc)

		// Weekly report digest (P4-06): renders the same report the dashboard
		// shows, attaches the CSV, mails a recipient list. Off by default;
		// enabled with REPORT_EMAIL_INTERVAL_SECONDS (ticket: weekly = 604800).
		if cfg.ReportEmailIntervalSeconds > 0 {
			mailer := smtpadapter.NewMailer(smtpadapter.Config{
				Addr:     cfg.SmtpAddr,
				Host:     cfg.SmtpHost,
				From:     cfg.SmtpFrom,
				Username: cfg.SmtpUsername,
				Password: cfg.SmtpPassword,
			})
			digest := service.NewReportEmailService(reportSvc, mailer, service.ReportEmailConfig{
				Recipients: cfg.ReportEmailRecipients,
				SenderName: "JG Social",
				WindowDays: cfg.ReportEmailWindowDays,
				Kind:       service.ExportActions,
				Clock:      time.Now,
				Logger:     logger,
			})
			go digest.Run(ctx, time.Duration(cfg.ReportEmailIntervalSeconds)*time.Second)
		}

		// Alert engine (P2-07): views-drop and mention-spike rules over scraped
		// metrics. Off by default; enabled with ALERT_INTERVAL_SECONDS.
		if cfg.AlertIntervalSeconds > 0 {
			alerts := service.NewAlertEngine(scrapeRepo, analyticsRepo, service.AlertEngineConfig{
				Clock:  time.Now,
				Logger: logger,
			})
			go alerts.Run(ctx, time.Duration(cfg.AlertIntervalSeconds)*time.Second)
		}

		// Metric aggregator (P2-05): re-samples the top-N posts' latest metrics
		// so the monitoring hypertable stays continuous between scrapes. Off by
		// default; enabled with AGGREGATE_INTERVAL_SECONDS (ticket: 30 min).
		if cfg.AggregateIntervalSeconds > 0 {
			agg := service.NewMetricAggregator(scrapeRepo, service.MetricAggregatorConfig{
				Clock:  time.Now,
				Logger: logger,
			})
			go agg.Run(ctx, time.Duration(cfg.AggregateIntervalSeconds)*time.Second)
		}

		// The desired-state loop is opt-in via RECONCILE_INTERVAL_SECONDS. It is
		// off by default so `make up` locally never depends on it (P1-04).
		if cfg.ReconcileIntervalSeconds > 0 {
			reconciler := service.NewReconciler(workerRepo, driver, logger)
			go reconciler.Run(ctx, time.Duration(cfg.ReconcileIntervalSeconds)*time.Second)

			// Orphan sweeper: deletes platform resources whose Worker row is
			// gone, after a 60s grace so a mid-create pod is never reaped
			// (P1-06). Shares the reconcile interval.
			sweeper := service.NewOrphanSweeper(workerRepo, driver, service.SweeperConfig{
				Clock:  adapter.SystemClock{},
				Logger: logger,
			})
			go sweeper.Run(ctx, time.Duration(cfg.ReconcileIntervalSeconds)*time.Second)
		}
	}

	e := apihttp.NewRouter(deps)

	logger.Info("api listening", "addr", cfg.HTTPAddress, "provisionerMode", cfg.ProvisionerMode)

	// Run the server; on ctx cancellation, drain gracefully.
	if err := run(ctx, e, cfg.HTTPAddress, time.Duration(cfg.ShutdownTimeoutSeconds)*time.Second, logger); err != nil {
		logger.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

// run starts the Echo server and blocks until ctx is cancelled or the server
// errors, then performs a graceful shutdown bounded by timeout.
func run(ctx context.Context, e *echo.Echo, addr string, timeout time.Duration, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := e.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}

// provisionDriver picks the container-platform driver for the configured tier:
// k8s inside a cluster, docker on a workstation, static for bookkeeping-only.
// All three implement port.K8sClient, so the reconciler and services are
// tier-agnostic.
func provisionDriver(ctx context.Context, cfg config.Config, workers port.WorkerStore, logs port.ProvisionLogStore, logger *slog.Logger) (port.K8sClient, error) {
	switch cfg.ProvisionerMode {
	case "k8s":
		logger.Info("provisioner: k8s driver", "namespace", cfg.K8sNamespace)
		return k8s.New(ctx, k8s.Config{
			Namespace:        cfg.K8sNamespace,
			Image:            cfg.WorkerImage,
			CreatesPerMinute: 10,
			Logger:           logger,
		})
	case "docker":
		// The local tier's real driver: the API itself launches worker
		// containers through the mounted socket. Local-only and opt-in
		// (REMEDIATION_PLAN §1.5): k8s stays the production path.
		logger.Info("provisioner: docker driver", "socket", cfg.DockerSocket, "network", cfg.DockerNetwork)
		return dockerprovisioner.New(dockerprovisioner.Config{
			SocketPath: cfg.DockerSocket,
			Network:    cfg.DockerNetwork,
			Image:      cfg.WorkerImage,
			PublicHost: cfg.DockerPublicHost,
			PortMin:    cfg.NovncPortMin,
			PortMax:    cfg.NovncPortMax,
			Platforms:  cfg.WorkerPlatforms,
			DryRun:     cfg.ActionDryRun,
			Logger:     logger,
		})
	default:
		logger.Info("provisioner: static driver (no cluster); scale locally with docker compose")
		return adapter.NewStaticProvisioner(workers, logs, adapter.SystemClock{}, logger), nil
	}
}

// actorForPlatform maps a platform to its configured Apify actor id. The ids
// are derived from the actor prefix in config so an actor change ships without
// a rebuild. The MVP enables Instagram + Threads only; every other platform
// returns empty and the runner rejects the job up front.
func actorForPlatform(cfg config.Config) func(domain.Platform) string {
	return func(p domain.Platform) string {
		switch p {
		case domain.PlatformInstagram:
			return cfg.ApifyActorPrefix + "/instagram-scraper"
		case domain.PlatformThreads:
			return cfg.ApifyActorPrefix + "/threads-scraper"
		}
		return ""
	}
}

// healthcheck probes the local server; used by the container HEALTHCHECK.
func healthcheck() int {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8080/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// seed creates the bootstrap owner user from env (SEED_ADMIN_EMAIL /
// SEED_ADMIN_PASSWORD). It is idempotent: an existing email is left untouched.
// This is a local/ops convenience; production uses the invite flow.
func seed() int {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		return 1
	}
	slog.SetDefault(obs.NewLogger(cfg.LogLevel))

	email := os.Getenv("SEED_ADMIN_EMAIL")
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	if email == "" || password == "" {
		slog.Error("seed requires SEED_ADMIN_EMAIL and SEED_ADMIN_PASSWORD")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if cfg.DatabaseURL == "" {
		slog.Error("seed requires DATABASE_URL")
		return 1
	}
	pg, err := adapter.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connect failed", "err", err)
		return 1
	}
	defer pg.Close()

	repo := repository.NewAuthRepo(pg.Queries())
	if _, err := repo.GetByEmail(ctx, email); err == nil {
		slog.Info("seed: user already exists", "email", email)
		return 0
	}

	hash, err := service.HashPassword(password)
	if err != nil {
		slog.Error("hash password failed", "err", err)
		return 1
	}
	user, err := repo.Create(ctx, email, "Owner", hash, domain.RoleOwner)
	if err != nil {
		slog.Error("create user failed", "err", err)
		return 1
	}
	slog.Info("seed: owner created", "userId", user.ID, "email", user.Email)
	return 0
}
