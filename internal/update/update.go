// Package update implements self-update for the devrecall binary. It queries the
// GitHub Releases API for the latest version, compares against the running
// version, downloads the matching asset, verifies its SHA-256 against a
// checksums.txt asset in the same release, and atomically replaces the binary.
//
// PassiveCheck performs a throttled background check (default once per 24h)
// suitable for printing a one-line "update available" notice on every command.
package update

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultReleasesURL is the GitHub API endpoint for the latest devrecall release.
	DefaultReleasesURL = "https://api.github.com/repos/pavelpilyak/devrecall/releases/latest"

	// CheckInterval is the minimum time between passive update checks.
	CheckInterval = 24 * time.Hour

	checkCacheFile = "version_check.json"
	httpTimeout    = 10 * time.Second
)

// Release describes a released version of devrecall.
type Release struct {
	Version   string  `json:"tag_name"`
	Name      string  `json:"name"`
	Changelog string  `json:"body"`
	Assets    []Asset `json:"assets"`
}

// Asset is a single downloadable file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// archAliases maps Go's runtime.GOARCH values to the alternate names
// commonly used in release artifact filenames (e.g. tar.gz produced by
// goreleaser or our release.yml — `arm64` → `aarch64`, `amd64` → `x86_64`).
var archAliases = map[string][]string{
	"arm64": {"arm64", "aarch64"},
	"amd64": {"amd64", "x86_64", "x64"},
	"386":   {"386", "i386", "x86"},
}

// FindAsset returns the asset matching the given OS and architecture, or nil
// if none match. Both OS and arch are matched as case-insensitive substrings.
// arch is tried under common aliases (e.g. arm64 ↔ aarch64) so the lookup
// works regardless of the naming convention used for release artifacts.
func (r *Release) FindAsset(os, arch string) *Asset {
	osLower := strings.ToLower(os)
	aliases, ok := archAliases[strings.ToLower(arch)]
	if !ok {
		aliases = []string{strings.ToLower(arch)}
	}
	for _, alias := range aliases {
		for i := range r.Assets {
			name := strings.ToLower(r.Assets[i].Name)
			if strings.Contains(name, osLower) && strings.Contains(name, alias) {
				return &r.Assets[i]
			}
		}
	}
	return nil
}

// FindChecksums returns the checksums asset, or nil. Accepts the common
// names used by goreleaser, our release.yml, and shasum-style outputs.
func (r *Release) FindChecksums() *Asset {
	for i := range r.Assets {
		name := strings.ToLower(r.Assets[i].Name)
		switch name {
		case "checksums.txt", "checksums.sha256",
			"sha256sums", "sha256sums.txt", "sha256sum.txt":
			return &r.Assets[i]
		}
	}
	return nil
}

// Check queries the default releases URL for the latest release.
func Check() (*Release, error) {
	return CheckWithURL(DefaultReleasesURL)
}

// CheckWithURL queries the given URL (a GitHub Releases API endpoint) for the
// latest release. Used for testing.
func CheckWithURL(url string) (*Release, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "devrecall-update-check")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release lookup returned status %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decoding release info: %w", err)
	}
	if rel.Version == "" {
		return nil, errors.New("release info missing tag_name")
	}
	return &rel, nil
}

// IsNewer reports whether latest is strictly newer than current under semver
// rules. Both inputs may be prefixed with "v"; "dev" or empty current is treated
// as "always older". Pre-release suffixes are stripped before comparison.
func IsNewer(current, latest string) bool {
	if latest == "" {
		return false
	}
	if current == "" || current == "dev" {
		return true
	}
	c := parseSemver(current)
	l := parseSemver(latest)
	for i := 0; i < 3; i++ {
		if l[i] > c[i] {
			return true
		}
		if l[i] < c[i] {
			return false
		}
	}
	return false
}

// parseSemver returns [major, minor, patch] from a version string. Missing
// components default to 0. Invalid components are treated as 0.
func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Strip pre-release / build metadata.
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}

// fetchChecksums downloads a checksums file and parses it as `<sha256>  <name>`
// lines, returning a map keyed by asset name.
func fetchChecksums(url string) (map[string]string, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checksums download returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Allow `<hash>  <name>` and `<hash> *<name>` (binary mode).
		// Some pipelines (e.g. our release.yml) feed a `find`-style path like
		// "./devrecall-darwin-aarch64/devrecall-darwin-aarch64.tar.gz" into
		// sha256sum — collapse to the basename so lookups by asset name work.
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		name = filepath.Base(name)
		out[name] = fields[0]
	}
	return out, nil
}

// downloadVerified downloads url to a temp file and verifies its sha256 matches
// expectedHex. Returns the temp file path on success.
func downloadVerified(url, expectedHex string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("asset download returned status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "devrecall-update-*")
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, expectedHex) {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, got)
	}
	return tmp.Name(), nil
}

// extractBinary extracts the devrecall binary from a tar.gz archive at
// archivePath, writing it to destPath with mode 0755. Accepts entries
// named "devrecall" (legacy/goreleaser) or "devrecall-<os>-<arch>" /
// "devrecall_<os>_<arch>" (release.yml format). Returns nil on success.
func extractBinary(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("opening gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return errors.New("devrecall binary not found in archive")
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		base := filepath.Base(hdr.Name)
		if !isDevrecallBinaryName(base) {
			continue
		}
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(destPath)
			return err
		}
		return out.Close()
	}
}

// isDevrecallBinaryName reports whether base is a plausible name for the
// devrecall binary inside a release archive — bare "devrecall" or with a
// platform suffix like "devrecall-darwin-aarch64".
func isDevrecallBinaryName(base string) bool {
	if base == "devrecall" || base == "devrecall.exe" {
		return true
	}
	return strings.HasPrefix(base, "devrecall-") || strings.HasPrefix(base, "devrecall_")
}

// Apply downloads the asset, verifies its sha256 against the checksums file,
// extracts the devrecall binary, and atomically replaces targetPath. The caller
// is responsible for resolving targetPath (usually os.Executable()).
func Apply(rel *Release, targetPath string) error {
	asset := rel.FindAsset(runtime.GOOS, runtime.GOARCH)
	if asset == nil {
		return fmt.Errorf("no release asset for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	checksums := rel.FindChecksums()
	if checksums == nil {
		return errors.New("release has no checksums.txt asset")
	}

	sums, err := fetchChecksums(checksums.URL)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}
	expected, ok := sums[asset.Name]
	if !ok {
		return fmt.Errorf("no checksum for asset %s", asset.Name)
	}

	tmpArchive, err := downloadVerified(asset.URL, expected)
	if err != nil {
		return err
	}
	defer os.Remove(tmpArchive)

	// Extract to a sibling temp file so the rename is atomic on the same FS.
	stagedPath := targetPath + ".new"
	if err := extractBinary(tmpArchive, stagedPath); err != nil {
		os.Remove(stagedPath)
		return err
	}
	if err := os.Rename(stagedPath, targetPath); err != nil {
		os.Remove(stagedPath)
		return fmt.Errorf("replacing binary: %w", err)
	}
	return nil
}
