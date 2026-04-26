// Package packaging contains tests that validate the DevRecall
// distribution artifacts (nfpm config and the release workflow).
// These configs live in the repo root rather than under a Go package,
// so the tests read them as plain files. Homebrew formulas live in
// the dedicated tap repo (pavelpilyak/homebrew-devrecall) and are
// validated there.
package packaging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot returns the repository root as an absolute path, derived
// from the location of this test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestNFPMConfig_HasRequiredFields(t *testing.T) {
	root := repoRoot(t)
	content := readFile(t, filepath.Join(root, "packaging", "nfpm.yaml"))

	required := []string{
		"name: devrecall",
		"arch: ${NFPM_ARCH}",
		"platform: linux",
		"version: ${NFPM_VERSION}",
		"maintainer:",
		"homepage: https://devrecall.dev",
		"contents:",
		"dst: /usr/bin/devrecall",
	}
	for _, line := range required {
		if !strings.Contains(content, line) {
			t.Errorf("nfpm.yaml missing required line: %q", line)
		}
	}

	// Every arch-templated source path must use the same placeholder as `arch`.
	if !strings.Contains(content, "bin/devrecall-linux-${NFPM_ARCH}") {
		t.Error("nfpm.yaml source binary path does not use ${NFPM_ARCH} placeholder")
	}
}

func TestReleaseWorkflow_BuildsLinuxAndDeb(t *testing.T) {
	root := repoRoot(t)
	content := readFile(t, filepath.Join(root, ".github", "workflows", "release.yml"))

	required := []string{
		"build-cli-linux:",          // Linux build job exists
		"nfpm package",              // .deb produced via nfpm
		"devrecall-linux-x86_64",    // Linux amd64 tarball
		"devrecall-linux-aarch64",   // Linux arm64 tarball
		"aarch64-linux-gnu-gcc",     // cross-compiler for arm64
		"*.deb",                     // .deb files uploaded to release
		"Formula/devrecall-cli.rb",  // formula rewritten at release time
	}
	for _, s := range required {
		if !strings.Contains(content, s) {
			t.Errorf("release.yml missing required token: %q", s)
		}
	}

	// The release job must depend on both CLI build matrices.
	if !strings.Contains(content, "needs: [build-cli, build-cli-linux, build-desktop]") {
		t.Error("release job does not depend on build-cli-linux")
	}
}
