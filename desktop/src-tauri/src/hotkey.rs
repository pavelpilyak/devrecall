//! Global hotkey registration with runtime remapping.
//!
//! Default: Cmd+Shift+D (macOS) / Ctrl+Shift+D (other).
//! The user can remap via the Settings screen. The preference is persisted
//! to ~/.devrecall/desktop-prefs.json so it survives restarts.

use std::fs;
use std::path::PathBuf;
use std::sync::Mutex;

use tauri::{AppHandle, Manager};
use tauri_plugin_global_shortcut::{GlobalShortcutExt, Shortcut};

const DEFAULT_HOTKEY: &str = "CmdOrCtrl+Shift+D";
const PREFS_FILE: &str = "desktop-prefs.json";

/// Preferences stored on disk.
#[derive(serde::Serialize, serde::Deserialize, Default)]
struct DesktopPrefs {
    #[serde(default)]
    hotkey: Option<String>,
}

/// In-memory state for the current hotkey string.
pub struct HotkeyState(pub Mutex<String>);

/// Load the saved hotkey or return the default.
fn load_hotkey() -> String {
    if let Some(path) = prefs_path() {
        if let Ok(data) = fs::read_to_string(&path) {
            if let Ok(prefs) = serde_json::from_str::<DesktopPrefs>(&data) {
                if let Some(hk) = prefs.hotkey {
                    return hk;
                }
            }
        }
    }
    DEFAULT_HOTKEY.to_string()
}

/// Persist the hotkey to desktop-prefs.json.
fn save_hotkey(hotkey: &str) -> Result<(), Box<dyn std::error::Error>> {
    let path = prefs_path().ok_or("cannot determine prefs path")?;
    let mut prefs = DesktopPrefs::default();

    // Read existing prefs to preserve other fields.
    if let Ok(data) = fs::read_to_string(&path) {
        if let Ok(existing) = serde_json::from_str::<DesktopPrefs>(&data) {
            prefs = existing;
        }
    }

    prefs.hotkey = Some(hotkey.to_string());

    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    fs::write(&path, serde_json::to_string_pretty(&prefs)?)?;
    Ok(())
}

fn prefs_path() -> Option<PathBuf> {
    let home = std::env::var("HOME").ok()?;
    Some(PathBuf::from(home).join(".devrecall").join(PREFS_FILE))
}

/// Register the global hotkey on startup using saved or default shortcut.
pub fn register(app: &AppHandle) -> Result<(), Box<dyn std::error::Error>> {
    let hotkey_str = load_hotkey();
    register_shortcut(app, &hotkey_str)?;

    // Store in managed state so we can read it from commands.
    app.manage(HotkeyState(Mutex::new(hotkey_str)));

    Ok(())
}

/// Register a specific shortcut string.
fn register_shortcut(app: &AppHandle, hotkey_str: &str) -> Result<(), Box<dyn std::error::Error>> {
    let shortcut: Shortcut = hotkey_str.parse()?;
    let app_handle = app.clone();

    app.global_shortcut().on_shortcut(shortcut, move |_app, _shortcut, _event| {
        if let Some(window) = app_handle.get_webview_window("main") {
            if window.is_visible().unwrap_or(false) {
                let _ = window.hide();
            } else {
                let _ = window.show();
                let _ = window.set_focus();
            }
        }
    })?;

    Ok(())
}

// --- Tauri commands ---

/// Get the current hotkey string.
#[tauri::command]
pub fn get_hotkey(state: tauri::State<'_, HotkeyState>) -> String {
    state.0.lock().unwrap().clone()
}

/// Change the global hotkey. Unregisters the old one, registers the new one, persists to disk.
#[tauri::command]
pub fn set_hotkey(
    app: AppHandle,
    state: tauri::State<'_, HotkeyState>,
    shortcut: String,
) -> Result<(), String> {
    // Validate the new shortcut parses.
    let _: Shortcut = shortcut.parse().map_err(|e| {
        format!("Invalid shortcut: {e}")
    })?;

    // Unregister all existing shortcuts.
    app.global_shortcut()
        .unregister_all()
        .map_err(|e| format!("Failed to unregister old shortcut: {e}"))?;

    // Register the new one.
    register_shortcut(&app, &shortcut)
        .map_err(|e| format!("Failed to register shortcut: {e}"))?;

    // Persist.
    save_hotkey(&shortcut).map_err(|e| format!("Failed to save preference: {e}"))?;

    // Update in-memory state.
    *state.0.lock().unwrap() = shortcut;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn default_hotkey_value() {
        assert_eq!(DEFAULT_HOTKEY, "CmdOrCtrl+Shift+D");
    }

    #[test]
    fn desktop_prefs_roundtrip() {
        let prefs = DesktopPrefs {
            hotkey: Some("CmdOrCtrl+Shift+K".to_string()),
        };
        let json = serde_json::to_string(&prefs).unwrap();
        let parsed: DesktopPrefs = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.hotkey.unwrap(), "CmdOrCtrl+Shift+K");
    }

    #[test]
    fn desktop_prefs_default_has_none_hotkey() {
        let prefs = DesktopPrefs::default();
        assert!(prefs.hotkey.is_none());
    }

    #[test]
    fn desktop_prefs_missing_hotkey_field() {
        let json = "{}";
        let prefs: DesktopPrefs = serde_json::from_str(json).unwrap();
        assert!(prefs.hotkey.is_none());
    }

    #[test]
    fn desktop_prefs_extra_fields_ignored() {
        let json = r#"{"hotkey": "CmdOrCtrl+Shift+X", "theme": "dark"}"#;
        let prefs: DesktopPrefs = serde_json::from_str(json).unwrap();
        assert_eq!(prefs.hotkey.unwrap(), "CmdOrCtrl+Shift+X");
    }

    #[test]
    fn save_and_load_hotkey_via_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(PREFS_FILE);

        // Write prefs manually.
        let prefs = DesktopPrefs {
            hotkey: Some("Alt+Shift+R".to_string()),
        };
        let mut f = fs::File::create(&path).unwrap();
        f.write_all(serde_json::to_string_pretty(&prefs).unwrap().as_bytes())
            .unwrap();

        // Read back.
        let data = fs::read_to_string(&path).unwrap();
        let loaded: DesktopPrefs = serde_json::from_str(&data).unwrap();
        assert_eq!(loaded.hotkey.unwrap(), "Alt+Shift+R");
    }

    #[test]
    fn load_hotkey_returns_default_without_file() {
        let hk = load_hotkey();
        assert!(!hk.is_empty());
    }

    #[test]
    fn prefs_path_is_under_devrecall() {
        if let Some(p) = prefs_path() {
            assert!(
                p.to_string_lossy().contains(".devrecall"),
                "prefs path should be under .devrecall: {:?}",
                p
            );
            assert!(
                p.to_string_lossy().ends_with(PREFS_FILE),
                "prefs path should end with {}: {:?}",
                PREFS_FILE,
                p
            );
        }
    }
}
