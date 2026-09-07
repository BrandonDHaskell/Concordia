package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
)

// AuthorizeOptions tune the loopback flow.
type AuthorizeOptions struct {
	// Prompt is written the authorization URL for the user to open. Defaults
	// to os.Stderr when nil.
	Prompt io.Writer
	// OpenBrowser attempts to launch the URL with the desktop opener.
	OpenBrowser bool
}

// Authorize runs the OAuth loopback flow: it listens on 127.0.0.1, prints an
// authorization URL, waits for the redirect, and exchanges the code (with PKCE)
// for a token. The caller controls the overall deadline through ctx.
func Authorize(ctx context.Context, cfg *oauth2.Config, opts AuthorizeOptions) (*oauth2.Token, error) {
	prompt := opts.Prompt
	if prompt == nil {
		return nil, fmt.Errorf("auth: Authorize requires a Prompt writer")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("auth: opening loopback listener: %w", err)
	}
	defer ln.Close()

	// Copy the config so we do not mutate the caller's, and pin the redirect
	// to this listener.
	local := *cfg
	local.RedirectURL = fmt.Sprintf("http://%s/callback", ln.Addr().String())

	state, err := randToken()
	if err != nil {
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()

	authURL := local.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "consent"),
	)

	fmt.Fprintf(prompt, "Open this URL to authorize:\n\n%s\n\n", authURL)
	if opts.OpenBrowser {
		if err := openBrowser(authURL); err != nil {
			fmt.Fprintf(prompt, "(could not open a browser automatically: %v)\n", err)
		}
	}

	result := make(chan callbackResult, 1)
	srv := &http.Server{
		Handler:           callbackHandler(state, result),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go srv.Serve(ln)
	defer func() {
		// Graceful shutdown so the browser's response finishes flushing
		// before the listener closes.
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("auth: authorization cancelled: %w", ctx.Err())
	case r := <-result:
		if r.err != nil {
			return nil, r.err
		}
		tok, err := local.Exchange(ctx, r.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return nil, fmt.Errorf("auth: exchanging authorization code: %w", err)
		}
		return tok, nil
	}
}

type callbackResult struct {
	code string
	err  error
}

func callbackHandler(wantState string, out chan<- callbackResult) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if e := q.Get("error"); e != "" {
			http.Error(w, "authorization failed", http.StatusBadRequest)
			out <- callbackResult{err: fmt.Errorf("auth: provider returned error %q", e)}
			return
		}
		if q.Get("state") != wantState {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			out <- callbackResult{err: fmt.Errorf("auth: state parameter mismatch")}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			out <- callbackResult{err: fmt.Errorf("auth: redirect carried no code")}
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "Concordia is authorized. You can close this tab.\n")
		out <- callbackResult{code: code}
	}
}

func randToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generating random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	return exec.Command(cmd, append(args, url)...).Start()
}
