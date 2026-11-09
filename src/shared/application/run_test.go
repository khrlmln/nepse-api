package application

import (
	"strings"
	"testing"

	"nepse-api/src/shared/config"
	"nepse-api/src/shared/nepse"
)

func TestProductionBuildRejectsInsecureUpstreamTransport(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Common
	}{
		{"skip TLS verification", config.Common{NEPSE: nepse.Config{InsecureSkipVerify: true}}},
		{"allow plain HTTP", config.Common{NEPSE: nepse.Config{AllowInsecureHTTP: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBuildSecurity(tc.cfg, "production")
			if err == nil || !strings.Contains(err.Error(), "production build refuses insecure NEPSE transport") {
				t.Fatalf("expected production security rejection, got %v", err)
			}
		})
	}
}

func TestDevelopmentBuildCanUseExplicitTestTransportOverrides(t *testing.T) {
	cfg := config.Common{NEPSE: nepse.Config{InsecureSkipVerify: true, AllowInsecureHTTP: true}}
	if err := validateBuildSecurity(cfg, "development"); err != nil {
		t.Fatalf("development build unexpectedly rejected test transport settings: %v", err)
	}
}

func TestProductionBuildAllowsVerifiedHTTPSConfiguration(t *testing.T) {
	if err := validateBuildSecurity(config.Common{}, "production"); err != nil {
		t.Fatalf("normal production configuration rejected: %v", err)
	}
}
