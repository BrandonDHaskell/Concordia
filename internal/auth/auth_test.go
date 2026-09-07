package auth

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestFileTokenStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileTokenStore(dir)
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}

	want := &oauth2.Token{
		AccessToken:  "at-abc",
		TokenType:    "Bearer",
		RefreshToken: "rt-xyz",
		Expiry:       time.Now().Add(time.Hour).UTC().Truncate(time.Second),
	}
	if err := store.Save("gmail", want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "gmail.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file perms = %o, want 600", perm)
	}

	got, err := store.Load("gmail")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken ||
		got.TokenType != want.TokenType || !got.Expiry.Equal(want.Expiry) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestFileTokenStoreLoadMissing(t *testing.T) {
	store, _ := NewFileTokenStore(t.TempDir())
	_, err := store.Load("nope")
	if err == nil || !strings.Contains(err.Error(), "no stored token") {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestFileTokenStoreRejectsUnsafeRef(t *testing.T) {
	store, _ := NewFileTokenStore(t.TempDir())
	for _, ref := range []string{"", ".", "..", "../evil", "a/b", `a\b`} {
		if _, err := store.Load(ref); err == nil {
			t.Errorf("Load(%q) accepted an unsafe ref", ref)
		}
		if err := store.Save(ref, &oauth2.Token{}); err == nil {
			t.Errorf("Save(%q) accepted an unsafe ref", ref)
		}
	}
}

func TestLoadGoogleConfig(t *testing.T) {
	const secret = `{"installed":{"client_id":"cid.apps.googleusercontent.com",` +
		`"client_secret":"shh","auth_uri":"https://accounts.google.com/o/oauth2/auth",` +
		`"token_uri":"https://oauth2.googleapis.com/token","redirect_uris":["http://localhost"]}}`
	path := filepath.Join(t.TempDir(), "client_secret.json")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadGoogleConfig(path)
	if err != nil {
		t.Fatalf("LoadGoogleConfig: %v", err)
	}
	if cfg.ClientID != "cid.apps.googleusercontent.com" || cfg.ClientSecret != "shh" {
		t.Errorf("client credentials not parsed: %+v", cfg)
	}
	if len(cfg.Scopes) != 1 || cfg.Scopes[0] != ScopeGoogleCalendarReadonly {
		t.Errorf("scopes = %v, want just read-only calendar", cfg.Scopes)
	}
	if cfg.RedirectURL != "" {
		t.Errorf("RedirectURL = %q, want empty (set per flow)", cfg.RedirectURL)
	}
}

func TestSystemdCredentialPath(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "/run/creds/concordia.service")
	got, ok := SystemdCredentialPath("google_oauth")
	if !ok || got != "/run/creds/concordia.service/google_oauth" {
		t.Fatalf("got (%q, %v)", got, ok)
	}

	os.Unsetenv("CREDENTIALS_DIRECTORY")
	if _, ok := SystemdCredentialPath("google_oauth"); ok {
		t.Error("ok = true with no CREDENTIALS_DIRECTORY set")
	}
}

// fakeOAuthServer serves the authorization and token endpoints.
func fakeOAuthServer(t *testing.T, tokenBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unused in tests", http.StatusNotImplemented)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, tokenBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testConfig(srv *httptest.Server) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "cid",
		ClientSecret: "sec",
		Scopes:       []string{ScopeGoogleCalendarReadonly},
		Endpoint: oauth2.Endpoint{
			AuthURL:  srv.URL + "/auth",
			TokenURL: srv.URL + "/token",
		},
	}
}

func TestAuthorizeLoopbackFlow(t *testing.T) {
	srv := fakeOAuthServer(t, `{"access_token":"at-1","refresh_token":"rt-1","token_type":"Bearer","expires_in":3600}`)
	cfg := testConfig(srv)

	pr, pw := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type result struct {
		tok *oauth2.Token
		err error
	}
	done := make(chan result, 1)
	go func() {
		tok, err := Authorize(ctx, cfg, AuthorizeOptions{Prompt: pw})
		done <- result{tok, err}
	}()

	// Read the authorization URL out of the prompt stream.
	authURL := scanForURL(t, pr)
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parsing auth URL %q: %v", authURL, err)
	}
	redirect := u.Query().Get("redirect_uri")
	state := u.Query().Get("state")
	if redirect == "" || state == "" {
		t.Fatalf("auth URL missing redirect_uri/state: %s", authURL)
	}
	if got := u.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256 (PKCE)", got)
	}

	// Play the browser: hit the loopback redirect with a code.
	cbURL := redirect + "?code=the-code&state=" + url.QueryEscape(state)
	resp, err := http.Get(cbURL)
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()

	r := <-done
	if r.err != nil {
		t.Fatalf("Authorize: %v", r.err)
	}
	if r.tok.AccessToken != "at-1" || r.tok.RefreshToken != "rt-1" {
		t.Errorf("token = %+v", r.tok)
	}
}

func TestAuthorizeStateMismatch(t *testing.T) {
	srv := fakeOAuthServer(t, `{"access_token":"at-1"}`)
	cfg := testConfig(srv)

	pr, pw := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := Authorize(ctx, cfg, AuthorizeOptions{Prompt: pw})
		done <- err
	}()

	authURL := scanForURL(t, pr)
	u, _ := url.Parse(authURL)
	redirect := u.Query().Get("redirect_uri")

	resp, err := http.Get(redirect + "?code=x&state=wrong")
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()

	if err := <-done; err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("err = %v, want state mismatch", err)
	}
}

func TestTokenSourcePersistsRefresh(t *testing.T) {
	srv := fakeOAuthServer(t, `{"access_token":"at-refreshed","refresh_token":"rt-1","token_type":"Bearer","expires_in":3600}`)
	cfg := testConfig(srv)

	store, _ := NewFileTokenStore(t.TempDir())
	stale := &oauth2.Token{
		AccessToken:  "at-stale",
		RefreshToken: "rt-1",
		Expiry:       time.Now().Add(-time.Hour),
	}
	if err := store.Save("gmail", stale); err != nil {
		t.Fatal(err)
	}

	ts, err := TokenSource(context.Background(), cfg, store, "gmail")
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "at-refreshed" {
		t.Fatalf("access token = %q, want refreshed", tok.AccessToken)
	}

	stored, err := store.Load("gmail")
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "at-refreshed" {
		t.Errorf("store not updated after refresh: %q", stored.AccessToken)
	}
}

func TestTokenSourceNoToken(t *testing.T) {
	store, _ := NewFileTokenStore(t.TempDir())
	_, err := TokenSource(context.Background(), &oauth2.Config{}, store, "gmail")
	if err == nil || !strings.Contains(err.Error(), "no stored token") {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func scanForURL(t *testing.T, r io.Reader) string {
	t.Helper()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			return line
		}
	}
	t.Fatalf("no URL found in prompt output: %v", sc.Err())
	return ""
}
