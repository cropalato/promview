use std::env;

use url::Url;

/// Where this shell points, and how often it refreshes the tray.
///
/// A desktop client has no origin to be relative to: unlike the browser
/// console, which is served by the server it talks to, this one is a local
/// webview that must be told. For the walking skeleton that comes from the
/// environment; a settings surface and multiple server profiles are later work.
#[derive(Debug, Clone)]
pub struct Config {
    pub server_url: Url,
    pub poll_interval_secs: u64,
}

/// Refresh cadence for the tray count. The stream is not in the Rust core yet,
/// so this polls; the interval is deliberately unhurried, because a tray badge
/// that is a few seconds stale costs nothing and a tight loop against a shared
/// server costs everyone.
const DEFAULT_POLL_INTERVAL_SECS: u64 = 15;
const MIN_POLL_INTERVAL_SECS: u64 = 5;

pub const SERVER_URL_ENV: &str = "PROMVIEW_SERVER_URL";
pub const POLL_INTERVAL_ENV: &str = "PROMVIEW_POLL_INTERVAL_SECS";

impl Config {
    pub fn from_env() -> Result<Self, String> {
        let raw = env::var(SERVER_URL_ENV).unwrap_or_else(|_| "http://localhost:8080".to_string());
        Ok(Self {
            server_url: parse_server_url(&raw)?,
            poll_interval_secs: parse_poll_interval(env::var(POLL_INTERVAL_ENV).ok().as_deref())?,
        })
    }
}

/// Rejects a URL that would send requests somewhere other than where the
/// operator asked. A path is kept, so a server behind a reverse-proxy subpath
/// works; a query or fragment is refused, because joining a path onto it would
/// silently drop it.
pub fn parse_server_url(raw: &str) -> Result<Url, String> {
    let parsed =
        Url::parse(raw.trim()).map_err(|err| format!("{SERVER_URL_ENV} is not a URL: {err}"))?;
    if parsed.scheme() != "http" && parsed.scheme() != "https" {
        return Err(format!(
            "{SERVER_URL_ENV} must be http or https, got {}",
            parsed.scheme()
        ));
    }
    if parsed.host_str().is_none() {
        return Err(format!("{SERVER_URL_ENV} must name a host"));
    }
    if parsed.query().is_some() || parsed.fragment().is_some() {
        return Err(format!(
            "{SERVER_URL_ENV} must not carry a query or fragment"
        ));
    }
    Ok(parsed)
}

fn parse_poll_interval(raw: Option<&str>) -> Result<u64, String> {
    let Some(raw) = raw.map(str::trim).filter(|value| !value.is_empty()) else {
        return Ok(DEFAULT_POLL_INTERVAL_SECS);
    };
    let seconds: u64 = raw
        .parse()
        .map_err(|_| format!("{POLL_INTERVAL_ENV} must be a whole number of seconds"))?;
    if seconds < MIN_POLL_INTERVAL_SECS {
        // A tray badge that is a few seconds stale costs nothing; a tight loop
        // against a shared server costs everyone using it.
        return Err(format!(
            "{POLL_INTERVAL_ENV} must be at least {MIN_POLL_INTERVAL_SECS} seconds"
        ));
    }
    Ok(seconds)
}

/// The base the webview should resolve its API paths against.
///
/// Normalised the same way the console's own `setApiBaseUrl` does: no trailing
/// slash, so joining an absolute path is a plain concatenation.
pub fn api_base(server_url: &Url) -> String {
    server_url.as_str().trim_end_matches('/').to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_a_plain_server_and_a_subpath() {
        assert_eq!(
            api_base(&parse_server_url("http://localhost:8080").unwrap()),
            "http://localhost:8080"
        );
        assert_eq!(
            api_base(&parse_server_url("https://ops.example/").unwrap()),
            "https://ops.example"
        );
        // A reverse proxy subpath is kept; dropping it would misroute everything.
        assert_eq!(
            api_base(&parse_server_url("https://ops.example/promview/").unwrap()),
            "https://ops.example/promview"
        );
    }

    #[test]
    fn refuses_a_url_that_would_misroute_requests() {
        for raw in [
            "",
            "promview.example",
            "ftp://promview.example",
            "http://promview.example?token=x",
            "http://promview.example#x",
        ] {
            assert!(
                parse_server_url(raw).is_err(),
                "expected {raw:?} to be refused"
            );
        }
    }

    #[test]
    fn poll_interval_defaults_and_refuses_a_tight_loop() {
        assert_eq!(
            parse_poll_interval(None).unwrap(),
            DEFAULT_POLL_INTERVAL_SECS
        );
        assert_eq!(
            parse_poll_interval(Some("  ")).unwrap(),
            DEFAULT_POLL_INTERVAL_SECS
        );
        assert_eq!(parse_poll_interval(Some("30")).unwrap(), 30);
        assert!(parse_poll_interval(Some("1")).is_err());
        assert!(parse_poll_interval(Some("soon")).is_err());
    }
}
