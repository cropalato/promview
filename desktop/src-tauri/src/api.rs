use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use serde::Deserialize;
use url::Url;

use crate::credentials::Credentials;

/// Reading alert counts for the tray.
///
/// The Rust core owns transport rather than the webview, which is what keeps
/// credentials out of the webview later and lets the tray keep working with
/// every window closed. For the walking skeleton it polls; moving the stream
/// here is the next increment.

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct AlertCounts {
    pub total: i64,
    pub critical: i64,
    pub warning: i64,
    pub info: i64,
}

impl AlertCounts {
    /// What the tray says at a glance. Severity leads because that is the
    /// question someone glancing at a tray is asking.
    pub fn tray_label(&self) -> String {
        if self.total == 0 {
            return "No firing alerts".to_string();
        }
        let mut parts = Vec::new();
        for (count, name) in [
            (self.critical, "critical"),
            (self.warning, "warning"),
            (self.info, "info"),
        ] {
            if count > 0 {
                parts.push(format!("{count} {name}"));
            }
        }
        if parts.is_empty() {
            // Firing, but under a severity this build does not name; report the
            // total rather than claiming nothing is wrong.
            return format!("{} firing", self.total);
        }
        parts.join(" · ")
    }
}

#[derive(Debug, Deserialize)]
struct AlertsPage {
    #[serde(default)]
    total: i64,
    #[serde(default, rename = "severityCounts")]
    severity_counts: BTreeMap<String, i64>,
}

#[derive(Clone)]
pub struct Client {
    /// The proxy's client, not one of this module's own: the session a
    /// cookie-based deployment sets lands in that jar, and a second client
    /// would have a second, permanently empty one.
    http: reqwest::Client,
    base: Url,
    timeout: Duration,
    credentials: Arc<Credentials>,
}

impl Client {
    pub fn new(
        http: reqwest::Client,
        base: Url,
        timeout: Duration,
        credentials: Arc<Credentials>,
    ) -> Self {
        Self {
            http,
            base,
            timeout,
            credentials,
        }
    }

    /// Firing counts. Asks for a single alert because only the aggregate is
    /// wanted: the page itself is the server's, and the tray needs none of it.
    pub async fn firing_counts(&self) -> Result<AlertCounts, String> {
        let url = self
            .base
            .join("api/v1/alerts?limit=1&status=firing")
            .map_err(|err| format!("build alerts url: {err}"))?;
        let mut request = self
            .http
            .get(url.clone())
            .header("Accept", "application/json")
            // The shared client's timeout is the proxy's, which is generous
            // because a page may ask for a lot. A tooltip may not: it is read
            // at a glance, and a stale one beats a hung read.
            .timeout(self.timeout);
        // Read per request rather than once at construction: the tray outlives
        // signing in, and a token captured before there was one never updates.
        if let Some(token) = self.credentials.token() {
            request = request.bearer_auth(token);
        }
        let response = request
            .send()
            .await
            .map_err(|err| format!("query {url}: {err}"))?;
        let status = response.status();
        if !status.is_success() {
            // An unauthenticated server is not an unreachable one, and the
            // tooltip should not say it is: the answer is to sign in.
            if status == reqwest::StatusCode::UNAUTHORIZED {
                return Err(format!("{url} needs a session: sign in from the tray"));
            }
            return Err(format!("{url} returned HTTP {status}"));
        }
        let page: AlertsPage = response
            .json()
            .await
            .map_err(|err| format!("decode alerts from {url}: {err}"))?;
        Ok(AlertCounts {
            total: page.total,
            critical: *page.severity_counts.get("critical").unwrap_or(&0),
            warning: *page.severity_counts.get("warning").unwrap_or(&0),
            info: *page.severity_counts.get("info").unwrap_or(&0),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A server that answers one request and hands back the `Authorization` it
    /// saw. Enough to prove what left this process, which is the whole question.
    fn serve_once(
        status: u16,
        body: &'static str,
    ) -> (Url, std::sync::mpsc::Receiver<Option<String>>) {
        let server = tiny_http::Server::http("127.0.0.1:0").expect("bind test server");
        let base = Url::parse(&format!("http://{}/", server.server_addr())).expect("test url");
        let (tx, rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            if let Ok(request) = server.recv() {
                let seen = request
                    .headers()
                    .iter()
                    .find(|header| header.field.equiv("Authorization"))
                    .map(|header| header.value.as_str().to_string());
                let _ = tx.send(seen);
                let response = tiny_http::Response::from_string(body)
                    .with_status_code(tiny_http::StatusCode(status));
                let _ = request.respond(response);
            }
        });
        (base, rx)
    }

    fn client(base: Url, credentials: Arc<Credentials>) -> Client {
        Client::new(
            reqwest::Client::new(),
            base,
            Duration::from_secs(5),
            credentials,
        )
    }

    #[tokio::test]
    async fn the_tray_read_carries_the_session() {
        // The regression this guards: the tray had its own client with no
        // credentials, so it got a 401 on every poll while the console — which
        // goes through the proxy — showed the alerts perfectly well.
        let (base, seen) = serve_once(200, r#"{"total":1,"severityCounts":{"critical":1}}"#);
        let credentials = Arc::new(Credentials::new("https://tray-read.example"));
        credentials.store("session-token");

        let counts = client(base, Arc::clone(&credentials))
            .firing_counts()
            .await
            .expect("counts");
        assert_eq!(counts.critical, 1);
        assert_eq!(
            seen.recv().expect("request"),
            Some("Bearer session-token".to_string())
        );
        credentials.clear();
    }

    #[tokio::test]
    async fn a_read_before_signing_in_sends_no_bearer() {
        let (base, seen) = serve_once(200, r#"{"total":0,"severityCounts":{}}"#);
        let credentials = Arc::new(Credentials::new("https://no-session.example"));

        let _ = client(base, credentials).firing_counts().await;
        assert_eq!(seen.recv().expect("request"), None);
    }

    #[tokio::test]
    async fn an_unauthenticated_server_is_not_reported_as_unreachable() {
        let (base, _seen) = serve_once(401, "unauthorized");
        let credentials = Arc::new(Credentials::new("https://needs-session.example"));

        let message = client(base, credentials)
            .firing_counts()
            .await
            .expect_err("401 is an error");
        assert!(message.contains("sign in"), "unexpected message: {message}");
    }

    #[test]
    fn tray_label_leads_with_severity() {
        let counts = AlertCounts {
            total: 6,
            critical: 3,
            warning: 2,
            info: 1,
        };
        assert_eq!(counts.tray_label(), "3 critical · 2 warning · 1 info");
    }

    #[test]
    fn tray_label_omits_empty_severities() {
        let counts = AlertCounts {
            total: 3,
            critical: 3,
            warning: 0,
            info: 0,
        };
        assert_eq!(counts.tray_label(), "3 critical");
    }

    #[test]
    fn tray_label_says_so_when_nothing_is_firing() {
        assert_eq!(AlertCounts::default().tray_label(), "No firing alerts");
    }

    #[test]
    fn tray_label_reports_a_total_it_cannot_break_down() {
        // Firing under a severity this build does not name. Saying nothing is
        // wrong would be the one unacceptable answer.
        let counts = AlertCounts {
            total: 4,
            critical: 0,
            warning: 0,
            info: 0,
        };
        assert_eq!(counts.tray_label(), "4 firing");
    }
}
