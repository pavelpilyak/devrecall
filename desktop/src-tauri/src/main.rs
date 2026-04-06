// Prevents additional console window on Windows in release.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod hotkey;
mod server;
mod tray;

use tauri::Manager;

/// Status response from the DevRecall API.
#[derive(serde::Deserialize, serde::Serialize, Clone)]
pub struct ApiStatus {
    pub status: String,
}

/// Tauri command: check if the DevRecall API server is reachable.
#[tauri::command]
async fn check_api() -> Result<ApiStatus, String> {
    server::check_api().await.map_err(|e| e.to_string())
}

/// Tauri command: get the API base URL.
#[tauri::command]
fn api_url() -> String {
    format!("http://127.0.0.1:{}", server::API_PORT)
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_global_shortcut::Builder::new().build())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![check_api, api_url, hotkey::get_hotkey, hotkey::set_hotkey])
        .setup(|app| {
            // Build tray menu.
            tray::setup(app)?;

            // Register global hotkey (Cmd+Shift+D).
            if let Err(e) = hotkey::register(app.handle()) {
                eprintln!("Failed to register global hotkey: {e}");
            }

            // Spawn or attach to the DevRecall API server.
            let app_handle = app.handle().clone();
            tauri::async_runtime::spawn(async move {
                if let Err(e) = server::ensure_running(&app_handle).await {
                    eprintln!("Failed to start DevRecall API server: {e}");
                }
            });

            // Hide the main window on startup — the tray icon is the entry point.
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.hide();
            }

            Ok(())
        })
        .on_window_event(|window, event| {
            // Hide instead of close — the app lives in the tray.
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                let _ = window.hide();
                api.prevent_close();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running DevRecall");
}
