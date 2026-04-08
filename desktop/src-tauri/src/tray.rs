//! System tray (menu bar) icon and context menu.

use tauri::{
    image::Image,
    menu::{Menu, MenuItem},
    tray::{TrayIconBuilder, TrayIconEvent},
    Manager,
};

/// Set up the system tray icon and context menu.
pub fn setup(app: &tauri::App) -> Result<(), Box<dyn std::error::Error>> {
    let sync_now = MenuItem::with_id(app, "sync_now", "Sync Now", true, None::<&str>)?;
    let show = MenuItem::with_id(app, "show", "Open DevRecall", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;

    let menu = Menu::with_items(app, &[&show, &sync_now, &quit])?;

    let icon = Image::from_bytes(include_bytes!("../icons/icon.png"))
        .map_err(|e| format!("Failed to load tray icon: {e}"))?;

    let tray = TrayIconBuilder::with_id("devrecall-tray")
        .icon(icon)
        .tooltip("DevRecall")
        .menu(&menu)
        .on_menu_event(move |app, event| match event.id.as_ref() {
            "show" => {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.show();
                    let _ = window.set_focus();
                }
            }
            "sync_now" => {
                let app = app.clone();
                tauri::async_runtime::spawn(async move {
                    let url = format!("http://127.0.0.1:{}/api/sync", crate::server::API_PORT);
                    let _ = reqwest::Client::new().post(&url).send().await;
                    if let Some(tray) = app.tray_by_id("devrecall-tray") {
                        let _ = tray.set_tooltip(Some("DevRecall — synced just now"));
                    }
                });
            }
            "quit" => {
                app.exit(0);
            }
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            // Only react to left-click, not double-click or other events.
            if matches!(event, TrayIconEvent::Click { button: tauri::tray::MouseButton::Left, button_state: tauri::tray::MouseButtonState::Up, .. }) {
                let app = tray.app_handle();
                if let Some(window) = app.get_webview_window("main") {
                    if window.is_visible().unwrap_or(false) {
                        let _ = window.hide();
                    } else {
                        let _ = window.show();
                        let _ = window.set_focus();
                    }
                }
            }
        })
        .build(app)?;

    let _ = tray.set_show_menu_on_left_click(false);

    Ok(())
}
