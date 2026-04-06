// Package daemon manages background sync scheduling via OS-native service managers
// (launchd on macOS, systemd on Linux).
package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

const (
	// Label is the reverse-DNS identifier for the daemon.
	Label = "dev.devrecall.agent"

	// DefaultIntervalSec is the sync interval in seconds (15 minutes).
	DefaultIntervalSec = 900
)

// Config holds daemon installation parameters.
type Config struct {
	BinaryPath  string // absolute path to devrecall binary
	IntervalSec int    // sync interval in seconds
	LogPath     string // path for stdout/stderr logs
}

// Install installs and starts the background daemon.
func Install(cfg Config) error {
	if cfg.IntervalSec <= 0 {
		cfg.IntervalSec = DefaultIntervalSec
	}
	if cfg.BinaryPath == "" {
		p, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot determine binary path: %w", err)
		}
		cfg.BinaryPath = p
	}
	if cfg.LogPath == "" {
		home, _ := os.UserHomeDir()
		cfg.LogPath = filepath.Join(home, ".devrecall", "daemon.log")
	}

	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(cfg)
	case "linux":
		return installSystemd(cfg)
	default:
		return fmt.Errorf("unsupported platform: %s (supported: darwin, linux)", runtime.GOOS)
	}
}

// Uninstall stops and removes the background daemon.
func Uninstall() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchd()
	case "linux":
		return uninstallSystemd()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// Status returns a human-readable status of the daemon.
type Status struct {
	Installed bool
	Running   bool
	Platform  string // "launchd" or "systemd"
	PID       int    // 0 if not running
	Path      string // path to the service file
}

// GetStatus checks whether the daemon is installed and running.
func GetStatus() (*Status, error) {
	switch runtime.GOOS {
	case "darwin":
		return statusLaunchd()
	case "linux":
		return statusSystemd()
	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// --- macOS launchd ---

const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>sync</string>
    </array>
    <key>StartInterval</key>
    <integer>{{.IntervalSec}}</integer>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`

func launchdPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

// GenerateLaunchdPlist returns the plist XML content.
func GenerateLaunchdPlist(cfg Config) (string, error) {
	tmpl, err := template.New("plist").Parse(launchdPlistTemplate)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	err = tmpl.Execute(&buf, struct {
		Label       string
		BinaryPath  string
		IntervalSec int
		LogPath     string
	}{
		Label:       Label,
		BinaryPath:  cfg.BinaryPath,
		IntervalSec: cfg.IntervalSec,
		LogPath:     cfg.LogPath,
	})
	return buf.String(), err
}

func installLaunchd(cfg Config) error {
	plistPath := launchdPlistPath()

	content, err := GenerateLaunchdPlist(cfg)
	if err != nil {
		return fmt.Errorf("generating plist: %w", err)
	}

	// Ensure LaunchAgents directory exists.
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents dir: %w", err)
	}

	// Unload if already loaded (ignore error if not loaded).
	exec.Command("launchctl", "unload", plistPath).Run()

	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	return nil
}

func uninstallLaunchd() error {
	plistPath := launchdPlistPath()

	// Unload (ignore error if not loaded).
	exec.Command("launchctl", "unload", plistPath).Run()

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist: %w", err)
	}
	return nil
}

func statusLaunchd() (*Status, error) {
	plistPath := launchdPlistPath()
	s := &Status{Platform: "launchd", Path: plistPath}

	if _, err := os.Stat(plistPath); err != nil {
		return s, nil // not installed
	}
	s.Installed = true

	out, err := exec.Command("launchctl", "list", Label).Output()
	if err != nil {
		return s, nil // installed but not loaded
	}

	// Parse launchctl list output for PID.
	// Output format: "{" ... "\"PID\" = NNN;" ... "}"
	output := string(out)
	if strings.Contains(output, `"PID"`) {
		s.Running = true
		// Extract PID.
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, `"PID"`) {
				// Format: "PID" = 12345;
				parts := strings.Split(line, "=")
				if len(parts) == 2 {
					pidStr := strings.TrimSpace(strings.TrimSuffix(parts[1], ";"))
					fmt.Sscanf(pidStr, "%d", &s.PID)
				}
			}
		}
	}

	return s, nil
}

// --- Linux systemd ---

const systemdUnitTemplate = `[Unit]
Description=DevRecall background sync agent
After=network.target

[Service]
Type=oneshot
ExecStart={{.BinaryPath}} sync
StandardOutput=append:{{.LogPath}}
StandardError=append:{{.LogPath}}

[Install]
WantedBy=default.target
`

const systemdTimerTemplate = `[Unit]
Description=DevRecall periodic sync timer

[Timer]
OnBootSec=60
OnUnitActiveSec={{.IntervalSec}}s
Persistent=true

[Install]
WantedBy=timers.target
`

func systemdUnitDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func systemdServicePath() string {
	return filepath.Join(systemdUnitDir(), "devrecall-sync.service")
}

func systemdTimerPath() string {
	return filepath.Join(systemdUnitDir(), "devrecall-sync.timer")
}

// GenerateSystemdUnit returns the systemd service unit content.
func GenerateSystemdUnit(cfg Config) (string, error) {
	tmpl, err := template.New("unit").Parse(systemdUnitTemplate)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	err = tmpl.Execute(&buf, cfg)
	return buf.String(), err
}

// GenerateSystemdTimer returns the systemd timer unit content.
func GenerateSystemdTimer(cfg Config) (string, error) {
	tmpl, err := template.New("timer").Parse(systemdTimerTemplate)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	err = tmpl.Execute(&buf, cfg)
	return buf.String(), err
}

func installSystemd(cfg Config) error {
	unitDir := systemdUnitDir()
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("creating systemd user dir: %w", err)
	}

	serviceContent, err := GenerateSystemdUnit(cfg)
	if err != nil {
		return fmt.Errorf("generating service unit: %w", err)
	}

	timerContent, err := GenerateSystemdTimer(cfg)
	if err != nil {
		return fmt.Errorf("generating timer unit: %w", err)
	}

	if err := os.WriteFile(systemdServicePath(), []byte(serviceContent), 0o644); err != nil {
		return fmt.Errorf("writing service unit: %w", err)
	}
	if err := os.WriteFile(systemdTimerPath(), []byte(timerContent), 0o644); err != nil {
		return fmt.Errorf("writing timer unit: %w", err)
	}

	// Reload and enable the timer.
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", "--now", "devrecall-sync.timer").Run(); err != nil {
		return fmt.Errorf("systemctl enable timer: %w", err)
	}

	return nil
}

func uninstallSystemd() error {
	// Stop and disable timer (ignore errors if not active).
	exec.Command("systemctl", "--user", "disable", "--now", "devrecall-sync.timer").Run()

	for _, path := range []string{systemdTimerPath(), systemdServicePath()} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}

	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func statusSystemd() (*Status, error) {
	s := &Status{Platform: "systemd", Path: systemdTimerPath()}

	if _, err := os.Stat(systemdTimerPath()); err != nil {
		return s, nil // not installed
	}
	s.Installed = true

	out, err := exec.Command("systemctl", "--user", "is-active", "devrecall-sync.timer").Output()
	if err == nil && strings.TrimSpace(string(out)) == "active" {
		s.Running = true
	}

	// Try to get main PID of the service (if currently running a sync).
	pidOut, err := exec.Command("systemctl", "--user", "show", "-p", "MainPID", "devrecall-sync.service").Output()
	if err == nil {
		line := strings.TrimSpace(string(pidOut))
		if strings.HasPrefix(line, "MainPID=") {
			fmt.Sscanf(strings.TrimPrefix(line, "MainPID="), "%d", &s.PID)
		}
	}

	return s, nil
}
