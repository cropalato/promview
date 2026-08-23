//! The Tauri shell around the Promview console.
//!
//! The React application is unchanged: it is the same build the browser
//! serves, pointed at a configured server through the `setApiBaseUrl` seam
//! rather than at its own origin. Everything this crate adds is what a webview
//! cannot do for itself — a tray that survives every window being closed, and
//! transport owned outside the webview so credentials can stay out of it.

use std::sync::Arc;
use std::time::Duration;

use tauri::menu::{Menu, MenuItem};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Manager, WebviewWindow};

use crate::api::Client;
use crate::config::{api_base, Config};
use crate::credentials::{Credentials, Durability};
use crate::proxy::ApiProxy;
use crate::stream::{StreamActivity, StreamHandle};

pub mod api;
pub mod config;
pub mod credentials;
pub mod proxy;
pub mod signin;
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

    let credentials = Arc::new(Credentials::new(&base));
    let proxy = match ApiProxy::new(
        config.server_url.clone(),
        Duration::from_secs(30),
        Arc::clone(&credentials),
    ) {
        Ok(proxy) => proxy,
        Err(message) => {
            eprintln!("promview-desktop: {message}");
            std::process::exit(2);
        }
    };

    tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .manage(proxy)
        .manage(StreamHandle::default())
        .manage(Arc::new(StreamActivity::default()))
        .manage(SignInState {
            credentials: Arc::clone(&credentials),
            server: config.server_url.clone(),
        })
        .invoke_handler(tauri::generate_handler![
            crate::proxy::api_request,
            crate::stream::stream_start,
            crate::stream::stream_stop,
            sign_in,
            sign_out,
            auth_status,
            show_notification,
        ])
        .setup(move |app| {
            let quit = MenuItem::with_id(app, "quit", "Quit Promview", true, None::<&str>)?;
            let sign_in_item = MenuItem::with_id(app, "sign-in", "Sign in…", true, None::<&str>)?;
            let sign_out_item = MenuItem::with_id(app, "sign-out", "Sign out", true, None::<&str>)?;
            let console = MenuItem::with_id(app, "console", "Open console", true, None::<&str>)?;
            let compact =
                MenuItem::with_id(app, "compact", "Toggle compact window", true, None::<&str>)?;
            let menu = Menu::with_items(
                app,
                &[&console, &compact, &sign_in_item, &sign_out_item, &quit],
            )?;

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
                    "sign-in" => {
                        let app = app.clone();
                        tauri::async_runtime::spawn(async move {
                            if let Err(message) =
                                run_sign_in(app.state::<SignInState>().inner()).await
                            {
                                eprintln!("promview-desktop: sign-in failed: {message}");
                            }
                        });
                    }
                    "sign-out" => {
                        app.state::<SignInState>().credentials.clear();
                        eprintln!("promview-desktop: signed out");
                    }
                    _ => {}
                })
                .build(app)?;

            // The tray re-reads whenever the stream says something changed, and
            // on a timer as a fallback for when the stream is down or has not
            // been opened yet. A failed read leaves the last known counts on
            // screen and says so in the tooltip rather than blanking to zero,
            // which would read as "nothing is firing".
            let client = Client::new(config.server_url.clone(), Duration::from_secs(10))
                .map_err(std::io::Error::other)?;
            let fallback = Duration::from_secs(config.poll_interval_secs);
            let activity: Arc<StreamActivity> = Arc::clone(&app.state::<Arc<StreamActivity>>());
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

                    tokio::select! {
                        _ = activity.wait() => {
                            // A burst of events is one change as far as a
                            // tooltip is concerned; settle before re-reading so
                            // an alert storm costs one request, not hundreds.
                            tokio::time::sleep(Duration::from_millis(500)).await;
                        }
                        _ = tokio::time::sleep(fallback) => {}
                    }
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

/// What the sign-in commands need: where the server is, and where to put the
/// session once there is one.
pub struct SignInState {
    credentials: Arc<Credentials>,
    server: url::Url,
}

/// Runs the whole flow: bind a loopback listener, send the operator to the
/// system browser, wait for the one-time code, exchange it, store the session.
///
/// The listener is bound before the browser opens, so the redirect can never
/// arrive at a port nothing is listening on.
async fn run_sign_in(state: &SignInState) -> Result<Durability, String> {
    let callback = crate::signin::Callback::bind()?;
    let url = crate::signin::authorization_url(&state.server, &callback.redirect_uri())?;
    crate::signin::open_in_browser(url.as_str())?;

    // The listener blocks, so it waits on a thread rather than holding the
    // async runtime for as long as someone takes to type a password.
    let code = tauri::async_runtime::spawn_blocking(move || callback.wait_for_code())
        .await
        .map_err(|err| format!("sign-in listener stopped: {err}"))??;

    let http = reqwest::Client::builder()
        .timeout(Duration::from_secs(30))
        .build()
        .map_err(|err| format!("build sign-in client: {err}"))?;
    let token = crate::signin::exchange_code(&http, &state.server, &code).await?;
    Ok(state.credentials.store(&token))
}

#[tauri::command]
async fn sign_in(state: tauri::State<'_, SignInState>) -> Result<String, String> {
    match run_sign_in(state.inner()).await? {
        Durability::Keychain => Ok("keychain".to_string()),
        // Signed in either way; the difference is only whether they will still
        // be tomorrow, and saying so beats a silent surprise at next launch.
        Durability::Memory => Ok("memory".to_string()),
    }
}

#[tauri::command]
fn sign_out(state: tauri::State<'_, SignInState>) {
    state.credentials.clear();
}

#[tauri::command]
fn auth_status(state: tauri::State<'_, SignInState>) -> bool {
    state.credentials.token().is_some()
}

/// Shows an operating-system notification.
///
/// The console decides *whether* to notify — it owns the opt-in, the selector,
/// and the ledger that stops a replayed event notifying twice. Duplicating any
/// of that here would give the two halves separate opinions about what deserves
/// a page. This only puts one on screen, which is the part a webview cannot do:
/// WebKitGTK has no usable Notification API, so without this there are no
/// notifications at all.
#[tauri::command]
fn show_notification(app: AppHandle, title: String, body: String) -> Result<(), String> {
    use tauri_plugin_notification::NotificationExt;
    app.notification()
        .builder()
        .title(title)
        .body(body)
        .show()
        .map_err(|err| format!("show notification: {err}"))
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
