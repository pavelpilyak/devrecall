//! CLI process management: discover the devrecall binary, spawn or attach to the API server.

use std::fs::{File, OpenOptions};
use std::path::{Path, PathBuf};
use std::process::Stdio;
use tokio::process::Command;
use tokio::time::{sleep, Duration};

use tauri::Emitter;

use crate::ApiStatus;

pub const DEFAULT_API_PORT: u16 = 3725;

/// Read the configured port from `~/.devrecall/config.json` (`server.port`),
/// falling back to `DEFAULT_API_PORT` if the file is missing, unparseable,
/// or the field is absent/zero.
pub fn configured_port() -> u16 {
    if let Some(home) = dirs_home() {
        let path = home.join(".devrecall/config.json");
        if let Ok(data) = std::fs::read_to_string(&path) {
            if let Ok(v) = serde_json::from_str::<serde_json::Value>(&data) {
                if let Some(port) = v.get("server").and_then(|s| s.get("port")).and_then(|p| p.as_u64()) {
                    if port > 0 && port <= 65535 {
                        return port as u16;
                    }
                }
            }
        }
    }
    DEFAULT_API_PORT
}

fn api_base(port: u16) -> String {
    format!("http://127.0.0.1:{port}")
}
const HEALTH_RETRIES: u32 = 10;
const HEALTH_INTERVAL: Duration = Duration::from_millis(500);

/// How often the watchdog re-checks the API and revives a dead server.
const WATCHDOG_INTERVAL: Duration = Duration::from_secs(15);

/// Check if the API is reachable on the given port.
pub async fn check_api_on(port: u16) -> Result<ApiStatus, Box<dyn std::error::Error>> {
    let resp = reqwest::get(format!("{}/api/status", api_base(port))).await?;
    let status: ApiStatus = resp.json().await?;
    Ok(status)
}

/// Check if the API is reachable on the configured port.
pub async fn check_api() -> Result<ApiStatus, Box<dyn std::error::Error>> {
    check_api_on(configured_port()).await
}

/// Ensure the DevRecall API server is running.
/// If already running, attach (do nothing). Otherwise, spawn `devrecall serve`.
pub async fn ensure_running(
    app: &tauri::AppHandle,
) -> Result<(), Box<dyn std::error::Error>> {
    let port = configured_port();

    // Check if the server is already running.
    if check_api_on(port).await.is_ok() {
        eprintln!("DevRecall API already running on port {port}");
        return Ok(());
    }

    // Find the binary.
    let binary = find_binary().ok_or("devrecall binary not found")?;
    eprintln!("Starting DevRecall API server: {}", binary.display());

    // Redirect the server's stdout/stderr to a persistent log file rather than
    // a pipe. A pipe whose read end we stop owning (when this function returns
    // and the `Child` drops) is fatal: the next stderr write in the server hits
    // a closed pipe, and in Go a SIGPIPE on fd 1/2 terminates the process — so
    // the daemon would die the first time it logged (e.g. the LLM-fallback
    // warning during daily generation). A file never closes or fills, and it
    // doubles as a debugging log.
    let log_path = log_file_path();
    let (out, err): (Stdio, Stdio) = match log_path.as_deref().and_then(open_log_file) {
        Some(file) => match file.try_clone() {
            Ok(clone) => (Stdio::from(file), Stdio::from(clone)),
            Err(_) => (Stdio::from(file), Stdio::null()),
        },
        None => (Stdio::null(), Stdio::null()),
    };

    // Spawn `devrecall serve` as a background child process.
    // The server reads the port from config.json itself; no need to pass --port.
    let mut child = Command::new(&binary)
        .args(["serve"])
        .stdin(Stdio::null())
        .stdout(out)
        .stderr(err)
        .spawn()?;

    // Wait for the server to become healthy.
    for i in 0..HEALTH_RETRIES {
        sleep(HEALTH_INTERVAL).await;

        // If the child exited early, surface the tail of the log file.
        if let Ok(Some(status)) = child.try_wait() {
            if !status.success() {
                let msg = log_path
                    .as_deref()
                    .and_then(read_log_tail)
                    .unwrap_or_default()
                    .trim()
                    .to_string();
                let err_msg = if msg.is_empty() {
                    "DevRecall API server exited unexpectedly".to_string()
                } else {
                    msg
                };
                // Emit event so the frontend can show the error.
                let _ = app.emit("server-error", &err_msg);
                return Err(err_msg.into());
            }
        }

        if check_api_on(port).await.is_ok() {
            eprintln!("DevRecall API ready after {}ms", (i + 1) * 500);
            return Ok(());
        }
    }

    Err("DevRecall API server did not start in time".into())
}

/// Background watchdog: periodically health-check the API and re-spawn the
/// server if it's gone. Guards against any future crash (not just the stderr
/// case) so the user never has to start `devrecall serve` from a terminal.
/// `ensure_running` re-checks health before spawning, so a transient blip
/// won't double-spawn.
pub fn spawn_watchdog(app: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        loop {
            sleep(WATCHDOG_INTERVAL).await;
            if check_api().await.is_err() {
                eprintln!("watchdog: API unreachable, attempting restart");
                if let Err(e) = ensure_running(&app).await {
                    eprintln!("watchdog: restart failed: {e}");
                }
            }
        }
    });
}

/// Path to the server log file (`~/.devrecall/serve.log`).
fn log_file_path() -> Option<PathBuf> {
    dirs_home().map(|h| h.join(".devrecall/serve.log"))
}

/// Open the server log file for appending, creating it (and its parent) if
/// needed. Returns `None` if it can't be opened so the caller falls back to
/// discarding output.
fn open_log_file(path: &Path) -> Option<File> {
    if let Some(parent) = path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    OpenOptions::new().create(true).append(true).open(path).ok()
}

/// Read the last ~20 lines of the log file, for surfacing startup failures.
fn read_log_tail(path: &Path) -> Option<String> {
    let data = std::fs::read_to_string(path).ok()?;
    let tail: Vec<&str> = data.lines().rev().take(20).collect();
    Some(tail.into_iter().rev().collect::<Vec<_>>().join("\n"))
}

/// Discover the devrecall binary in known locations.
fn find_binary() -> Option<PathBuf> {
    // 0. Prefer the sidecar bundled alongside the desktop binary. Tauri places
    //    `bundle.externalBin` at Contents/MacOS/<base>; for our desktop binary
    //    at Contents/MacOS/devrecall-desktop, the CLI lives at
    //    Contents/MacOS/devrecall. Picking the sidecar first keeps the .app
    //    self-contained — a stale `/opt/homebrew/bin/devrecall` symlink (e.g.
    //    from a leftover devrecall-cli formula) can't shadow it.
    if let Ok(exe) = std::env::current_exe() {
        if let Some(p) = sidecar_next_to(&exe) {
            return Some(p);
        }
    }

    // 1. Check $PATH via `which`.
    if let Ok(output) = std::process::Command::new("which")
        .arg("devrecall")
        .output()
    {
        if output.status.success() {
            let path = String::from_utf8_lossy(&output.stdout).trim().to_string();
            if !path.is_empty() {
                return Some(PathBuf::from(path));
            }
        }
    }

    // 2. Well-known Homebrew paths.
    let candidates = [
        "/opt/homebrew/bin/devrecall",   // Apple Silicon
        "/usr/local/bin/devrecall",      // Intel Mac
    ];

    for path in &candidates {
        let p = PathBuf::from(path);
        if p.exists() {
            return Some(p);
        }
    }

    // 3. Manual install location.
    if let Some(home) = dirs_home() {
        let p = home.join(".devrecall/bin/devrecall");
        if p.exists() {
            return Some(p);
        }
    }

    // 4. Development: project bin/ directory (from make build).
    if let Ok(exe) = std::env::current_exe() {
        // Walk up from the executable to find the project root.
        let mut dir = exe.parent().map(|p| p.to_path_buf());
        for _ in 0..10 {
            if let Some(ref d) = dir {
                let candidate = d.join("bin/devrecall");
                if candidate.exists() {
                    return Some(candidate);
                }
                dir = d.parent().map(|p| p.to_path_buf());
            } else {
                break;
            }
        }
    }

    None
}

fn sidecar_next_to(exe: &Path) -> Option<PathBuf> {
    let candidate = exe.parent()?.join("devrecall");
    if candidate.exists() {
        Some(candidate)
    } else {
        None
    }
}

fn dirs_home() -> Option<PathBuf> {
    std::env::var("HOME").ok().map(PathBuf::from)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_api_port_is_3725() {
        assert_eq!(DEFAULT_API_PORT, 3725);
    }

    #[test]
    fn api_base_uses_loopback() {
        let base = api_base(DEFAULT_API_PORT);
        assert!(base.starts_with("http://127.0.0.1:"));
    }

    #[test]
    fn configured_port_returns_valid() {
        let port = configured_port();
        assert!(port > 0, "configured port should be > 0");
    }

    #[test]
    fn dirs_home_returns_some() {
        // HOME is always set in dev/CI environments.
        let home = dirs_home();
        assert!(home.is_some());
        assert!(home.unwrap().is_absolute());
    }

    #[test]
    fn find_binary_returns_path_or_none() {
        // We can't guarantee devrecall is installed, but the function must not panic.
        let result = find_binary();
        // If found, path must be absolute.
        if let Some(p) = result {
            assert!(p.is_absolute(), "binary path should be absolute: {:?}", p);
        }
    }

    #[test]
    fn sidecar_next_to_returns_path_when_present() {
        let dir = tempfile::tempdir().unwrap();
        let exe = dir.path().join("devrecall-desktop");
        std::fs::write(&exe, b"").unwrap();
        let cli = dir.path().join("devrecall");
        std::fs::write(&cli, b"").unwrap();

        let found = sidecar_next_to(&exe);
        assert_eq!(found, Some(cli));
    }

    #[test]
    fn sidecar_next_to_returns_none_when_missing() {
        let dir = tempfile::tempdir().unwrap();
        let exe = dir.path().join("devrecall-desktop");
        std::fs::write(&exe, b"").unwrap();

        assert!(sidecar_next_to(&exe).is_none());
    }

    #[test]
    fn health_retries_is_reasonable() {
        assert!(HEALTH_RETRIES >= 5, "need enough retries for server startup");
        assert!(HEALTH_RETRIES <= 30, "too many retries would cause long waits");
    }

    #[test]
    fn health_interval_is_reasonable() {
        assert!(
            HEALTH_INTERVAL.as_millis() >= 200 && HEALTH_INTERVAL.as_millis() <= 2000,
            "health interval should be 200ms-2s, got {}ms",
            HEALTH_INTERVAL.as_millis()
        );
    }

    #[test]
    fn log_file_path_under_devrecall() {
        if let Some(p) = log_file_path() {
            assert!(p.ends_with(".devrecall/serve.log"), "unexpected path: {:?}", p);
            assert!(p.is_absolute());
        }
    }

    #[test]
    fn open_log_file_appends_not_truncates() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("nested/serve.log");

        // First open creates parent + file and appends.
        {
            use std::io::Write;
            let mut f = open_log_file(&path).expect("open 1");
            writeln!(f, "first line").unwrap();
        }
        // Second open must append, not truncate.
        {
            use std::io::Write;
            let mut f = open_log_file(&path).expect("open 2");
            writeln!(f, "second line").unwrap();
        }

        let contents = std::fs::read_to_string(&path).unwrap();
        assert!(contents.contains("first line"), "append lost earlier content");
        assert!(contents.contains("second line"));
    }

    #[test]
    fn read_log_tail_returns_last_lines() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("serve.log");
        let body: String = (0..50).map(|i| format!("line {i}\n")).collect();
        std::fs::write(&path, body).unwrap();

        let tail = read_log_tail(&path).expect("tail");
        assert!(tail.contains("line 49"), "should keep the most recent line");
        assert!(!tail.contains("line 0\n"), "should drop the oldest lines");
        assert!(tail.lines().count() <= 20);
    }

    #[test]
    fn read_log_tail_missing_file_is_none() {
        let dir = tempfile::tempdir().unwrap();
        assert!(read_log_tail(&dir.path().join("nope.log")).is_none());
    }

    #[test]
    fn watchdog_interval_is_reasonable() {
        assert!(
            WATCHDOG_INTERVAL.as_secs() >= 5 && WATCHDOG_INTERVAL.as_secs() <= 60,
            "watchdog interval should be 5-60s, got {}s",
            WATCHDOG_INTERVAL.as_secs()
        );
    }
}
