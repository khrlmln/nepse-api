package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"nepse-api/src/shared/config"
	"nepse-api/src/shared/nepse"
)

func TestNewBuildsSharedServicesAndClosesClient(t *testing.T) {
	app, err := New(config.Common{
		NEPSE: nepse.Config{BaseURL: "https://www.nepalstock.com"},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	if app.Client == nil || app.Market == nil || app.Securities == nil || app.Trading == nil {
		t.Fatal("application composition returned a nil dependency")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Close(ctx); err != nil {
		t.Fatalf("close application: %v", err)
	}
}
