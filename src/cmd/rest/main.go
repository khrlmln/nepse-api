package main

import (
	"fmt"
	"log/slog"
	"os"

	"nepse-api/src/rest"
	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rest:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadREST()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	return application.Run(cfg.Common, func(log *slog.Logger, app *application.App) (application.Runner, error) {
		return rest.New(cfg, app, log)
	})
}
