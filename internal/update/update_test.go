package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.4.0", "v0.5.0", true},
		{"v0.4.0", "v0.4.1", true},
		{"v0.4.0", "v1.0.0", true},
		{"v0.5.0", "v0.4.0", false},
		{"v0.5.0", "v0.5.0", false},
		{"0.4.0", "0.5.0", true},
		{"v0.4.0-rc1", "v0.4.0", false}, // pre-release stripped → equal
		{"dev", "v0.5.0", true},
		{"", "v0.5.0", true},
		{"v0.5.0", "", false},
		{"v0.4.0", "v0.4.0+build123", false},
		{"v0.10.0", "v0.9.0", false}, // numeric, not lexical
		{"v0.9.0", "v0.10.0", true},
	}
	for _, tc := range cases {
		got := IsNewer(tc.current, tc.latest)
		if got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
	}{
		{"v1.2.3", [3]int{1, 2, 3}},
		{"1.2.3", [3]int{1, 2, 3}},
		{"v0.5.0", [3]int{0, 5, 0}},
		{"v1.0", [3]int{1, 0, 0}},
		{"v0.5.0-rc1", [3]int{0, 5, 0}},
		{"v0.5.0+build", [3]int{0, 5, 0}},
		{"", [3]int{0, 0, 0}},
		{"garbage", [3]int{0, 0, 0}},
	}
	for _, tc := range cases {
		got := parseSemver(tc.in)
		if got != tc.want {
			t.Errorf("parseSemver(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestCheckWithURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("expected User-Agent header")
		}
		json.NewEncoder(w).Encode(Release{
			Version:   "v0.5.0",
			Name:      "v0.5.0",
			Changelog: "Added Linear integration",
			Assets: []Asset{
				{Name: "devrecall_0.5.0_darwin_arm64.tar.gz", URL: "https://example.com/asset"},
				{Name: "checksums.txt", URL: "https://example.com/checksums.txt"},
			},
		})
	}))
	defer srv.Close()

	rel, err := CheckWithURL(srv.URL)
	if err != nil {
		t.Fatalf("CheckWithURL: %v", err)
	}
	if rel.Version != "v0.5.0" {
		t.Errorf("version = %q, want v0.5.0", rel.Version)
	}
	if len(rel.Assets) != 2 {
		t.Errorf("assets = %d, want 2", len(rel.Assets))
	}
}

func TestCheckWithURL_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := CheckWithURL(srv.URL)
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestCheckWithURL_MissingTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name": "no tag"}`))
	}))
	defer srv.Close()

	_, err := CheckWithURL(srv.URL)
	if err == nil {
		t.Fatal("expected error on missing tag_name")
	}
}

func TestFindAsset(t *testing.T) {
	rel := &Release{Assets: []Asset{
		{Name: "devrecall_0.5.0_darwin_arm64.tar.gz"},
		{Name: "devrecall_0.5.0_darwin_amd64.tar.gz"},
		{Name: "devrecall_0.5.0_linux_amd64.tar.gz"},
		{Name: "checksums.txt"},
	}}

	a := rel.FindAsset("darwin", "arm64")
	if a == nil || a.Name != "devrecall_0.5.0_darwin_arm64.tar.gz" {
		t.Errorf("darwin/arm64 mismatch: %+v", a)
	}

	a = rel.FindAsset("linux", "amd64")
	if a == nil || a.Name != "devrecall_0.5.0_linux_amd64.tar.gz" {
		t.Errorf("linux/amd64 mismatch: %+v", a)
	}

	a = rel.FindAsset("windows", "amd64")
	if a != nil {
		t.Errorf("windows/amd64 should be nil, got %+v", a)
	}
}

// TestFindAsset_RealReleaseShape mirrors the actual asset names produced
// by .github/workflows/release.yml — `aarch64`/`x86_64`, not Go's
// runtime.GOARCH values. Regression test for `update` failing with
// "no release asset for darwin/arm64".
func TestFindAsset_RealReleaseShape(t *testing.T) {
	rel := &Release{Assets: []Asset{
		{Name: "devrecall-darwin-aarch64.tar.gz"},
		{Name: "devrecall-darwin-x86_64.tar.gz"},
		{Name: "devrecall-linux-aarch64.tar.gz"},
		{Name: "devrecall-linux-x86_64.tar.gz"},
		{Name: "checksums.sha256"},
	}}

	cases := []struct {
		os, arch, want string
	}{
		{"darwin", "arm64", "devrecall-darwin-aarch64.tar.gz"},
		{"darwin", "amd64", "devrecall-darwin-x86_64.tar.gz"},
		{"linux", "arm64", "devrecall-linux-aarch64.tar.gz"},
		{"linux", "amd64", "devrecall-linux-x86_64.tar.gz"},
	}
	for _, tc := range cases {
		a := rel.FindAsset(tc.os, tc.arch)
		if a == nil || a.Name != tc.want {
			t.Errorf("FindAsset(%q,%q) = %+v, want %q", tc.os, tc.arch, a, tc.want)
		}
	}
}

func TestFindChecksums(t *testing.T) {
	cases := []struct {
		assetName string
		wantFound bool
	}{
		{"checksums.txt", true},
		{"checksums.sha256", true},
		{"sha256sums", true},
		{"sha256sums.txt", true},
		{"binary.tar.gz", false},
	}
	for _, tc := range cases {
		rel := &Release{Assets: []Asset{
			{Name: "devrecall_0.5.0_linux_amd64.tar.gz"},
			{Name: tc.assetName},
		}}
		c := rel.FindChecksums()
		if tc.wantFound {
			if c == nil || c.Name != tc.assetName {
				t.Errorf("FindChecksums for %q: got %+v, want match", tc.assetName, c)
			}
		} else {
			// Filter to the case where only the non-matching asset is present.
			rel = &Release{Assets: []Asset{{Name: tc.assetName}}}
			if rel.FindChecksums() != nil {
				t.Errorf("FindChecksums for %q: expected nil", tc.assetName)
			}
		}
	}
}

// makeTarGz returns the bytes of a tar.gz archive containing a single file
// "devrecall" with the given contents.
func makeTarGz(t *testing.T, contents []byte) []byte {
	t.Helper()
	return makeTarGzWithName(t, "devrecall", contents)
}

// makeTarGzWithName returns a tar.gz containing one file with the given name.
func makeTarGzWithName(t *testing.T, fileName string, contents []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name: fileName,
		Mode: 0o755,
		Size: int64(len(contents)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestApply_SuffixedBinaryName covers the case where the binary inside the
// archive is named "devrecall-<os>-<arch>" (release.yml format) rather than
// bare "devrecall".
func TestApply_SuffixedBinaryName(t *testing.T) {
	binaryContents := []byte("new binary")
	innerName := fmt.Sprintf("devrecall-%s-%s", runtime.GOOS, runtime.GOARCH)
	archive := makeTarGzWithName(t, innerName, binaryContents)
	assetName := fmt.Sprintf("devrecall-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	checksums := fmt.Sprintf("%s  %s\n", sha256Hex(archive), assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/checksums.sha256", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(checksums)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		Assets: []Asset{
			{Name: assetName, URL: srv.URL + "/asset"},
			{Name: "checksums.sha256", URL: srv.URL + "/checksums.sha256"},
		},
	}
	target := filepath.Join(t.TempDir(), "devrecall")
	os.WriteFile(target, []byte("old"), 0o755)

	if err := Apply(rel, target); err != nil {
		t.Fatalf("Apply with suffixed binary: %v", err)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, binaryContents) {
		t.Errorf("binary not replaced: %q", got)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestApply(t *testing.T) {
	binaryContents := []byte("#!/bin/sh\necho new binary\n")
	archive := makeTarGz(t, binaryContents)
	assetName := fmt.Sprintf("devrecall_0.5.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	checksums := fmt.Sprintf("%s  %s\n", sha256Hex(archive), assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksums))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		Version: "v0.5.0",
		Assets: []Asset{
			{Name: assetName, URL: srv.URL + "/asset"},
			{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"},
		},
	}

	target := filepath.Join(t.TempDir(), "devrecall")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Apply(rel, target); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, binaryContents) {
		t.Errorf("binary contents mismatch:\n got: %q\nwant: %q", got, binaryContents)
	}
}

func TestApply_ChecksumMismatch(t *testing.T) {
	archive := makeTarGz(t, []byte("real binary"))
	assetName := fmt.Sprintf("devrecall_0.5.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	wrongChecksum := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	checksums := fmt.Sprintf("%s  %s\n", wrongChecksum, assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksums))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		Assets: []Asset{
			{Name: assetName, URL: srv.URL + "/asset"},
			{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"},
		},
	}

	target := filepath.Join(t.TempDir(), "devrecall")
	os.WriteFile(target, []byte("old"), 0o755)

	err := Apply(rel, target)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}

	// Original binary should be untouched.
	got, _ := os.ReadFile(target)
	if string(got) != "old" {
		t.Errorf("binary should be untouched on checksum failure, got %q", got)
	}
}

// TestApply_PathPrefixedChecksums covers the format produced by
// `find . -type f -exec sha256sum {} +` in our release.yml, where each
// hash line carries a path-prefixed name (./asset-name/asset-name.tar.gz)
// rather than the bare asset filename.
func TestApply_PathPrefixedChecksums(t *testing.T) {
	binaryContents := []byte("new binary")
	archive := makeTarGz(t, binaryContents)
	assetName := fmt.Sprintf("devrecall-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	checksums := fmt.Sprintf("%s  ./%s/%s\n", sha256Hex(archive), assetName, assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/checksums.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksums))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		Assets: []Asset{
			{Name: assetName, URL: srv.URL + "/asset"},
			{Name: "checksums.sha256", URL: srv.URL + "/checksums.sha256"},
		},
	}

	target := filepath.Join(t.TempDir(), "devrecall")
	os.WriteFile(target, []byte("old"), 0o755)

	if err := Apply(rel, target); err != nil {
		t.Fatalf("Apply with path-prefixed checksums: %v", err)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, binaryContents) {
		t.Errorf("binary not replaced: %q", got)
	}
}

func TestApply_NoAssetForPlatform(t *testing.T) {
	rel := &Release{Assets: []Asset{
		{Name: "devrecall_0.5.0_plan9_riscv.tar.gz"},
		{Name: "checksums.txt"},
	}}
	err := Apply(rel, "/tmp/devrecall")
	if err == nil {
		t.Fatal("expected error when no asset matches platform")
	}
}

func TestApply_NoChecksumsAsset(t *testing.T) {
	rel := &Release{Assets: []Asset{
		{Name: fmt.Sprintf("devrecall_0.5.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)},
	}}
	err := Apply(rel, "/tmp/devrecall")
	if err == nil {
		t.Fatal("expected error when no checksums asset")
	}
}

func TestPassiveCheck_FreshCache(t *testing.T) {
	dir := t.TempDir()
	saveCache(dir, &checkCache{
		CheckedAt:     time.Now().Add(-1 * time.Hour),
		LatestVersion: "v0.5.0",
	})

	// URL that would error if called.
	rel, err := PassiveCheck(dir, "v0.4.0", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("fresh cache should not return error: %v", err)
	}
	if rel == nil {
		t.Fatal("expected release from fresh cache")
	}
	if rel.Version != "v0.5.0" {
		t.Errorf("version = %q, want v0.5.0", rel.Version)
	}
}

func TestPassiveCheck_FreshCache_NoNewer(t *testing.T) {
	dir := t.TempDir()
	saveCache(dir, &checkCache{
		CheckedAt:     time.Now().Add(-1 * time.Hour),
		LatestVersion: "v0.5.0",
	})

	rel, err := PassiveCheck(dir, "v0.5.0", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel != nil {
		t.Errorf("expected nil release when current matches latest, got %+v", rel)
	}
}

func TestPassiveCheck_StaleCache_NetworkRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{
			Version:   "v0.6.0",
			Changelog: "fresh!",
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	saveCache(dir, &checkCache{
		CheckedAt:     time.Now().Add(-48 * time.Hour),
		LatestVersion: "v0.5.0",
	})

	rel, err := PassiveCheck(dir, "v0.4.0", srv.URL)
	if err != nil {
		t.Fatalf("PassiveCheck: %v", err)
	}
	if rel == nil || rel.Version != "v0.6.0" {
		t.Errorf("expected v0.6.0 from refresh, got %+v", rel)
	}

	// Cache should now reflect the refreshed version.
	cache, err := loadCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cache.LatestVersion != "v0.6.0" {
		t.Errorf("cache not updated, version = %q", cache.LatestVersion)
	}
}

func TestPassiveCheck_NoCache_NetworkFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Version: "v0.5.0"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	rel, err := PassiveCheck(dir, "v0.4.0", srv.URL)
	if err != nil {
		t.Fatalf("PassiveCheck: %v", err)
	}
	if rel == nil || rel.Version != "v0.5.0" {
		t.Errorf("expected v0.5.0, got %+v", rel)
	}

	// Cache file should now exist.
	if _, err := os.Stat(filepath.Join(dir, checkCacheFile)); err != nil {
		t.Errorf("cache file not created: %v", err)
	}
}

func TestPassiveCheck_NetworkError(t *testing.T) {
	dir := t.TempDir()
	_, err := PassiveCheck(dir, "v0.4.0", "http://127.0.0.1:1")
	if err == nil {
		t.Error("expected error on unreachable URL")
	}
}

func TestFetchChecksums(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# header comment\nabc123  binary1.tar.gz\ndef456 *binary2.tar.gz\n"))
	}))
	defer srv.Close()

	sums, err := fetchChecksums(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if sums["binary1.tar.gz"] != "abc123" {
		t.Errorf("binary1 = %q, want abc123", sums["binary1.tar.gz"])
	}
	if sums["binary2.tar.gz"] != "def456" {
		t.Errorf("binary2 = %q, want def456", sums["binary2.tar.gz"])
	}
}
