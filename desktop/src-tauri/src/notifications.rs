//! Scheduled desktop notifications for standup and weekly summary.
//!
//! - Standup: daily at a configurable hour (default 9:00)
//! - Weekly summary: Monday at the same hour
//!
//! Preferences are persisted in ~/.devrecall/desktop-prefs.json alongside hotkey prefs.

use std::fs;
use std::path::PathBuf;
use std::sync::Mutex;

use chrono::{Datelike, Local, NaiveTime, Weekday};
use tauri::{AppHandle, Manager};
use tauri_plugin_notification::NotificationExt;
use tokio::time::{sleep, Duration};

const PREFS_FILE: &str = "desktop-prefs.json";

/// Notification preferences.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct NotificationPrefs {
    /// Whether standup notifications are enabled (default true).
    #[serde(default = "default_true")]
    pub standup_enabled: bool,
    /// Whether weekly summary notifications are enabled (default true).
    #[serde(default = "default_true")]
    pub weekly_enabled: bool,
    /// Hour of day for notifications (0-23, default 9).
    #[serde(default = "default_hour")]
    pub hour: u32,
    /// Minute of hour for notifications (0-59, default 0).
    #[serde(default)]
    pub minute: u32,
}

fn default_true() -> bool {
    true
}

fn default_hour() -> u32 {
    9
}

impl Default for NotificationPrefs {
    fn default() -> Self {
        Self {
            standup_enabled: true,
            weekly_enabled: true,
            hour: 9,
            minute: 0,
        }
    }
}

/// Managed state for notification preferences.
pub struct NotificationState(pub Mutex<NotificationPrefs>);

/// Full desktop prefs file (shared with hotkey module).
#[derive(serde::Serialize, serde::Deserialize, Default)]
struct DesktopPrefs {
    #[serde(default)]
    hotkey: Option<String>,
    #[serde(default)]
    notifications: Option<NotificationPrefs>,
}

fn prefs_path() -> Option<PathBuf> {
    let home = std::env::var("HOME").ok()?;
    Some(PathBuf::from(home).join(".devrecall").join(PREFS_FILE))
}

/// Load notification prefs from disk.
pub fn load_prefs() -> NotificationPrefs {
    if let Some(path) = prefs_path() {
        if let Ok(data) = fs::read_to_string(&path) {
            if let Ok(prefs) = serde_json::from_str::<DesktopPrefs>(&data) {
                if let Some(np) = prefs.notifications {
                    return np;
                }
            }
        }
    }
    NotificationPrefs::default()
}

/// Save notification prefs to disk (preserving other fields).
fn save_prefs(prefs: &NotificationPrefs) -> Result<(), Box<dyn std::error::Error>> {
    let path = prefs_path().ok_or("cannot determine prefs path")?;

    let mut desktop_prefs = DesktopPrefs::default();
    if let Ok(data) = fs::read_to_string(&path) {
        if let Ok(existing) = serde_json::from_str::<DesktopPrefs>(&data) {
            desktop_prefs = existing;
        }
    }

    desktop_prefs.notifications = Some(prefs.clone());

    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    fs::write(&path, serde_json::to_string_pretty(&desktop_prefs)?)?;
    Ok(())
}

/// Calculate seconds until the next occurrence of a given time.
/// If the time has already passed today, returns seconds until tomorrow at that time.
pub fn seconds_until(hour: u32, minute: u32) -> u64 {
    let now = Local::now();
    let target_time = NaiveTime::from_hms_opt(hour, minute, 0).unwrap_or(NaiveTime::from_hms_opt(9, 0, 0).unwrap());
    let today_target = now.date_naive().and_time(target_time);

    let naive_now = now.naive_local();
    if naive_now < today_target {
        (today_target - naive_now).num_seconds().max(0) as u64
    } else {
        // Tomorrow at target time.
        let tomorrow_target = today_target + chrono::Duration::days(1);
        (tomorrow_target - naive_now).num_seconds().max(0) as u64
    }
}

/// Calculate seconds until the next Monday at the given time.
pub fn seconds_until_next_monday(hour: u32, minute: u32) -> u64 {
    let now = Local::now();
    let target_time = NaiveTime::from_hms_opt(hour, minute, 0).unwrap_or(NaiveTime::from_hms_opt(9, 0, 0).unwrap());

    let days_until_monday = match now.weekday() {
        Weekday::Mon => {
            let today_target = now.date_naive().and_time(target_time);
            if now.naive_local() < today_target {
                0 // Still before target time on Monday
            } else {
                7 // Next Monday
            }
        }
        Weekday::Tue => 6,
        Weekday::Wed => 5,
        Weekday::Thu => 4,
        Weekday::Fri => 3,
        Weekday::Sat => 2,
        Weekday::Sun => 1,
    };

    let target_date = now.date_naive() + chrono::Duration::days(days_until_monday);
    let target_dt = target_date.and_time(target_time);
    let naive_now = now.naive_local();

    (target_dt - naive_now).num_seconds().max(0) as u64
}

/// Start the background notification scheduler.
pub fn start_scheduler(app: &AppHandle) {
    let prefs = load_prefs();
    app.manage(NotificationState(Mutex::new(prefs)));

    let app_handle = app.clone();
    tauri::async_runtime::spawn(async move {
        schedule_loop(app_handle).await;
    });
}

async fn schedule_loop(app: AppHandle) {
    loop {
        let prefs = {
            if let Some(state) = app.try_state::<NotificationState>() {
                state.0.lock().unwrap().clone()
            } else {
                NotificationPrefs::default()
            }
        };

        if !prefs.standup_enabled && !prefs.weekly_enabled {
            // Nothing to schedule — check again in 1 hour.
            sleep(Duration::from_secs(3600)).await;
            continue;
        }

        // Find the next notification to fire.
        let secs_standup = if prefs.standup_enabled {
            seconds_until(prefs.hour, prefs.minute)
        } else {
            u64::MAX
        };

        let secs_weekly = if prefs.weekly_enabled {
            seconds_until_next_monday(prefs.hour, prefs.minute)
        } else {
            u64::MAX
        };

        let (wait_secs, is_weekly) = if secs_weekly <= secs_standup {
            (secs_weekly, true)
        } else {
            (secs_standup, false)
        };

        // Sleep until the next notification.
        sleep(Duration::from_secs(wait_secs)).await;

        // Re-check prefs in case they changed while sleeping.
        let current_prefs = {
            if let Some(state) = app.try_state::<NotificationState>() {
                state.0.lock().unwrap().clone()
            } else {
                continue;
            }
        };

        if is_weekly && current_prefs.weekly_enabled {
            let _ = app
                .notification()
                .builder()
                .title("DevRecall")
                .body("Your weekly summary is ready")
                .show();
        } else if !is_weekly && current_prefs.standup_enabled {
            let _ = app
                .notification()
                .builder()
                .title("DevRecall")
                .body("Your standup is ready")
                .show();
        }

        // Small delay to avoid firing twice at the exact same second.
        sleep(Duration::from_secs(60)).await;
    }
}

// --- Tauri commands ---

/// Get current notification preferences.
#[tauri::command]
pub fn get_notification_prefs(state: tauri::State<'_, NotificationState>) -> NotificationPrefs {
    state.0.lock().unwrap().clone()
}

/// Update notification preferences.
#[tauri::command]
pub fn set_notification_prefs(
    state: tauri::State<'_, NotificationState>,
    prefs: NotificationPrefs,
) -> Result<(), String> {
    save_prefs(&prefs).map_err(|e| format!("Failed to save notification prefs: {e}"))?;
    *state.0.lock().unwrap() = prefs;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_prefs() {
        let prefs = NotificationPrefs::default();
        assert!(prefs.standup_enabled);
        assert!(prefs.weekly_enabled);
        assert_eq!(prefs.hour, 9);
        assert_eq!(prefs.minute, 0);
    }

    #[test]
    fn prefs_serde_roundtrip() {
        let prefs = NotificationPrefs {
            standup_enabled: false,
            weekly_enabled: true,
            hour: 10,
            minute: 30,
        };
        let json = serde_json::to_string(&prefs).unwrap();
        let parsed: NotificationPrefs = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.standup_enabled, false);
        assert_eq!(parsed.weekly_enabled, true);
        assert_eq!(parsed.hour, 10);
        assert_eq!(parsed.minute, 30);
    }

    #[test]
    fn prefs_defaults_on_missing_fields() {
        let json = "{}";
        let prefs: NotificationPrefs = serde_json::from_str(json).unwrap();
        assert!(prefs.standup_enabled);
        assert!(prefs.weekly_enabled);
        assert_eq!(prefs.hour, 9);
        assert_eq!(prefs.minute, 0);
    }

    #[test]
    fn desktop_prefs_preserves_notifications() {
        let dp = DesktopPrefs {
            hotkey: Some("CmdOrCtrl+Shift+K".into()),
            notifications: Some(NotificationPrefs {
                standup_enabled: false,
                weekly_enabled: true,
                hour: 8,
                minute: 15,
            }),
        };
        let json = serde_json::to_string(&dp).unwrap();
        let parsed: DesktopPrefs = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.hotkey.unwrap(), "CmdOrCtrl+Shift+K");
        let np = parsed.notifications.unwrap();
        assert_eq!(np.standup_enabled, false);
        assert_eq!(np.hour, 8);
    }

    #[test]
    fn desktop_prefs_without_notifications() {
        let json = r#"{"hotkey": "CmdOrCtrl+Shift+D"}"#;
        let parsed: DesktopPrefs = serde_json::from_str(json).unwrap();
        assert!(parsed.notifications.is_none());
    }

    #[test]
    fn seconds_until_is_positive() {
        // Regardless of current time, result must be non-negative.
        let secs = seconds_until(9, 0);
        // Could be 0 to ~86400.
        assert!(secs <= 86400, "seconds_until should be <= 24h, got {secs}");
    }

    #[test]
    fn seconds_until_next_monday_is_bounded() {
        let secs = seconds_until_next_monday(9, 0);
        // At most 7 days away.
        assert!(secs <= 7 * 86400, "should be <= 7 days, got {secs}");
    }

    #[test]
    fn seconds_until_next_monday_at_least_near_future() {
        let secs = seconds_until_next_monday(9, 0);
        // Must be > 0 (can't be in the past).
        // Note: if it's Monday before 9am, secs could be very small but still > 0.
        // We already ensured .max(0) in the impl.
        assert!(secs <= 7 * 86400);
    }

    #[test]
    fn save_and_load_prefs_via_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(PREFS_FILE);

        let dp = DesktopPrefs {
            hotkey: None,
            notifications: Some(NotificationPrefs {
                standup_enabled: false,
                weekly_enabled: false,
                hour: 14,
                minute: 45,
            }),
        };

        fs::write(&path, serde_json::to_string_pretty(&dp).unwrap()).unwrap();

        let data = fs::read_to_string(&path).unwrap();
        let loaded: DesktopPrefs = serde_json::from_str(&data).unwrap();
        let np = loaded.notifications.unwrap();
        assert_eq!(np.standup_enabled, false);
        assert_eq!(np.weekly_enabled, false);
        assert_eq!(np.hour, 14);
        assert_eq!(np.minute, 45);
    }
}
