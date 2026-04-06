//! Global hotkey registration (Cmd+Shift+D on macOS).

use tauri::{AppHandle, Manager};
use tauri_plugin_global_shortcut::{GlobalShortcutExt, Shortcut};

/// Default hotkey: Cmd+Shift+D (macOS) / Ctrl+Shift+D (other).
const HOTKEY: &str = "CmdOrCtrl+Shift+D";

/// Register the global hotkey that toggles the main window.
pub fn register(app: &AppHandle) -> Result<(), Box<dyn std::error::Error>> {
    let shortcut: Shortcut = HOTKEY.parse()?;
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
