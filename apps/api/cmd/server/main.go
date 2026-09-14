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
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/config"
	apihttp "github.com/ahdirmai/jg-smm-automation/apps/api/internal/http"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/obs"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/service"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
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
	}

	healthSvc := service.NewHealthService(checkers)
	e := apihttp.NewRouter(apihttp.NewHealthHandler(healthSvc))

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
