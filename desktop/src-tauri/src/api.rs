use std::collections::BTreeMap;
use std::time::Duration;

use serde::Deserialize;
use url::Url;

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

#[derive(Debug, Clone)]
pub struct Client {
    http: reqwest::Client,
    base: Url,
}

impl Client {
    pub fn new(base: Url, timeout: Duration) -> Result<Self, String> {
        let http = reqwest::Client::builder()
            .timeout(timeout)
            .build()
            .map_err(|err| format!("build http client: {err}"))?;
        Ok(Self { http, base })
    }

    /// Firing counts. Asks for a single alert because only the aggregate is
    /// wanted: the page itself is the server's, and the tray needs none of it.
    pub async fn firing_counts(&self) -> Result<AlertCounts, String> {
        let url = self
            .base
            .join("api/v1/alerts?limit=1&status=firing")
            .map_err(|err| format!("build alerts url: {err}"))?;
        let response = self
            .http
            .get(url.clone())
            .header("Accept", "application/json")
            .send()
            .await
            .map_err(|err| format!("query {url}: {err}"))?;
        if !response.status().is_success() {
            return Err(format!("{url} returned HTTP {}", response.status()));
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
