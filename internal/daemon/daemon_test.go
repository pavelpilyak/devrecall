package daemon

import (
	"strings"
	"testing"
)

func TestGenerateLaunchdPlist(t *testing.T) {
	cfg := Config{
		BinaryPath:  "/usr/local/bin/devrecall",
		IntervalSec: 900,
		LogPath:     "/tmp/devrecall.log",
	}

	plist, err := GenerateLaunchdPlist(cfg)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name     string
		contains string
	}{
		{"label", Label},
		{"binary path", "/usr/local/bin/devrecall"},
		{"sync command", "<string>sync</string>"},
		{"interval", "<integer>900</integer>"},
		{"log path", "/tmp/devrecall.log"},
		{"run at load", "<true/>"},
		{"xml header", `<?xml version="1.0"`},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(plist, c.contains) {
				t.Errorf("plist missing %q\n\nGot:\n%s", c.contains, plist)
			}
		})
	}
}

func TestGenerateSystemdUnit(t *testing.T) {
	cfg := Config{
		BinaryPath:  "/usr/local/bin/devrecall",
		IntervalSec: 600,
		LogPath:     "/tmp/devrecall.log",
	}

	unit, err := GenerateSystemdUnit(cfg)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name     string
		contains string
	}{
		{"description", "DevRecall background sync"},
		{"exec start", "ExecStart=/usr/local/bin/devrecall sync"},
		{"type", "Type=oneshot"},
		{"log path", "/tmp/devrecall.log"},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(unit, c.contains) {
				t.Errorf("unit missing %q\n\nGot:\n%s", c.contains, unit)
			}
		})
	}
}

func TestGenerateSystemdTimer(t *testing.T) {
	cfg := Config{
		BinaryPath:  "/usr/local/bin/devrecall",
		IntervalSec: 600,
		LogPath:     "/tmp/devrecall.log",
	}

	timer, err := GenerateSystemdTimer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name     string
		contains string
	}{
		{"timer description", "DevRecall periodic sync timer"},
		{"interval", "OnUnitActiveSec=600s"},
		{"boot delay", "OnBootSec=60"},
		{"persistent", "Persistent=true"},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(timer, c.contains) {
				t.Errorf("timer missing %q\n\nGot:\n%s", c.contains, timer)
			}
		})
	}
}

func TestDefaultIntervalSec(t *testing.T) {
	if DefaultIntervalSec != 900 {
		t.Errorf("expected default interval 900, got %d", DefaultIntervalSec)
	}
}

func TestLabel(t *testing.T) {
	if Label != "dev.devrecall.agent" {
		t.Errorf("expected label dev.devrecall.agent, got %s", Label)
	}
}

func TestGetStatus_ReturnsStatus(t *testing.T) {
	s, err := GetStatus()
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("expected non-nil status")
	}
	// On this machine, daemon is not installed, so installed should be false.
	if s.Platform == "" {
		t.Error("expected non-empty platform")
	}
}
