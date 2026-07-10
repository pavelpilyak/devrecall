//! System tray (menu bar) icon and context menu.

use tauri::{
    image::Image,
    menu::{Menu, MenuItem},
    tray::{TrayIconBuilder, TrayIconEvent},
    Emitter, Manager,
};

/// Tauri event name emitted when the user clicks "Log Event…" in the tray.
/// The frontend listens for this and switches to the Log tab + focuses the
/// text area for rapid entry.
pub const LOG_QUICKADD_EVENT: &str = "open-log-quickadd";

/// Stable id for the menu-bar tray icon, shared by setup and `set_tray_error`.
pub const TRAY_ID: &str = "devrecall-tray";

/// The normal, adaptive tray glyph. It's a template image (alpha-only), so
/// macOS tints it to match the menu bar in light and dark appearance.
fn load_base_icon() -> Result<Image<'static>, String> {
    Image::from_bytes(include_bytes!("../icons/tray-template.png"))
        .map_err(|e| format!("Failed to load tray icon: {e}"))
}

/// Build the errored tray glyph: the same shape stamped with a red dot in the
/// bottom-right corner. Template images are forced monochrome by macOS, so the
/// caller must render this one with `set_icon_as_template(false)` for the red
/// to survive. In non-template mode macOS no longer tints the glyph, so we
/// recolor it ourselves to match the menu bar — bright on a dark bar, dark on
/// a light one (`dark` reflects the system appearance) — so it looks like the
/// normal adaptive logo rather than a washed-out gray. The red dot is the
/// alert signal and reads on either.
fn build_error_icon(dark: bool) -> Result<Image<'static>, String> {
    let base = load_base_icon()?;
    let (w, h) = (base.width(), base.height());
    let mut rgba = base.rgba().to_vec();

    // Match the menu bar's content color so the glyph reads like the template.
    let (gr, gg, gb) = if dark {
        (236u8, 238u8, 242u8) // light glyph for a dark menu bar
    } else {
        (38u8, 40u8, 44u8) // dark glyph for a light menu bar
    };
    for px in rgba.chunks_exact_mut(4) {
        if px[3] > 0 {
            px[0] = gr;
            px[1] = gg;
            px[2] = gb;
        }
    }

    // Stamp a small filled red dot in the bottom-right corner — a badge, not
    // a fill. ~0.16 of the icon so it reads as an overlay once the menu bar
    // downscales the 64px source to ~22px.
    let radius = ((w.min(h) as f32) * 0.16).round() as i32;
    let cx = w as i32 - radius - 2;
    let cy = h as i32 - radius - 2;
    for y in 0..h as i32 {
        for x in 0..w as i32 {
            let (dx, dy) = (x - cx, y - cy);
            if dx * dx + dy * dy <= radius * radius {
                let idx = ((y as u32 * w + x as u32) * 4) as usize;
                rgba[idx] = 255; // R
                rgba[idx + 1] = 69; // G
                rgba[idx + 2] = 69; // B
                rgba[idx + 3] = 255; // A
            }
        }
    }

    Ok(Image::new_owned(rgba, w, h))
}

/// Tauri command: reflect sync-error state on the menu-bar icon. When
/// `has_error` is true the tray shows the red-badged, non-template glyph;
/// otherwise it restores the adaptive template icon. The frontend calls this
/// whenever its derived error state changes.
#[tauri::command]
pub fn set_tray_error(app: tauri::AppHandle, has_error: bool, dark: bool) -> Result<(), String> {
    let tray = app
        .tray_by_id(TRAY_ID)
        .ok_or_else(|| "tray icon not found".to_string())?;

    if has_error {
        tray.set_icon(Some(build_error_icon(dark)?))
            .map_err(|e| e.to_string())?;
        let _ = tray.set_icon_as_template(false);
        let _ = tray.set_tooltip(Some("DevRecall — a source failed to sync"));
    } else {
        tray.set_icon(Some(load_base_icon()?))
            .map_err(|e| e.to_string())?;
        let _ = tray.set_icon_as_template(true);
        let _ = tray.set_tooltip(Some("DevRecall"));
    }
    Ok(())
}

/// Set up the system tray icon and context menu.
pub fn setup(app: &tauri::App) -> Result<(), Box<dyn std::error::Error>> {
    let sync_now = MenuItem::with_id(app, "sync_now", "Sync Now", true, None::<&str>)?;
    let log_event = MenuItem::with_id(app, "log_event", "Log Event…", true, None::<&str>)?;
    let show = MenuItem::with_id(app, "show", "Open DevRecall", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;

    let menu = Menu::with_items(app, &[&show, &log_event, &sync_now, &quit])?;

    let icon = load_base_icon()?;

    let tray = TrayIconBuilder::with_id(TRAY_ID)
        .icon(icon)
        .icon_as_template(true)
        .tooltip("DevRecall")
        .menu(&menu)
        .on_menu_event(move |app, event| match event.id.as_ref() {
            "show" => {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.show();
                    let _ = window.set_focus();
                }
            }
            "log_event" => {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.show();
                    let _ = window.set_focus();
                }
                let _ = app.emit(LOG_QUICKADD_EVENT, ());
            }
            "sync_now" => {
                let app = app.clone();
                tauri::async_runtime::spawn(async move {
                    let url = format!("http://127.0.0.1:{}/api/sync", crate::server::configured_port());
                    let _ = reqwest::Client::new().post(&url).send().await;
                    if let Some(tray) = app.tray_by_id(TRAY_ID) {
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn error_icon_keeps_dimensions_and_stamps_red_badge() {
        let base = load_base_icon().expect("base icon decodes");
        for dark in [true, false] {
            let err = build_error_icon(dark).expect("error icon builds");

            // Same canvas as the base glyph.
            assert_eq!(err.width(), base.width());
            assert_eq!(err.height(), base.height());
            assert_eq!(err.rgba().len(), base.rgba().len());

            // The badge stamps opaque red pixels that the base template lacks.
            let red = err
                .rgba()
                .chunks_exact(4)
                .filter(|p| p[0] == 255 && p[1] == 69 && p[2] == 69 && p[3] == 255)
                .count();
            assert!(red > 0, "expected red badge pixels, found none");
        }
    }
}
