package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMandatoryCheck_DevBuildExempt(t *testing.T) {
	dir := t.TempDir()
	if err := MandatoryCheck(dir, "dev", "http://127.0.0.1:1"); err != nil {
		t.Errorf("dev build should be exempt: %v", err)
	}
	if err := MandatoryCheck(dir, "", "http://127.0.0.1:1"); err != nil {
		t.Errorf("empty version should be exempt: %v", err)
	}
}

func TestMandatoryCheck_VersionAcceptable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VersionManifest{
			LatestVersion:      "v0.5.0",
			MinRequiredVersion: "v0.4.0",
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := MandatoryCheck(dir, "v0.4.0", srv.URL); err != nil {
		t.Errorf("v0.4.0 should be acceptable: %v", err)
	}
	if err := MandatoryCheck(dir, "v0.5.0", srv.URL); err != nil {
		t.Errorf("v0.5.0 should be acceptable: %v", err)
	}
}

func TestMandatoryCheck_VersionBelowMin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VersionManifest{
			LatestVersion:      "v0.5.0",
			MinRequiredVersion: "v0.4.0",
			Message:            "Critical security fix",
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	err := MandatoryCheck(dir, "v0.3.0", srv.URL)
	if err == nil {
		t.Fatal("expected UpdateRequiredError")
	}
	if !IsUpdateRequired(err) {
		t.Errorf("error should be UpdateRequiredError, got %T", err)
	}

	var ureErr *UpdateRequiredError
	if !asUpdateRequired(err, &ureErr) {
		t.Fatal("could not unwrap UpdateRequiredError")
	}
	if ureErr.Current != "v0.3.0" {
		t.Errorf("Current = %q, want v0.3.0", ureErr.Current)
	}
	if ureErr.Required != "v0.4.0" {
		t.Errorf("Required = %q, want v0.4.0", ureErr.Required)
	}
	if ureErr.Message != "Critical security fix" {
		t.Errorf("Message = %q", ureErr.Message)
	}
}

// asUpdateRequired is a tiny helper to avoid importing errors in every test.
func asUpdateRequired(err error, target **UpdateRequiredError) bool {
	if e, ok := err.(*UpdateRequiredError); ok {
		*target = e
		return true
	}
	return false
}

func TestMandatoryCheck_NoKillSwitch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VersionManifest{
			LatestVersion:      "v0.5.0",
			MinRequiredVersion: "v0.0.0",
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := MandatoryCheck(dir, "v0.1.0", srv.URL); err != nil {
		t.Errorf("v0.0.0 minimum should disable kill switch: %v", err)
	}
}

func TestMandatoryCheck_NetworkErrorSoftFail(t *testing.T) {
	dir := t.TempDir()
	err := MandatoryCheck(dir, "v0.5.0", "http://127.0.0.1:1")
	if err == nil {
		t.Error("expected network error")
	}
	if IsUpdateRequired(err) {
		t.Error("network error should not be UpdateRequiredError")
	}
}

func TestMandatoryCheck_CachedKillSwitchSurvivesOffline(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed cache with a kill switch.
	saveMandatoryCache(dir, &mandatoryCache{
		CheckedAt:          time.Now().Add(-30 * time.Minute), // fresh
		LatestVersion:      "v0.5.0",
		MinRequiredVersion: "v0.4.0",
		Message:            "rotate",
	})

	// Even with unreachable URL, fresh cache should enforce.
	err := MandatoryCheck(dir, "v0.3.0", "http://127.0.0.1:1")
	if !IsUpdateRequired(err) {
		t.Errorf("cached kill switch should enforce offline, got %v", err)
	}
}

func TestMandatoryCheck_StaleCacheTriggersRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VersionManifest{
			LatestVersion:      "v0.6.0",
			MinRequiredVersion: "v0.0.0", // kill switch lifted
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	saveMandatoryCache(dir, &mandatoryCache{
		CheckedAt:          time.Now().Add(-2 * time.Hour), // stale
		LatestVersion:      "v0.5.0",
		MinRequiredVersion: "v0.4.0",
	})

	// Stale cache says v0.3.0 is too old, but refresh lifts the kill switch.
	if err := MandatoryCheck(dir, "v0.3.0", srv.URL); err != nil {
		t.Errorf("refresh should lift kill switch: %v", err)
	}

	cache, err := loadMandatoryCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cache.MinRequiredVersion != "v0.0.0" {
		t.Errorf("cache should have refreshed minimum, got %q", cache.MinRequiredVersion)
	}
}

func TestMandatoryCheck_NetworkErrorFallsBackToCache(t *testing.T) {
	dir := t.TempDir()
	saveMandatoryCache(dir, &mandatoryCache{
		CheckedAt:          time.Now().Add(-2 * time.Hour), // stale
		LatestVersion:      "v0.5.0",
		MinRequiredVersion: "v0.4.0",
	})

	err := MandatoryCheck(dir, "v0.3.0", "http://127.0.0.1:1")
	if !IsUpdateRequired(err) {
		t.Errorf("stale cache + network failure should still enforce, got %v", err)
	}
}

func TestMandatoryCheck_CachePersisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VersionManifest{
			LatestVersion:      "v0.5.0",
			MinRequiredVersion: "v0.4.0",
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	_ = MandatoryCheck(dir, "v0.5.0", srv.URL)

	if _, err := os.Stat(filepath.Join(dir, mandatoryCacheFile)); err != nil {
		t.Errorf("cache file should exist: %v", err)
	}
}

func TestUpdateRequiredError_Format(t *testing.T) {
	e := &UpdateRequiredError{Current: "v0.3.0", Required: "v0.4.0"}
	if !contains(e.Error(), "v0.3.0") || !contains(e.Error(), "v0.4.0") {
		t.Errorf("error should mention versions: %s", e.Error())
	}

	withMsg := &UpdateRequiredError{Current: "v0.3.0", Required: "v0.4.0", Message: "rotate keys"}
	if !contains(withMsg.Error(), "rotate keys") {
		t.Errorf("error should include message: %s", withMsg.Error())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
