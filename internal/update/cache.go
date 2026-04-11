package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// checkCache is the persisted state of the most recent passive update check.
type checkCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
	Changelog     string    `json:"changelog,omitempty"`
}

func cachePath(dir string) string {
	return filepath.Join(dir, checkCacheFile)
}

func loadCache(dir string) (*checkCache, error) {
	data, err := os.ReadFile(cachePath(dir))
	if err != nil {
		return nil, err
	}
	var c checkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func saveCache(dir string, c *checkCache) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cachePath(dir), data, 0o600)
}

// PassiveCheck returns a Release if a newer version is available. It is
// throttled by CheckInterval: if the cache is fresh, it returns the cached
// result without making a network call. Errors are returned but should be
// treated as non-fatal by callers (e.g. printed to stderr or ignored).
//
// Set releasesURL to "" to use DefaultReleasesURL.
func PassiveCheck(dir, currentVersion, releasesURL string) (*Release, error) {
	if releasesURL == "" {
		releasesURL = DefaultReleasesURL
	}

	cache, err := loadCache(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Corrupt cache — ignore and re-fetch.
		cache = nil
	}

	if cache != nil && time.Since(cache.CheckedAt) < CheckInterval {
		if IsNewer(currentVersion, cache.LatestVersion) {
			return &Release{Version: cache.LatestVersion, Changelog: cache.Changelog}, nil
		}
		return nil, nil
	}

	rel, err := CheckWithURL(releasesURL)
	if err != nil {
		return nil, err
	}

	_ = saveCache(dir, &checkCache{
		CheckedAt:     time.Now().UTC(),
		LatestVersion: rel.Version,
		Changelog:     rel.Changelog,
	})

	if IsNewer(currentVersion, rel.Version) {
		return rel, nil
	}
	return nil, nil
}
