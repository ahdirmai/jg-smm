package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"log/slog"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// AnalyticsIngestor (P2-12) is the cron that pulls official-account metrics from
// the 3rd-party provider and writes them into the analytics hypertables.
//
// Contract (TICKETS P2-12 AC):
//   - Cron every ANALYTICS_INGEST_INTERVAL + on demand (the dashboard's refresh
//     button enqueues a run, it does not hit the provider directly).
//   - Snapshots are idempotent per (account, ts, provider): a retried run
//     refreshes the same row, never duplicates a point.
//   - Every run writes an AnalyticsIngestRun with SUCCESS / PARTIAL / FAILED so
//     the freshness badge and the operator can tell "attempted" from "succeeded".
//   - One account failing never aborts the batch; the run ends PARTIAL.
type AnalyticsIngestor struct {
	store    port.AnalyticsStore
	provider port.AnalyticsProvider
	clock    func() time.Time
	cfg      AnalyticsIngestorConfig
	log      *slog.Logger
}

// AnalyticsIngestorConfig bounds one ingest pass.
type AnalyticsIngestorConfig struct {
	// BatchSize caps accounts pulled per pass (provider rate courtesy).
	BatchSize int
	// Scope labels the run for the audit trail: "platform:instagram",
	// "account:<id>" or "all".
	Scope string
	// Clock is injectable.
	Clock func() time.Time
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// NewAnalyticsIngestor wires the ingestor. The provider is optional: without it
// the loop records a FAILED run each tick, which is the correct operator signal
// (a configured-but-unwired ingestor is a deployment bug, not silence).
func NewAnalyticsIngestor(store port.AnalyticsStore, provider port.AnalyticsProvider, cfg AnalyticsIngestorConfig) *AnalyticsIngestor {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.Scope == "" {
		cfg.Scope = "all"
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AnalyticsIngestor{
		store:    store,
		provider: provider,
		clock:    cfg.Clock,
		cfg:      cfg,
		log:      cfg.Logger,
	}
}

// Run loops until ctx is cancelled. It is the process-long cron.
func (i *AnalyticsIngestor) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	i.log.Info("analytics ingestor started", "interval", interval, "scope", i.cfg.Scope)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			i.log.Info("analytics ingestor stopped")
			return
		case <-t.C:
			if _, err := i.IngestNow(ctx); err != nil {
				i.log.Error("analytics ingest tick failed", "err", err)
			}
		}
	}
}

// IngestNow runs one pass over every ACTIVE account for the configured provider
// and returns the audit row. Exposed so the API's refresh endpoint and tests can
// trigger a run without waiting for the cron.
func (i *AnalyticsIngestor) IngestNow(ctx context.Context) (domain.AnalyticsIngestRun, error) {
	if i.provider == nil {
		return i.failRun(ctx, "UNCONFIGURED", "analytics provider not configured")
	}
	provider := domain.AnalyticsProviderA
	accounts, err := i.store.ListOfficialAccountsForIngest(ctx, provider, i.cfg.BatchSize)
	if err != nil {
		return i.failRun(ctx, "STORE", fmt.Sprintf("list accounts: %v", err))
	}
	if len(accounts) == 0 {
		// No work is a success: an empty fleet should not raise a FAILED badge.
		run, err := i.store.CreateAnalyticsIngestRun(ctx, domain.AnalyticsProviderA, i.cfg.Scope)
		if err != nil {
			return domain.AnalyticsIngestRun{}, fmt.Errorf("ingestor: record run: %w", err)
		}
		return i.close(ctx, run, domain.IngestStatusSuccess, 0, 0, nil, nil)
	}

	run, err := i.store.CreateAnalyticsIngestRun(ctx, provider, i.cfg.Scope)
	if err != nil {
		return domain.AnalyticsIngestRun{}, fmt.Errorf("ingestor: open run: %w", err)
	}

	if err := i.provider.Health(ctx); err != nil {
		return i.close(ctx, run, domain.IngestStatusFailed, 0, 0,
			ptrString("PROVIDER_HEALTH"), ptrString(truncateErr(err.Error())))
	}

	ok, fail := 0, 0
	var firstErr error
	var firstErrClass string
	for _, acc := range accounts {
		if ctx.Err() != nil {
			break
		}
		if err := i.ingestAccount(ctx, acc); err != nil {
			fail++
			if firstErr == nil {
				firstErr = err
				firstErrClass = classify(err)
			}
			i.log.Warn("ingestor: account failed", "account", acc.Handle, "err", err)
			continue
		}
		ok++
	}

	status := domain.IngestStatusSuccess
	var errClass, errMsg *string
	if fail > 0 {
		if ok == 0 {
			status = domain.IngestStatusFailed
		} else {
			status = domain.IngestStatusPartial
		}
		errClass = &firstErrClass
		msg := truncateErr(firstErr.Error())
		errMsg = &msg
	}
	return i.close(ctx, run, status, ok, fail, errClass, errMsg)
}

// ingestAccount pulls metrics + mentions for one account and writes them.
// Only a fully successful pull touches last_fetched_at, so the freshness badge
// means "data present", not merely "an attempt ran".
func (i *AnalyticsIngestor) ingestAccount(ctx context.Context, acc domain.OfficialAccount) error {
	snap, err := i.provider.FetchMetrics(ctx, acc)
	if err != nil {
		return fmt.Errorf("fetch metrics: %w", err)
	}
	if _, err := i.store.UpsertAnalyticsSnapshot(ctx, snap); err != nil {
		return fmt.Errorf("upsert snapshot: %w", err)
	}

	mentions, err := i.provider.FetchMentions(ctx, acc)
	if err != nil {
		return fmt.Errorf("fetch mentions: %w", err)
	}
	for _, m := range mentions {
		if _, err := i.store.UpsertAnalyticsMention(ctx, m); err != nil {
			return fmt.Errorf("upsert mention: %w", err)
		}
	}

	if err := i.store.TouchOfficialAccountFetched(ctx, acc.ID); err != nil {
		return fmt.Errorf("touch fetched: %w", err)
	}
	return nil
}

// record closes a run with an outcome. It is the only writer of finished_at.
func (i *AnalyticsIngestor) record(ctx context.Context, status domain.IngestStatus, ok, fail int, errClass, errMsg *string) (domain.AnalyticsIngestRun, error) {
	run, err := i.store.CreateAnalyticsIngestRun(ctx, domain.AnalyticsProviderA, i.cfg.Scope)
	if err != nil {
		return domain.AnalyticsIngestRun{}, fmt.Errorf("ingestor: record run: %w", err)
	}
	return i.close(ctx, run, status, ok, fail, errClass, errMsg)
}

// close stamps the outcome on an already-open run.
func (i *AnalyticsIngestor) close(ctx context.Context, run domain.AnalyticsIngestRun, status domain.IngestStatus, ok, fail int, errClass, errMsg *string) (domain.AnalyticsIngestRun, error) {
	run.Status = status
	run.AccountsOk = ok
	run.AccountsErr = fail
	run.ErrorClass = errClass
	run.Error = errMsg
	closed, err := i.store.UpdateAnalyticsIngestRun(ctx, run)
	if err != nil {
		return domain.AnalyticsIngestRun{}, fmt.Errorf("ingestor: close run: %w", err)
	}
	i.log.Info("ingestor: run complete", "status", status, "ok", ok, "fail", fail)
	return closed, nil
}

func (i *AnalyticsIngestor) failRun(ctx context.Context, class, msg string) (domain.AnalyticsIngestRun, error) {
	c := class
	m := truncateErr(msg)
	return i.record(ctx, domain.IngestStatusFailed, 0, 0, &c, &m)
}

// classify turns a provider/store error into a coarse error class for the audit
// trail and the dashboard. Kept deliberately small: the operator needs
// "provider down" vs "data bad", not a taxonomy.
func classify(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case contains(msg, "timeout"), contains(msg, "deadline"), contains(msg, "connection"):
		return "TRANSIENT"
	case contains(msg, "unauthorized"), contains(msg, "401"), contains(msg, "403"):
		return "AUTH"
	case contains(msg, "429"), contains(msg, "rate limit"):
		return "RATE_LIMIT"
	case contains(msg, "not found"):
		return "NOT_FOUND"
	}
	return "UNKNOWN"
}

var _ = errors.New
