package token

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"pi-agent/internal/oauth"
)

// Store manages persisting and refreshing OAuth credentials on disk.
type Store struct {
	path string
	mu   sync.Mutex
	cred *oauth.Credentials
}

// NewStore creates a token store that reads/writes credentials to the given
// file path. The parent directory is created if it does not exist.
func NewStore(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating token directory: %w", err)
	}
	s := &Store{path: path}
	_ = s.load() // best-effort load; may not exist yet
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var cred oauth.Credentials
	if err := json.Unmarshal(data, &cred); err != nil {
		return err
	}
	s.cred = &cred
	return nil
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.cred, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("writing credentials: %w", err)
	}
	return nil
}

// HasCredentials returns true if credentials have been loaded or stored.
func (s *Store) HasCredentials() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cred != nil
}

// Save persists new credentials to disk.
func (s *Store) Save(cred *oauth.Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cred = cred
	return s.save()
}

// AccessToken returns a valid access token, refreshing automatically if
// the current one is expired. Returns an error if no credentials exist.
func (s *Store) AccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cred == nil {
		return "", fmt.Errorf("no credentials stored; authenticate first")
	}

	if !s.cred.IsExpired() {
		return s.cred.AccessToken, nil
	}

	tokenResp, err := oauth.RefreshToken(s.cred.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refreshing token: %w", err)
	}

	s.cred.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		s.cred.RefreshToken = tokenResp.RefreshToken
	}
	s.cred.ExpiresAt = time.Now().Unix() + int64(tokenResp.ExpiresIn)

	if err := s.save(); err != nil {
		return "", fmt.Errorf("saving refreshed token: %w", err)
	}

	return s.cred.AccessToken, nil
}
