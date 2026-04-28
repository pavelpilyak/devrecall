// Prevents additional console window on Windows in release.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

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
    format!("http://127.0.0.1:{}", server::configured_port())
}

/// Tauri command: open a file with the system's default app (text editor for JSON, etc).
#[tauri::command]
fn open_path(path: String) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        std::process::Command::new("open")
            .arg(&path)
            .spawn()
            .map_err(|e| e.to_string())?;
    }
    #[cfg(target_os = "linux")]
    {
        std::process::Command::new("xdg-open")
            .arg(&path)
            .spawn()
            .map_err(|e| e.to_string())?;
    }
    #[cfg(target_os = "windows")]
    {
        std::process::Command::new("cmd")
            .args(["/C", "start", "", &path])
            .spawn()
            .map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// Tauri command: reveal a file in the OS file manager (Finder on macOS).
#[tauri::command]
fn reveal_file(path: String) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        std::process::Command::new("open")
            .arg("-R")
            .arg(&path)
            .spawn()
            .map_err(|e| e.to_string())?;
    }
    #[cfg(target_os = "linux")]
    {
        // Open the parent directory.
        if let Some(parent) = std::path::Path::new(&path).parent() {
            std::process::Command::new("xdg-open")
                .arg(parent)
                .spawn()
                .map_err(|e| e.to_string())?;
        }
    }
    #[cfg(target_os = "windows")]
    {
        std::process::Command::new("explorer")
            .arg("/select,")
            .arg(&path)
            .spawn()
            .map_err(|e| e.to_string())?;
    }
    Ok(())
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            check_api,
            api_url,
            reveal_file,
            open_path,
        ])
        .setup(|app| {
            // Build tray menu.
            tray::setup(app)?;

            // Spawn or attach to the DevRecall API server.
            let app_handle = app.handle().clone();
            tauri::async_runtime::spawn(async move {
                if let Err(e) = server::ensure_running(&app_handle).await {
                    eprintln!("Failed to start DevRecall API server: {e}");
                }
            });

            // Open devtools in debug builds.
            #[cfg(debug_assertions)]
            if let Some(window) = app.get_webview_window("main") {
                window.open_devtools();
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
        .build(tauri::generate_context!())
        .expect("error while building DevRecall")
        .run(|app, event| {
            // Re-show window when user clicks the dock icon on macOS.
            if let tauri::RunEvent::Reopen { .. } = event {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.show();
                    let _ = window.set_focus();
                }
            }
        });
}
