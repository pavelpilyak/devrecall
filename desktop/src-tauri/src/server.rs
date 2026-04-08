//! CLI process management: discover the devrecall binary, spawn or attach to the API server.

use std::path::PathBuf;
use std::process::Stdio;
use tokio::process::Command;
use tokio::time::{sleep, Duration};

use crate::ApiStatus;

pub const API_PORT: u16 = 9147;
const API_BASE: &str = "http://127.0.0.1:9147";
const HEALTH_RETRIES: u32 = 10;
const HEALTH_INTERVAL: Duration = Duration::from_millis(500);

/// Check if the API is reachable.
pub async fn check_api() -> Result<ApiStatus, Box<dyn std::error::Error>> {
    let resp = reqwest::get(format!("{API_BASE}/api/status")).await?;
    let status: ApiStatus = resp.json().await?;
    Ok(status)
}

/// Ensure the DevRecall API server is running.
/// If already running, attach (do nothing). Otherwise, spawn `devrecall serve`.
pub async fn ensure_running(
    _app: &tauri::AppHandle,
) -> Result<(), Box<dyn std::error::Error>> {
    // Check if the server is already running.
    if check_api().await.is_ok() {
        eprintln!("DevRecall API already running on port {API_PORT}");
        return Ok(());
    }

    // Find the binary.
    let binary = find_binary().ok_or("devrecall binary not found")?;
    eprintln!("Starting DevRecall API server: {}", binary.display());

    // Spawn `devrecall serve` as a background child process.
    Command::new(&binary)
        .args(["serve"])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()?;

    // Wait for the server to become healthy.
    for i in 0..HEALTH_RETRIES {
        sleep(HEALTH_INTERVAL).await;
        if check_api().await.is_ok() {
            eprintln!("DevRecall API ready after {}ms", (i + 1) * 500);
            return Ok(());
        }
    }

    Err("DevRecall API server did not start in time".into())
}

/// Discover the devrecall binary in known locations.
fn find_binary() -> Option<PathBuf> {
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

fn dirs_home() -> Option<PathBuf> {
    std::env::var("HOME").ok().map(PathBuf::from)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn api_port_is_9147() {
        assert_eq!(API_PORT, 9147);
    }

    #[test]
    fn api_base_uses_loopback() {
        assert!(API_BASE.starts_with("http://127.0.0.1:"));
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
}
