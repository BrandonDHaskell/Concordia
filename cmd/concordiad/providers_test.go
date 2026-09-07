package main

import (
	"strings"
	"testing"

	"github.com/bhaskell/Concordia/internal/config"
)

func TestOAuthConfigForGraph(t *testing.T) {
	cfg := &config.Config{Graph: config.Graph{ClientID: "app-guid", Tenant: "organizations"}}
	acct := config.Account{Provider: config.ProviderGraph, Name: "Work"}

	oc, err := oauthConfigFor(cfg, acct)
	if err != nil {
		t.Fatalf("oauthConfigFor: %v", err)
	}
	if oc.ClientID != "app-guid" || oc.ClientSecret != "" {
		t.Errorf("client = %q / %q", oc.ClientID, oc.ClientSecret)
	}
	if !strings.Contains(oc.Endpoint.TokenURL, "/organizations/") {
		t.Errorf("token URL = %q", oc.Endpoint.TokenURL)
	}
}

func TestOAuthConfigForGraphMissingClientID(t *testing.T) {
	cfg := &config.Config{}
	acct := config.Account{Provider: config.ProviderGraph, Name: "Work"}
	if _, err := oauthConfigFor(cfg, acct); err == nil {
		t.Fatal("expected an error when [graph] client_id is unset")
	}
}

func TestOAuthConfigForUnknownProvider(t *testing.T) {
	if _, err := oauthConfigFor(&config.Config{}, config.Account{Provider: "icloud"}); err == nil {
		t.Fatal("expected an error for a non-OAuth provider")
	}
}
