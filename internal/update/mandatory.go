package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultVersionURL is the relay endpoint that publishes the security
	// kill-switch manifest. The CLI must refuse to run when its embedded version
	// is older than the manifest's min_required_version.
	DefaultVersionURL = "https://relay.devrecall.dev/v1/version"

	// MandatoryCheckInterval is the minimum time between mandatory-version
	// checks. Shorter than the passive update interval because security kills
	// should propagate quickly.
	MandatoryCheckInterval = 1 * time.Hour

	mandatoryCacheFile = "version_required.json"
)

// VersionManifest is the response from the relay /v1/version endpoint.
type VersionManifest struct {
	LatestVersion      string `json:"latest_version"`
	MinRequiredVersion string `json:"min_required_version"`
	Message            string `json:"message,omitempty"`
}

// UpdateRequiredError is returned by MandatoryCheck when the running version is
// below MinRequiredVersion. Callers should print Error() and exit non-zero.
type UpdateRequiredError struct {
	Current  string
	Required string
	Message  string
}

func (e *UpdateRequiredError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("update required: %s (running %s, minimum %s). %s", e.Required, e.Current, e.Required, e.Message)
	}
	return fmt.Sprintf("update required: %s (running %s, minimum %s)", e.Required, e.Current, e.Required)
}

// IsUpdateRequired reports whether err is an UpdateRequiredError.
func IsUpdateRequired(err error) bool {
	var u *UpdateRequiredError
	return errors.As(err, &u)
}

type mandatoryCache struct {
	CheckedAt          time.Time `json:"checked_at"`
	LatestVersion      string    `json:"latest_version"`
	MinRequiredVersion string    `json:"min_required_version"`
	Message            string    `json:"message,omitempty"`
}

func mandatoryCachePath(dir string) string {
	return filepath.Join(dir, mandatoryCacheFile)
}

func loadMandatoryCache(dir string) (*mandatoryCache, error) {
	data, err := os.ReadFile(mandatoryCachePath(dir))
	if err != nil {
		return nil, err
	}
	var c mandatoryCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func saveMandatoryCache(dir string, c *mandatoryCache) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mandatoryCachePath(dir), data, 0o600)
}

// fetchManifest queries url for the version manifest.
func fetchManifest(url string) (*VersionManifest, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "devrecall-version-check")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version endpoint returned status %d", resp.StatusCode)
	}

	var m VersionManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// MandatoryCheck consults the relay version manifest. It returns:
//   - nil, nil          → check skipped (e.g. dev build) or version is acceptable
//   - nil, error        → network/server error (caller should treat as soft failure)
//   - *UpdateRequiredError → version is below min_required_version (HARD STOP)
//
// Throttled via MandatoryCheckInterval. Cached results from a previous check
// are honored even when offline so a known kill-switch survives reboots.
//
// Pass versionURL = "" to use DefaultVersionURL.
func MandatoryCheck(dir, currentVersion, versionURL string) error {
	if currentVersion == "" || currentVersion == "dev" {
		// Dev builds are exempt from the kill switch.
		return nil
	}
	if versionURL == "" {
		versionURL = DefaultVersionURL
	}

	cache, err := loadMandatoryCache(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cache = nil
	}

	if cache != nil && time.Since(cache.CheckedAt) < MandatoryCheckInterval {
		return enforceManifest(currentVersion, &VersionManifest{
			LatestVersion:      cache.LatestVersion,
			MinRequiredVersion: cache.MinRequiredVersion,
			Message:            cache.Message,
		})
	}

	manifest, err := fetchManifest(versionURL)
	if err != nil {
		// Network error: fall back to cached enforcement if available, else soft fail.
		if cache != nil {
			return enforceManifest(currentVersion, &VersionManifest{
				LatestVersion:      cache.LatestVersion,
				MinRequiredVersion: cache.MinRequiredVersion,
				Message:            cache.Message,
			})
		}
		return err
	}

	_ = saveMandatoryCache(dir, &mandatoryCache{
		CheckedAt:          time.Now().UTC(),
		LatestVersion:      manifest.LatestVersion,
		MinRequiredVersion: manifest.MinRequiredVersion,
		Message:            manifest.Message,
	})

	return enforceManifest(currentVersion, manifest)
}

// enforceManifest returns an UpdateRequiredError if currentVersion is below
// manifest.MinRequiredVersion, otherwise nil. An empty or "v0.0.0" minimum is
// treated as "no kill switch active".
func enforceManifest(currentVersion string, manifest *VersionManifest) error {
	min := manifest.MinRequiredVersion
	if min == "" || min == "v0.0.0" || min == "0.0.0" {
		return nil
	}
	if IsNewer(currentVersion, min) {
		return &UpdateRequiredError{
			Current:  currentVersion,
			Required: min,
			Message:  manifest.Message,
		}
	}
	return nil
}
