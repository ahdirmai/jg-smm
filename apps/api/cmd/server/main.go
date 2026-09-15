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

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/adapter"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/adapter/k8s"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/config"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	apihttp "github.com/ahdirmai/jg-smm-automation/apps/api/internal/http"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/obs"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	checkers := map[string]port.HealthChecker{}
	deps := apihttp.Dependencies{Health: apihttp.NewHealthHandler(service.NewHealthService(checkers))}

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

		// Worker callbacks (heartbeat / auth outcome / action verdict). The
		// worker container POSTs here; it never touches the DB directly.
		workerRepo := repository.NewWorkerRepo(pg.Queries())
		accountRepo := repository.NewAccountRepo(pg.Queries())
		logRepo := repository.NewProvisionLogRepo(pg.Queries())
		jobSvc := service.NewJobService(workerRepo, accountRepo, logRepo, adapter.SystemClock{}, logger)
		deps.Internal = apihttp.NewInternalHandler(jobSvc)

		// Bin-packing: accounts land in the first container with a free platform
		// slot; PROVISION_AUTO_CREATE is the fallback that spawns an AUTO
		// container when the fleet is full (P1-05).
		packer := service.NewPacker(workerRepo, accountRepo, service.PackerConfig{
			MaxPerContainer: cfg.MaxAccountsPerContainer,
			AutoCreate:      cfg.ProvisionAutoCreate,
			Clock:           adapter.SystemClock{},
			Logger:          logger,
		})

		// Container API (P1-19): create/list/delete MANUAL containers. The
		// reconciler provisions the pod from the row this service writes.
		containerSvc := service.NewContainerService(workerRepo, accountRepo, packer, service.ContainerConfig{
			Clock:  adapter.SystemClock{},
			Logger: logger,
		})
		deps.Containers = apihttp.NewContainerHandler(containerSvc)

		// Provisioning driver: k8s in a cluster, static (bookkeeping only) on
		// a workstation. The reconciler is a pure loop over this port, so both
		// tiers share the same code path (P1-03/P1-04). The LoggingDriver wraps
		// either one so every create/delete lands in provision_log (P1-06).
		raw, err := provisionDriver(ctx, cfg, workerRepo, logRepo, logger)
		if err != nil {
			logger.Error("provisioner init failed", "err", err)
			os.Exit(1)
		}
		driver := service.NewLoggingDriver(raw, logRepo, adapter.SystemClock{}, logger)

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
// k8s inside a cluster, static bookkeeping on a workstation. Both implement
// port.K8sClient, so the reconciler and services are tier-agnostic.
func provisionDriver(ctx context.Context, cfg config.Config, workers port.WorkerStore, logs port.ProvisionLogStore, logger *slog.Logger) (port.K8sClient, error) {
	if cfg.ProvisionerMode != "k8s" {
		logger.Info("provisioner: static driver (no cluster); scale locally with docker compose")
		return adapter.NewStaticProvisioner(workers, logs, adapter.SystemClock{}, logger), nil
	}
	logger.Info("provisioner: k8s driver", "namespace", cfg.K8sNamespace)
	return k8s.New(ctx, k8s.Config{
		Namespace:        cfg.K8sNamespace,
		Image:            cfg.WorkerImage,
		CreatesPerMinute: 10,
		Logger:           logger,
	})
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
