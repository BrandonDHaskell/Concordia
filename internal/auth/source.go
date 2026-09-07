package auth

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
)

// TokenSource returns an oauth2.TokenSource for ref that loads the stored token,
// refreshes it as needed, and writes any refreshed token back to the store. It
// returns ErrNoToken (wrapped) if the account has not been authorized yet.
func TokenSource(ctx context.Context, cfg *oauth2.Config, store *FileTokenStore, ref string) (oauth2.TokenSource, error) {
	tok, err := store.Load(ref)
	if err != nil {
		return nil, err
	}
	base := cfg.TokenSource(ctx, tok)
	return oauth2.ReuseTokenSource(tok, &savingSource{
		store: store,
		ref:   ref,
		base:  base,
		last:  tok.AccessToken,
	}), nil
}

// savingSource persists a token whenever the underlying source hands back a new
// access token, so a refresh survives a restart.
type savingSource struct {
	store *FileTokenStore
	ref   string
	base  oauth2.TokenSource
	last  string
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	tok, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	if tok.AccessToken != s.last {
		if err := s.store.Save(s.ref, tok); err != nil {
			return nil, fmt.Errorf("auth: persisting refreshed token for %q: %w", s.ref, err)
		}
		s.last = tok.AccessToken
	}
	return tok, nil
}
