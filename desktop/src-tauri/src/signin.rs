//! Signing in through the system browser.
//!
//! The client never renders the identity provider's login form. Putting it in
//! our own webview is the shape phishing takes, and the operator would have no
//! address bar to check. So the flow goes out to the real browser, and comes
//! back to a listener bound to loopback for exactly one request.
//!
//! What comes back is a one-time code, not a credential. It is exchanged over
//! POST for a session, which lands in the platform secret store and never
//! reaches the webview.

use std::time::{Duration, Instant};

use serde::Deserialize;
use url::Url;

/// How long to wait for the operator to finish in the browser. Long enough for
/// a password manager, a second factor, and a moment of confusion; short enough
/// that an abandoned attempt releases the port.
const SIGN_IN_TIMEOUT: Duration = Duration::from_secs(300);

#[derive(Debug, Deserialize)]
struct ExchangeResponse {
    token: String,
}

/// A loopback listener holding one port for one callback.
pub struct Callback {
    server: tiny_http::Server,
    port: u16,
}

impl Callback {
    /// Binds to an ephemeral loopback port. The operating system chooses it, so
    /// two clients on one machine cannot collide.
    pub fn bind() -> Result<Self, String> {
        let server = tiny_http::Server::http("127.0.0.1:0")
            .map_err(|err| format!("bind loopback listener: {err}"))?;
        let port = server
            .server_addr()
            .to_ip()
            .ok_or("loopback listener has no port")?
            .port();
        Ok(Self { server, port })
    }

    pub fn redirect_uri(&self) -> String {
        format!("http://127.0.0.1:{}/callback", self.port)
    }

    /// Waits for the browser to arrive with a code.
    ///
    /// Answers every request, including ones without a code, so the operator
    /// sees a page rather than a connection error and knows to go back.
    pub fn wait_for_code(self) -> Result<String, String> {
        let deadline = Instant::now() + SIGN_IN_TIMEOUT;
        loop {
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err("timed out waiting for the browser to finish signing in".to_string());
            }
            let request = match self.server.recv_timeout(remaining) {
                Ok(Some(request)) => request,
                Ok(None) => continue,
                Err(err) => return Err(format!("loopback listener failed: {err}")),
            };
            match extract_code(request.url()) {
                Some(code) => {
                    respond(
                        request,
                        "Signed in. You can close this tab and return to Promview.",
                    );
                    return Ok(code);
                }
                None => {
                    // A favicon request, or the provider sending an error. Say
                    // so and keep waiting rather than failing the sign-in on a
                    // stray fetch the browser made on its own.
                    respond(request, "Waiting for a sign-in result…");
                }
            }
        }
    }
}

fn respond(request: tiny_http::Request, message: &str) {
    let body = format!(
        "<!doctype html><meta charset=\"utf-8\"><title>Promview</title>\
         <body style=\"font-family:system-ui;padding:2rem\">{message}</body>"
    );
    let header = "Content-Type: text/html; charset=utf-8".parse::<tiny_http::Header>();
    let mut response = tiny_http::Response::from_string(body);
    if let Ok(header) = header {
        response = response.with_header(header);
    }
    let _ = request.respond(response);
}

/// Pulls the `code` parameter out of a callback request line.
fn extract_code(target: &str) -> Option<String> {
    // The request line is a path, so it is resolved against a base that is
    // thrown away; only the query matters.
    let url = Url::parse("http://127.0.0.1").ok()?.join(target).ok()?;
    if url.path() != "/callback" {
        return None;
    }
    url.query_pairs()
        .find(|(name, _)| name == "code")
        .map(|(_, value)| value.into_owned())
        .filter(|code| !code.is_empty())
}

/// The URL that starts the flow in the system browser.
pub fn authorization_url(base: &Url, redirect_uri: &str) -> Result<Url, String> {
    let mut url = base
        .join("api/v1/auth/oidc/login")
        .map_err(|err| format!("build sign-in url: {err}"))?;
    url.query_pairs_mut()
        .append_pair("desktop_redirect", redirect_uri);
    Ok(url)
}

/// Redeems the one-time code for a session token.
pub async fn exchange_code(
    http: &reqwest::Client,
    base: &Url,
    code: &str,
) -> Result<String, String> {
    let url = base
        .join("api/v1/auth/desktop/exchange")
        .map_err(|err| format!("build exchange url: {err}"))?;
    let response = http
        .post(url.clone())
        .json(&serde_json::json!({ "code": code }))
        .send()
        .await
        .map_err(|err| format!("exchange code at {url}: {err}"))?;
    if !response.status().is_success() {
        return Err(format!("{url} returned HTTP {}", response.status()));
    }
    let body: ExchangeResponse = response
        .json()
        .await
        .map_err(|err| format!("decode exchange response: {err}"))?;
    if body.token.is_empty() {
        return Err("the server returned an empty session token".to_string());
    }
    Ok(body.token)
}

/// Opens a URL in the operating system's browser.
///
/// Deliberately not a webview: the provider's login form belongs somewhere the
/// operator has an address bar and their own password manager.
pub fn open_in_browser(url: &str) -> Result<(), String> {
    let opener = if cfg!(target_os = "macos") {
        "open"
    } else if cfg!(target_os = "windows") {
        "explorer"
    } else {
        "xdg-open"
    };
    // Spawned and left alone: the browser outlives the call, and its output is
    // its own business.
    std::process::Command::new(opener)
        .arg(url)
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()
        .map(|_| ())
        .map_err(|err| format!("open {url} in the browser: {err}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extracts_a_code_from_the_callback() {
        assert_eq!(
            extract_code("/callback?code=abc123"),
            Some("abc123".to_string())
        );
        assert_eq!(
            extract_code("/callback?state=x&code=abc123"),
            Some("abc123".to_string())
        );
    }

    #[test]
    fn ignores_requests_that_are_not_a_result() {
        // A browser fetches a favicon on its own; failing the sign-in over one
        // would be maddening.
        assert_eq!(extract_code("/favicon.ico"), None);
        assert_eq!(extract_code("/callback"), None);
        assert_eq!(extract_code("/callback?code="), None);
        assert_eq!(extract_code("/other?code=abc"), None);
    }

    #[test]
    fn decodes_a_percent_encoded_code() {
        assert_eq!(
            extract_code("/callback?code=a%26b"),
            Some("a&b".to_string())
        );
    }

    #[test]
    fn authorization_url_carries_the_loopback_redirect() {
        let base = Url::parse("https://promview.example/").unwrap();
        let url = authorization_url(&base, "http://127.0.0.1:53123/callback").unwrap();
        assert_eq!(url.path(), "/api/v1/auth/oidc/login");
        let redirect = url
            .query_pairs()
            .find(|(name, _)| name == "desktop_redirect")
            .map(|(_, value)| value.into_owned());
        assert_eq!(
            redirect,
            Some("http://127.0.0.1:53123/callback".to_string())
        );
    }

    #[test]
    fn authorization_url_keeps_a_reverse_proxy_subpath() {
        let base = Url::parse("https://ops.example/promview/").unwrap();
        let url = authorization_url(&base, "http://127.0.0.1:1/callback").unwrap();
        assert_eq!(url.path(), "/promview/api/v1/auth/oidc/login");
    }

    #[test]
    fn binds_a_loopback_port_the_os_chose() {
        let callback = Callback::bind().expect("bind");
        let redirect = callback.redirect_uri();
        assert!(redirect.starts_with("http://127.0.0.1:"));
        assert!(redirect.ends_with("/callback"));
        // An ephemeral port, so two clients on one machine cannot collide.
        assert_ne!(callback.port, 0);
    }
}
