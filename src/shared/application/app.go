// Package application wires the shared use cases to one NEPSE client. Both binaries build exactly one App at startup and share nothing else.
package application

import (
	"context"
	"fmt"
	"log/slog"

	"nepse-api/src/shared/config"
	"nepse-api/src/shared/market"
	"nepse-api/src/shared/nepse"
	"nepse-api/src/shared/securities"
	"nepse-api/src/shared/trading"
)

// App is the composition root's product: the client plus the use cases.
type App struct {
	Client     *nepse.Client
	Market     *market.Service
	Securities *securities.Service
	Trading    *trading.Service
}

// New builds the NEPSE client (loading assets, compiling css.wasm) and the use cases.
func New(cfg config.Common, logger *slog.Logger) (*App, error) {
	ncfg := cfg.NEPSE
	ncfg.Logger = logger
	client, err := nepse.NewClient(ncfg)
	if err != nil {
		return nil, fmt.Errorf("create NEPSE client: %w", err)
	}
	return &App{
		Client:     client,
		Market:     market.New(client),
		Securities: securities.New(client),
		Trading:    trading.New(client),
	}, nil
}

// Close releases the NEPSE client.
func (a *App) Close(ctx context.Context) error { return a.Client.Close(ctx) }
