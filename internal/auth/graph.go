package auth

import (
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

// Microsoft Graph OAuth scopes. Calendars.Read is delegated read-only calendar
// access; offline_access yields a refresh token.
const (
	ScopeGraphCalendarsRead = "https://graph.microsoft.com/Calendars.Read"
	scopeOfflineAccess      = "offline_access"
)

// GraphConfig builds an oauth2 config for a Microsoft Entra public client (no
// client secret; the loopback flow authenticates with PKCE). tenant is
// "common", "organizations", "consumers", or a directory (tenant) ID; empty
// means "common". RedirectURL is left unset for the loopback flow to fill in.
func GraphConfig(clientID, tenant string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: clientID,
		Endpoint: microsoft.AzureADEndpoint(tenant),
		Scopes:   []string{ScopeGraphCalendarsRead, scopeOfflineAccess},
	}
}
