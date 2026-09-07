package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
)

// ErrNoToken means no token has been stored for a credential ref yet: the
// account has not completed the authorization flow.
var ErrNoToken = errors.New("auth: no stored token")

// FileTokenStore keeps one 0600 JSON file per credential ref under a directory,
// typically $STATE_DIRECTORY/tokens. Saves are atomic (write temp, rename).
type FileTokenStore struct {
	dir string
}

// NewFileTokenStore returns a store rooted at dir, creating it 0700 if needed.
func NewFileTokenStore(dir string) (*FileTokenStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("auth: empty token store directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("auth: creating token store %s: %w", dir, err)
	}
	return &FileTokenStore{dir: dir}, nil
}

// Load returns the stored token for ref, or ErrNoToken if there is none.
func (s *FileTokenStore) Load(ref string) (*oauth2.Token, error) {
	path, err := s.path(ref)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%q: %w", ref, ErrNoToken)
	}
	if err != nil {
		return nil, fmt.Errorf("auth: reading token for %q: %w", ref, err)
	}
	tok, err := unmarshalToken(b)
	if err != nil {
		return nil, fmt.Errorf("auth: decoding token for %q: %w", ref, err)
	}
	return tok, nil
}

// Save writes the token for ref, replacing any existing one, with 0600 perms.
func (s *FileTokenStore) Save(ref string, tok *oauth2.Token) error {
	path, err := s.path(ref)
	if err != nil {
		return err
	}
	b, err := marshalToken(tok)
	if err != nil {
		return fmt.Errorf("auth: encoding token for %q: %w", ref, err)
	}

	tmp, err := os.CreateTemp(s.dir, ".tok-*")
	if err != nil {
		return fmt.Errorf("auth: staging token for %q: %w", ref, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("auth: chmod token for %q: %w", ref, err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("auth: writing token for %q: %w", ref, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("auth: closing token for %q: %w", ref, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("auth: replacing token for %q: %w", ref, err)
	}
	return nil
}

// path validates ref as a single safe filename component and returns its full
// path in the store.
func (s *FileTokenStore) path(ref string) (string, error) {
	if ref == "" || ref != filepath.Base(ref) || strings.ContainsAny(ref, `/\`) || ref == "." || ref == ".." {
		return "", fmt.Errorf("auth: invalid credential ref %q", ref)
	}
	return filepath.Join(s.dir, ref+".json"), nil
}
