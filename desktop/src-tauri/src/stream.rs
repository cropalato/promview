//! The alert stream, held open by the Rust core instead of the webview.
//!
//! This is the reason the plan chose a shell over a progressive web app: the
//! tray has to keep counting while every window is closed, and a stream owned
//! by a webview dies with it. Holding it here also removes the last
//! cross-origin request the page was making.
//!
//! Reconnect policy deliberately stays in the console. It already has one,
//! tested, with backoff and cursor resumption; duplicating it here would give
//! the two halves separate opinions about when to give up. This side reports
//! open, message, and error, and does as it is told.

use std::sync::Arc;
use std::sync::Mutex;

use futures_util::StreamExt;
use serde::Serialize;
use tauri::{AppHandle, Manager, State};
use tokio::sync::Notify;
use url::Url;

use crate::sse::SseParser;

/// Signalled whenever the stream says something changed.
///
/// The tray listens rather than polling, but it re-reads the counts instead of
/// applying the event as a delta — exactly what the console does. Deriving
/// totals from a delta stream means tracking every alert's severity and state,
/// and being wrong in a way nobody notices until the number is wrong.
#[derive(Default)]
pub struct StreamActivity {
    notify: Notify,
}

impl StreamActivity {
    pub fn changed(&self) {
        self.notify.notify_one();
    }

    pub async fn wait(&self) {
        self.notify.notified().await;
    }
}

/// Pushed into the webview as a call to a global the page installs. Tauri's own
/// event plugin would need the JS API package in the console's bundle, which a
/// browser build should not carry for a shell it will never run in.
const DISPATCH_GLOBAL: &str = "__PROMVIEW_STREAM__";

#[derive(Debug, Clone, Serialize)]
#[serde(tag = "kind", rename_all = "camelCase")]
pub enum StreamMessage {
    Open,
    Message {
        event: String,
        data: String,
        id: Option<String>,
    },
    Error {
        message: String,
    },
}

#[derive(Default)]
pub struct StreamHandle {
    running: Mutex<Option<tauri::async_runtime::JoinHandle<()>>>,
}

impl StreamHandle {
    /// Replaces any stream already running. The console opens exactly one at a
    /// time and reopens on reconnect, so the previous task is always finished
    /// with by the time a new one is asked for.
    fn replace(&self, next: tauri::async_runtime::JoinHandle<()>) {
        if let Ok(mut slot) = self.running.lock() {
            if let Some(previous) = slot.replace(next) {
                previous.abort();
            }
        }
    }

    fn stop(&self) {
        if let Ok(mut slot) = self.running.lock() {
            if let Some(previous) = slot.take() {
                previous.abort();
            }
        }
    }
}

fn dispatch(app: &AppHandle, message: &StreamMessage) {
    let Ok(payload) = serde_json::to_string(message) else {
        return;
    };
    // JSON-encoded, so the payload cannot escape the call it is embedded in.
    let script = format!("globalThis.{DISPATCH_GLOBAL}&&globalThis.{DISPATCH_GLOBAL}({payload});");
    for (_, window) in app.webview_windows() {
        let _ = window.eval(&script);
    }
}

async fn pump(
    app: AppHandle,
    http: reqwest::Client,
    url: Url,
    bearer: Option<String>,
    activity: Arc<StreamActivity>,
) {
    let mut request = http
        .get(url.clone())
        .header("Accept", "text/event-stream")
        // Whatever the server's own idea of a keepalive interval is, the read
        // must not time out waiting for the next event.
        .timeout(std::time::Duration::from_secs(86_400));
    if let Some(token) = bearer {
        // The stream is authenticated the same way every other request is.
        request = request.bearer_auth(token);
    }

    let response = match request.send().await {
        Ok(response) => response,
        Err(err) => {
            dispatch(
                &app,
                &StreamMessage::Error {
                    message: format!("connect {url}: {err}"),
                },
            );
            return;
        }
    };
    if !response.status().is_success() {
        dispatch(
            &app,
            &StreamMessage::Error {
                message: format!("{url} returned HTTP {}", response.status()),
            },
        );
        return;
    }

    dispatch(&app, &StreamMessage::Open);
    // A fresh connection may have missed events while it was down, so the tray
    // re-reads on open as well as on every event.
    activity.changed();

    let mut parser = SseParser::new();
    let mut body = response.bytes_stream();
    while let Some(chunk) = body.next().await {
        let chunk = match chunk {
            Ok(chunk) => chunk,
            Err(err) => {
                dispatch(
                    &app,
                    &StreamMessage::Error {
                        message: format!("read {url}: {err}"),
                    },
                );
                return;
            }
        };
        for event in parser.push(&String::from_utf8_lossy(&chunk)) {
            dispatch(
                &app,
                &StreamMessage::Message {
                    event: event.event,
                    data: event.data,
                    id: event.id,
                },
            );
            activity.changed();
        }
    }

    // The body ended without an error. That is still the stream going away, and
    // the console's reconnect is what should decide what happens next.
    dispatch(
        &app,
        &StreamMessage::Error {
            message: "stream closed by the server".to_string(),
        },
    );
}

#[tauri::command]
pub async fn stream_start(
    app: AppHandle,
    proxy: State<'_, crate::proxy::ApiProxy>,
    handle: State<'_, StreamHandle>,
    activity: State<'_, Arc<StreamActivity>>,
    path: String,
) -> Result<(), String> {
    // Resolved by the same rule every other request uses: the page says what to
    // stream, never who to stream it from.
    let url = proxy.resolve_path(&path)?;
    let http = proxy.stream_client();
    let bearer = proxy.bearer();
    let app_handle = app.clone();
    let activity = Arc::clone(&activity);
    handle.replace(tauri::async_runtime::spawn(async move {
        pump(app_handle, http, url, bearer, activity).await;
    }));
    Ok(())
}

#[tauri::command]
pub fn stream_stop(handle: State<'_, StreamHandle>) {
    handle.stop();
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn messages_serialise_with_a_discriminator_the_page_can_switch_on() {
        let open = serde_json::to_string(&StreamMessage::Open).unwrap();
        assert_eq!(open, r#"{"kind":"open"}"#);

        let message = serde_json::to_string(&StreamMessage::Message {
            event: "alert.created".to_string(),
            data: "{}".to_string(),
            id: Some("7".to_string()),
        })
        .unwrap();
        assert!(message.contains(r#""kind":"message""#));
        assert!(message.contains(r#""event":"alert.created""#));
        assert!(message.contains(r#""id":"7""#));

        let error = serde_json::to_string(&StreamMessage::Error {
            message: "boom".to_string(),
        })
        .unwrap();
        assert_eq!(error, r#"{"kind":"error","message":"boom"}"#);
    }

    #[tokio::test]
    async fn activity_wakes_a_waiting_tray() {
        let activity = StreamActivity::default();
        activity.changed();
        // Already signalled: the wait must return rather than block, or a tray
        // that was busy reading when the event arrived would miss it.
        tokio::time::timeout(std::time::Duration::from_millis(100), activity.wait())
            .await
            .expect("wait should return for a signal that already happened");
    }

    #[tokio::test]
    async fn a_burst_of_events_is_one_wakeup() {
        let activity = StreamActivity::default();
        for _ in 0..50 {
            activity.changed();
        }
        tokio::time::timeout(std::time::Duration::from_millis(100), activity.wait())
            .await
            .expect("first wait returns");
        // The tray debounces after waking, so fifty events cost one re-read
        // rather than fifty. Nothing further is pending.
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(50), activity.wait())
                .await
                .is_err(),
            "a burst should not queue a wakeup per event"
        );
    }

    #[test]
    fn a_payload_cannot_escape_the_call_it_is_embedded_in() {
        // The payload is interpolated into a script, so what matters is that it
        // stays one JSON string literal: a quote that closed early would turn
        // the rest into code. Round-tripping proves it, where substring
        // matching only looks like it does.
        let hostile = "\");evil();//";
        let payload = serde_json::to_string(&StreamMessage::Error {
            message: hostile.to_string(),
        })
        .unwrap();

        let parsed: serde_json::Value = serde_json::from_str(&payload).expect("valid JSON");
        assert_eq!(
            parsed["message"],
            serde_json::Value::String(hostile.to_string())
        );
        assert_eq!(
            parsed["kind"],
            serde_json::Value::String("error".to_string())
        );
    }
}
