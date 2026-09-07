package auth

import (
	"strings"
	"testing"
)

func TestGraphConfig(t *testing.T) {
	cfg := GraphConfig("app-guid", "organizations")

	if cfg.ClientID != "app-guid" {
		t.Errorf("ClientID = %q", cfg.ClientID)
	}
	if cfg.ClientSecret != "" {
		t.Errorf("ClientSecret = %q, want empty (public client)", cfg.ClientSecret)
	}
	if !strings.Contains(cfg.Endpoint.AuthURL, "/organizations/") ||
		!strings.Contains(cfg.Endpoint.TokenURL, "/organizations/") {
		t.Errorf("endpoint does not carry the tenant: %+v", cfg.Endpoint)
	}

	want := map[string]bool{ScopeGraphCalendarsRead: true, scopeOfflineAccess: true}
	if len(cfg.Scopes) != len(want) {
		t.Fatalf("scopes = %v", cfg.Scopes)
	}
	for _, s := range cfg.Scopes {
		if !want[s] {
			t.Errorf("unexpected scope %q", s)
		}
	}
}

func TestGraphConfigDefaultTenant(t *testing.T) {
	cfg := GraphConfig("app-guid", "")
	if !strings.Contains(cfg.Endpoint.AuthURL, "/common/") {
		t.Errorf("empty tenant did not default to common: %s", cfg.Endpoint.AuthURL)
	}
}
