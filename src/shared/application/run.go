package application

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"nepse-api/src/shared/config"
	"nepse-api/src/shared/observability"
	"nepse-api/src/shared/version"
)

const appCloseTimeout = 5 * time.Second

// Runner is a server that runs until its context is canceled.
type Runner interface {
	Run(ctx context.Context) error
}

// Run is the shared process lifecycle of both binaries: build the logger, log the startup banner, create the application, build the service-specific runner, run it until SIGINT/SIGTERM, then release resources.
func Run(cfg config.Common, build func(*slog.Logger, *App) (Runner, error)) error {
	if err := validateBuildSecurity(cfg, version.BuildMode); err != nil {
		return err
	}
	log, err := observability.NewLogger(os.Stderr, cfg.LogLevel, cfg.LogFormat, cfg.ServiceName)
	if err != nil {
		return err
	}
	log.Info("starting", "mode", version.BuildMode, "version", version.Version, "commit", version.Commit, "go", runtime.Version())
	if cfg.NEPSE.InsecureSkipVerify {
		log.Warn("NEPSE TLS verification is DISABLED (NEPSE_INSECURE_SKIP_VERIFY); use only for testing")
	}

	app, err := New(cfg, log)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), appCloseTimeout)
		defer cancel()
		if err := app.Close(ctx); err != nil {
			log.Error("closing NEPSE client failed", "error", err)
		}
	}()

	runner, err := build(log, app)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Run(ctx); err != nil {
		return fmt.Errorf("%s: %w", cfg.ServiceName, err)
	}
	return nil
}

// validateBuildSecurity prevents a production-stamped binary from being started with upstream TLS verification disabled or plain-HTTP upstream access enabled. These escape hatches remain available to development/test builds only.
func validateBuildSecurity(cfg config.Common, buildMode string) error {
	if buildMode == "production" && (cfg.NEPSE.InsecureSkipVerify || cfg.NEPSE.AllowInsecureHTTP) {
		return fmt.Errorf("production build refuses insecure NEPSE transport settings; enable certificate verification and HTTPS")
	}
	return nil
}
