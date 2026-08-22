//! The Tauri shell around the Promview console.
//!
//! The React application is unchanged: it is the same build the browser
//! serves, pointed at a configured server through the `setApiBaseUrl` seam
//! rather than at its own origin. Everything this crate adds is what a webview
//! cannot do for itself — a tray that survives every window being closed, and
//! transport owned outside the webview so credentials can stay out of it.

use std::time::Duration;

use tauri::menu::{Menu, MenuItem};
use tauri::tray::TrayIconBuilder;
use tauri::{Manager, WebviewWindow};

use crate::api::Client;
use crate::config::{api_base, Config};
use crate::proxy::ApiProxy;
use crate::stream::StreamHandle;

pub mod api;
pub mod config;
pub mod proxy;
pub mod sse;
pub mod stream;

/// Injected before the app boots so the console resolves its API paths against
/// the configured server. It runs ahead of any application script, which is why
/// the first request already goes to the right place.
fn base_url_script(base: &str) -> String {
    let encoded = serde_json::to_string(base).unwrap_or_else(|_| "\"\"".to_string());
    format!("globalThis.__PROMVIEW_API_BASE__ = {encoded};")
}

fn toggle_window(window: &WebviewWindow) -> tauri::Result<()> {
    if window.is_visible()? {
        window.hide()
    } else {
        window.show()?;
        window.set_focus()
    }
}

pub fn run() {
    let config = match Config::from_env() {
        Ok(config) => config,
        Err(message) => {
            // Refusing to start beats starting pointed at nothing: every window
            // would show a connection error with no way to say where to look.
            eprintln!("promview-desktop: {message}");
            std::process::exit(2);
        }
    };

    let base = api_base(&config.server_url);
    eprintln!("promview-desktop: using server {base}");

    let proxy = match ApiProxy::new(config.server_url.clone(), Duration::from_secs(30)) {
        Ok(proxy) => proxy,
        Err(message) => {
            eprintln!("promview-desktop: {message}");
            std::process::exit(2);
        }
    };

    tauri::Builder::default()
        .manage(proxy)
        .manage(StreamHandle::default())
        .invoke_handler(tauri::generate_handler![
            crate::proxy::api_request,
            crate::stream::stream_start,
            crate::stream::stream_stop,
        ])
        .setup(move |app| {
            let quit = MenuItem::with_id(app, "quit", "Quit Promview", true, None::<&str>)?;
            let console = MenuItem::with_id(app, "console", "Open console", true, None::<&str>)?;
            let compact =
                MenuItem::with_id(app, "compact", "Toggle compact window", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&console, &compact, &quit])?;

            let tray = TrayIconBuilder::with_id("promview")
                .icon(
                    app.default_window_icon()
                        .cloned()
                        .ok_or("no bundled window icon")?,
                )
                .tooltip("Promview — connecting…")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "quit" => app.exit(0),
                    "console" => {
                        if let Some(window) = app.get_webview_window("console") {
                            let _ = window.show();
                            let _ = window.set_focus();
                        }
                    }
                    "compact" => {
                        if let Some(window) = app.get_webview_window("compact") {
                            let _ = toggle_window(&window);
                        }
                    }
                    _ => {}
                })
                .build(app)?;

            // The tray is the always-present surface, so it is what keeps
            // asking. A failed poll leaves the last known counts on screen and
            // says so in the tooltip rather than blanking to zero, which would
            // read as "nothing is firing".
            let client = Client::new(config.server_url.clone(), Duration::from_secs(10))
                .map_err(std::io::Error::other)?;
            let interval = Duration::from_secs(config.poll_interval_secs);
            tauri::async_runtime::spawn(async move {
                loop {
                    let tooltip = match client.firing_counts().await {
                        Ok(counts) => format!("Promview — {}", counts.tray_label()),
                        Err(message) => {
                            eprintln!("promview-desktop: {message}");
                            "Promview — cannot reach the server".to_string()
                        }
                    };
                    let _ = tray.set_tooltip(Some(&tooltip));
                    tokio::time::sleep(interval).await;
                }
            });

            Ok(())
        })
        .on_window_event(|window, event| {
            // Closing a window hides it instead: the tray is the application's
            // lifetime, and quitting is an explicit choice from its menu.
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .append_invoke_initialization_script(base_url_script(&base))
        .run(tauri::generate_context!())
        .expect("error while running the Promview desktop shell");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn base_url_script_escapes_its_value() {
        assert_eq!(
            base_url_script("https://ops.example/promview"),
            "globalThis.__PROMVIEW_API_BASE__ = \"https://ops.example/promview\";"
        );
        // A URL is not a trusted string just because it parsed; it reaches the
        // webview as source, so it is encoded rather than interpolated raw.
        assert!(base_url_script("https://x/\";alert(1);//").contains("\\\""));
    }
}
