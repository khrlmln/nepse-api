package main

import (
	"fmt"
	"log/slog"
	"os"

	mcpserver "nepse-api/src/mcp"
	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadMCP()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	return application.Run(cfg.Common, func(log *slog.Logger, app *application.App) (application.Runner, error) {
		return mcpserver.New(cfg, app, log)
	})
}
