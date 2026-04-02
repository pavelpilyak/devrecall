package auth

import (
	"encoding/json"
	"fmt"

	"github.com/zalando/go-keyring"
)

// KeychainTokenStore stores tokens in the OS keychain (macOS Keychain,
// Windows Credential Manager, or Linux Secret Service).
type KeychainTokenStore struct{}

// NewKeychainTokenStore creates a keychain-backed token store.
func NewKeychainTokenStore() *KeychainTokenStore {
	return &KeychainTokenStore{}
}

func keychainKey(vendor, key string) string {
	return fmt.Sprintf("%s_%s", vendor, key)
}

func (s *KeychainTokenStore) Save(vendor, key string, token any) error {
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}
	if err := keyring.Set(serviceName, keychainKey(vendor, key), string(data)); err != nil {
		return fmt.Errorf("keychain save %s/%s: %w", vendor, key, err)
	}
	return nil
}

func (s *KeychainTokenStore) Load(vendor, key string, dst any) error {
	data, err := keyring.Get(serviceName, keychainKey(vendor, key))
	if err != nil {
		return fmt.Errorf("token not found for %s/%s: %w", vendor, key, err)
	}
	return json.Unmarshal([]byte(data), dst)
}

func (s *KeychainTokenStore) Delete(vendor, key string) error {
	err := keyring.Delete(serviceName, keychainKey(vendor, key))
	if err == keyring.ErrNotFound {
		return nil
	}
	return err
}

// KeychainAvailable returns true if the OS keychain is usable.
func KeychainAvailable() bool {
	testKey := "__devrecall_keychain_test__"
	err := keyring.Set(serviceName, testKey, "test")
	if err != nil {
		return false
	}
	keyring.Delete(serviceName, testKey)
	return true
}
