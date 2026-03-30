package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	serviceName = "devrecall"
	tokensDir   = "tokens"
)

// TokenStore abstracts token persistence.
type TokenStore interface {
	Save(vendor, key string, token any) error
	Load(vendor, key string, dst any) error
	Delete(vendor, key string) error
}

// FileTokenStore stores tokens as JSON files in ~/.devrecall/tokens/.
// This is the default store; a keychain-backed store can replace it later.
type FileTokenStore struct {
	baseDir string
}

// NewFileTokenStore creates a store under the given base directory.
// If baseDir is empty, it defaults to ~/.devrecall.
func NewFileTokenStore(baseDir string) (*FileTokenStore, error) {
	if baseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot find home directory: %w", err)
		}
		baseDir = filepath.Join(home, ".devrecall")
	}
	dir := filepath.Join(baseDir, tokensDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating tokens directory: %w", err)
	}
	return &FileTokenStore{baseDir: baseDir}, nil
}

func (s *FileTokenStore) tokenPath(vendor, key string) string {
	filename := fmt.Sprintf("%s_%s.json", vendor, key)
	return filepath.Join(s.baseDir, tokensDir, filename)
}

func (s *FileTokenStore) Save(vendor, key string, token any) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.tokenPath(vendor, key), data, 0o600)
}

func (s *FileTokenStore) Load(vendor, key string, dst any) error {
	data, err := os.ReadFile(s.tokenPath(vendor, key))
	if err != nil {
		return fmt.Errorf("token not found for %s/%s: %w", vendor, key, err)
	}
	return json.Unmarshal(data, dst)
}

func (s *FileTokenStore) Delete(vendor, key string) error {
	err := os.Remove(s.tokenPath(vendor, key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
