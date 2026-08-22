//! The console's API requests, made by the Rust core instead of the webview.
//!
//! Two reasons, and the second is the one that matters. The webview is a local
//! page talking to a remote server, so its own requests are cross-origin and a
//! server that serves its console same-origin has no reason to send CORS
//! headers. And credentials belong out of the webview: once sessions arrive,
//! the cookie jar lives here, where page script cannot read it.
//!
//! The webview names a **path**, never a host. The base URL is the one this
//! process was configured with, so a compromised page cannot point the client
//! at a server of its choosing and hand it whatever credentials the jar holds.

use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::State;
use url::Url;

const MAX_BODY_BYTES: usize = 8 << 20;

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ApiRequest {
    pub method: String,
    /// Absolute path on the configured server, e.g. `/api/v1/alerts?limit=50`.
    pub path: String,
    #[serde(default)]
    pub body: Option<String>,
    #[serde(default)]
    pub headers: Vec<(String, String)>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ApiResponse {
    pub status: u16,
    pub body: String,
    pub headers: Vec<(String, String)>,
}

pub struct ApiProxy {
    http: reqwest::Client,
    base: Url,
}

impl ApiProxy {
    pub fn new(base: Url, timeout: Duration) -> Result<Self, String> {
        let http = reqwest::Client::builder()
            .timeout(timeout)
            // The jar is the point: a session cookie set by the server is held
            // here rather than in the webview, where any script could read it.
            .cookie_store(true)
            .build()
            .map_err(|err| format!("build api client: {err}"))?;
        Ok(Self { http, base })
    }

    /// The same path rule every request obeys, exposed so the stream resolves
    /// its URL identically rather than growing a second opinion.
    pub fn resolve_path(&self, path: &str) -> Result<Url, String> {
        resolve(&self.base, path)
    }

    /// A clone of the client for a long-lived read. It shares the cookie jar,
    /// which is the point: the stream is authenticated the same way everything
    /// else is.
    pub fn stream_client(&self) -> reqwest::Client {
        self.http.clone()
    }

    pub async fn send(&self, request: ApiRequest) -> Result<ApiResponse, String> {
        let url = resolve(&self.base, &request.path)?;
        let method = parse_method(&request.method)?;

        let mut builder = self.http.request(method, url.clone());
        for (name, value) in &request.headers {
            if is_forwardable_header(name) {
                builder = builder.header(name, value);
            }
        }
        if let Some(body) = request.body {
            builder = builder.body(body);
        }

        let response = builder
            .send()
            .await
            .map_err(|err| format!("request {url}: {err}"))?;
        let status = response.status().as_u16();
        let headers = response
            .headers()
            .iter()
            .filter_map(|(name, value)| {
                value
                    .to_str()
                    .ok()
                    .map(|value| (name.as_str().to_string(), value.to_string()))
            })
            .collect();
        let bytes = response
            .bytes()
            .await
            .map_err(|err| format!("read body from {url}: {err}"))?;
        if bytes.len() > MAX_BODY_BYTES {
            // A response this large is a server fault or an attack; either way
            // buffering it into the webview is not the answer.
            return Err(format!("{url} returned more than {MAX_BODY_BYTES} bytes"));
        }
        let body = String::from_utf8_lossy(&bytes).into_owned();

        Ok(ApiResponse {
            status,
            body,
            headers,
        })
    }
}

/// Joins a caller-supplied path onto the configured base.
///
/// The path must be absolute and may not be a URL of its own: the webview
/// chooses *what* to ask for, never *who* to ask. Anything else is refused
/// rather than normalised, because a rule that quietly rewrites its input is
/// one nobody can reason about.
fn resolve(base: &Url, path: &str) -> Result<Url, String> {
    if !path.starts_with('/') || path.starts_with("//") {
        return Err(format!(
            "api path must start with a single '/', got {path:?}"
        ));
    }
    let joined = format!("{}{}", base.as_str().trim_end_matches('/'), path);
    let url = Url::parse(&joined).map_err(|err| format!("build url for {path:?}: {err}"))?;
    // Belt and braces: joining cannot change the host, but this is the property
    // the whole design rests on, so it is asserted rather than assumed.
    if url.host_str() != base.host_str() || url.scheme() != base.scheme() {
        return Err(format!(
            "api path {path:?} would leave the configured server"
        ));
    }
    Ok(url)
}

fn parse_method(raw: &str) -> Result<reqwest::Method, String> {
    match raw.to_ascii_uppercase().as_str() {
        "GET" => Ok(reqwest::Method::GET),
        "POST" => Ok(reqwest::Method::POST),
        "PUT" => Ok(reqwest::Method::PUT),
        "PATCH" => Ok(reqwest::Method::PATCH),
        "DELETE" => Ok(reqwest::Method::DELETE),
        other => Err(format!("unsupported method {other}")),
    }
}

/// Headers the page may set. Anything naming the caller — authorization,
/// cookies, the host — is the core's business, and letting the page set them
/// would hand back the control this module exists to take.
fn is_forwardable_header(name: &str) -> bool {
    matches!(
        name.to_ascii_lowercase().as_str(),
        "accept" | "content-type" | "last-event-id" | "if-none-match"
    )
}

#[tauri::command]
pub async fn api_request(
    proxy: State<'_, ApiProxy>,
    request: ApiRequest,
) -> Result<ApiResponse, String> {
    proxy.send(request).await
}

#[cfg(test)]
mod tests {
    use super::*;

    fn base() -> Url {
        Url::parse("https://promview.example/promview/").unwrap()
    }

    #[test]
    fn resolves_a_path_against_the_configured_server() {
        assert_eq!(
            resolve(&base(), "/api/v1/alerts?limit=50")
                .unwrap()
                .as_str(),
            "https://promview.example/promview/api/v1/alerts?limit=50"
        );
    }

    #[test]
    fn refuses_a_path_that_names_its_own_destination() {
        // The webview chooses what to ask for, never who to ask. Each of these
        // would send the cookie jar somewhere the operator never configured.
        for path in [
            "https://evil.example/steal",
            "//evil.example/steal",
            "api/v1/alerts",
            "",
        ] {
            assert!(resolve(&base(), path).is_err(), "expected {path:?} refused");
        }
    }

    #[test]
    fn forwards_only_headers_that_are_the_pages_business() {
        assert!(is_forwardable_header("Accept"));
        assert!(is_forwardable_header("content-type"));
        assert!(is_forwardable_header("Last-Event-ID"));
        // Anything identifying the caller stays with the core.
        assert!(!is_forwardable_header("Authorization"));
        assert!(!is_forwardable_header("Cookie"));
        assert!(!is_forwardable_header("Host"));
    }

    #[test]
    fn accepts_the_methods_the_console_uses_and_no_others() {
        for method in ["get", "POST", "put"] {
            assert!(parse_method(method).is_ok(), "{method} should be allowed");
        }
        assert!(parse_method("TRACE").is_err());
        assert!(parse_method("CONNECT").is_err());
    }
}
