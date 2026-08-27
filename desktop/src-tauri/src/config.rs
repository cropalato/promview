use std::collections::BTreeMap;
use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use serde::Deserialize;
use url::Url;

use crate::notify::NotificationRules;

/// Where this shell points, how often the tray falls back to polling, and which
/// alerts this machine wants to be interrupted by.
///
/// A desktop client has no origin to be relative to: unlike the browser
/// console, which is served by the server it talks to, this one is a local
/// webview that must be told. The environment is still the whole answer for a
/// one-off run; the optional config file is what makes an answer survive a
/// reboot without wrapping the launcher in a shell script.
#[derive(Debug)]
pub struct Config {
    pub server_url: Url,
    pub poll_interval_secs: u64,
    /// The local notification filter. Empty means no filtering.
    pub notification_rules: NotificationRules,
    /// The file the settings came from, if one was found. Kept for the boot log
    /// and for the tray's reload, which re-reads the same path.
    pub source: Option<PathBuf>,
}

/// Fallback refresh cadence for the tray count.
///
/// The tray re-reads whenever the stream reports a change, so this is only what
/// covers the gaps: before the console has opened a stream, and while one is
/// down. A minute is unhurried on purpose — the stream is what makes the tray
/// prompt, and a tight loop against a shared server costs everyone using it.
const DEFAULT_POLL_INTERVAL_SECS: u64 = 60;
const MIN_POLL_INTERVAL_SECS: u64 = 5;
const DEFAULT_SERVER_URL: &str = "http://localhost:8080";

pub const SERVER_URL_ENV: &str = "PROMVIEW_SERVER_URL";
pub const POLL_INTERVAL_ENV: &str = "PROMVIEW_POLL_INTERVAL_SECS";
/// Names the config file outright, for a run that must not use the default one.
pub const CONFIG_PATH_ENV: &str = "PROMVIEW_DESKTOP_CONFIG";

/// The config file as written on disk.
///
/// `deny_unknown_fields` is the point of having a schema at all: a settings file
/// whose typos are ignored is a file the operator believes is in effect when it
/// is not.
#[derive(Debug, Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileConfig {
    pub server_url: Option<String>,
    pub poll_interval_secs: Option<u64>,
    /// Variables to export before anything reads them. For the settings that
    /// are not this application's own — a private CA bundle, a webview
    /// workaround — and that would otherwise need a wrapper script.
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    #[serde(default)]
    pub notifications: FileNotifications,
}

#[derive(Debug, Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileNotifications {
    /// ORed. Each rule is field-name to regex, ANDed within the rule.
    #[serde(default)]
    pub rules: Vec<BTreeMap<String, String>>,
}

impl FileConfig {
    pub fn parse(text: &str) -> Result<Self, String> {
        toml::from_str(text).map_err(|err| err.to_string())
    }

    fn read(path: &Path) -> Result<Self, String> {
        let text = fs::read_to_string(path)
            .map_err(|err| format!("cannot read {}: {err}", path.display()))?;
        Self::parse(&text).map_err(|err| format!("{}: {err}", path.display()))
    }
}

/// One variable the config file asks for, and whether it is this file's to set.
#[derive(Debug, PartialEq, Eq)]
pub struct EnvAction {
    pub name: String,
    pub value: String,
    /// True when the process already has the variable, so the file yields.
    pub already_set: bool,
}

/// What the file's `[env]` table would do, given what is already exported.
///
/// The environment the operator launched with wins. A file that overwrote it
/// would make `SSL_CERT_FILE=… promview-desktop` — the obvious way to test a
/// certificate bundle once — do nothing, with no clue as to why.
pub fn env_plan<F>(entries: &BTreeMap<String, String>, is_set: F) -> Result<Vec<EnvAction>, String>
where
    F: Fn(&str) -> bool,
{
    let mut actions = Vec::with_capacity(entries.len());
    for (name, value) in entries {
        if name.is_empty() || name.contains('=') || name.contains('\0') {
            return Err(format!("[env] has an unusable variable name: {name:?}"));
        }
        if value.contains('\0') {
            return Err(format!("[env] {name} has an unusable value"));
        }
        actions.push(EnvAction {
            name: name.clone(),
            value: value.clone(),
            already_set: is_set(name),
        });
    }
    Ok(actions)
}

/// Applies the file's `[env]` table to this process and reports what it did.
///
/// Values are never logged: this table is exactly where a token or a path
/// somebody considers private would be put.
fn apply_env(entries: &BTreeMap<String, String>) -> Result<(), String> {
    for action in env_plan(entries, |name| env::var_os(name).is_some())? {
        if action.already_set {
            eprintln!(
                "promview-desktop: {} is already set in the environment; config file value ignored",
                action.name
            );
            continue;
        }
        // Sound here and only here: called from `run` before the Tauri builder
        // exists, so no other thread can be reading the environment yet — which
        // is also the only moment early enough for WebKit's own variables to
        // still be read.
        env::set_var(&action.name, &action.value);
        eprintln!("promview-desktop: {} set from config file", action.name);
    }
    Ok(())
}

/// The config file to read, if any.
///
/// An explicitly named file that does not exist is an error: the operator said
/// where their settings are, and starting with defaults instead would look like
/// the file worked. A default location that does not exist is not — most runs
/// have no file at all.
pub fn config_path() -> Result<Option<PathBuf>, String> {
    if let Some(named) = env::var_os(CONFIG_PATH_ENV)
        .map(PathBuf::from)
        .filter(|path| !path.as_os_str().is_empty())
    {
        if !named.is_file() {
            return Err(format!(
                "{CONFIG_PATH_ENV} names {}, which is not a readable file",
                named.display()
            ));
        }
        return Ok(Some(named));
    }
    Ok(default_config_paths()
        .into_iter()
        .find(|path| path.is_file()))
}

/// Where the file is looked for, in order.
///
/// The platform's own config directory first — `$XDG_CONFIG_HOME` or
/// `~/.config` on Linux, `%APPDATA%` on Windows, Application Support on macOS —
/// then the dotfile in `$HOME`, because that is where a hand-written one tends
/// to land. The extensionless names are accepted so a file named `config` works
/// as well as `config.toml`; the contents are TOML either way.
pub fn default_config_paths() -> Vec<PathBuf> {
    let mut paths = Vec::new();
    if let Some(dir) = dirs::config_dir() {
        let base = dir.join("promview-desktop");
        paths.push(base.join("config.toml"));
        paths.push(base.join("config"));
    }
    if let Some(home) = dirs::home_dir() {
        paths.push(home.join(".promview-desktop").join("config.toml"));
        paths.push(home.join(".promview-desktop").join("config"));
        paths.push(home.join(".promview-desktop.toml"));
    }
    paths
}

impl Config {
    /// Reads the config file, exports its `[env]` table, and resolves the
    /// settings from the environment that results.
    ///
    /// Precedence, highest first: a variable already in the environment, the
    /// same variable set by the file's `[env]` table, the file's own key, the
    /// built-in default.
    pub fn load() -> Result<Self, String> {
        let path = config_path()?;
        let file = match &path {
            Some(path) => FileConfig::read(path)?,
            None => FileConfig::default(),
        };
        apply_env(&file.env)?;
        Self::resolve(&file, path, |name| env::var(name).ok())
    }

    /// The pure half of `load`: everything after the environment is settled.
    pub fn resolve<F>(file: &FileConfig, source: Option<PathBuf>, get: F) -> Result<Self, String>
    where
        F: Fn(&str) -> Option<String>,
    {
        let server_url = match get(SERVER_URL_ENV) {
            Some(raw) => parse_server_url(&raw)?,
            None => match &file.server_url {
                Some(raw) => parse_server_url_named(raw, "server_url in the config file")?,
                None => parse_server_url(DEFAULT_SERVER_URL)?,
            },
        };
        let poll_interval_secs = match get(POLL_INTERVAL_ENV) {
            Some(raw) => parse_poll_interval(Some(&raw))?,
            None => match file.poll_interval_secs {
                Some(seconds) => check_poll_interval(seconds, "poll_interval_secs")?,
                None => DEFAULT_POLL_INTERVAL_SECS,
            },
        };
        Ok(Self {
            server_url,
            poll_interval_secs,
            notification_rules: NotificationRules::compile(&file.notifications.rules)?,
            source,
        })
    }

    /// Re-reads only the notification rules from the same file.
    ///
    /// Rules are the one setting worth changing without a restart: writing a
    /// filter is iterative, and an operator testing one against live alerts
    /// should not have to relaunch between attempts. Everything else — the
    /// server, the exported variables — is read once by something that has
    /// already started, so re-reading it would only disagree with reality.
    pub fn reload_rules(path: Option<&Path>) -> Result<NotificationRules, String> {
        let Some(path) = path else {
            return Ok(NotificationRules::default());
        };
        let file = FileConfig::read(path)?;
        NotificationRules::compile(&file.notifications.rules)
    }
}

/// Rejects a URL that would send requests somewhere other than where the
/// operator asked. A path is kept, so a server behind a reverse-proxy subpath
/// works; a query or fragment is refused, because joining a path onto it would
/// silently drop it.
pub fn parse_server_url(raw: &str) -> Result<Url, String> {
    parse_server_url_named(raw, SERVER_URL_ENV)
}

fn parse_server_url_named(raw: &str, origin: &str) -> Result<Url, String> {
    let parsed = Url::parse(raw.trim()).map_err(|err| format!("{origin} is not a URL: {err}"))?;
    if parsed.scheme() != "http" && parsed.scheme() != "https" {
        return Err(format!(
            "{origin} must be http or https, got {}",
            parsed.scheme()
        ));
    }
    if parsed.host_str().is_none() {
        return Err(format!("{origin} must name a host"));
    }
    if parsed.query().is_some() || parsed.fragment().is_some() {
        return Err(format!("{origin} must not carry a query or fragment"));
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
    check_poll_interval(seconds, POLL_INTERVAL_ENV)
}

fn check_poll_interval(seconds: u64, origin: &str) -> Result<u64, String> {
    if seconds < MIN_POLL_INTERVAL_SECS {
        // A tray badge that is a few seconds stale costs nothing; a tight loop
        // against a shared server costs everyone using it.
        return Err(format!(
            "{origin} must be at least {MIN_POLL_INTERVAL_SECS} seconds"
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

/// Whether a path looks like one of the config files, for the boot log's sake.
pub fn describe_source(path: Option<&Path>) -> String {
    match path {
        Some(path) => path.display().to_string(),
        None => "no config file".to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn no_env(_: &str) -> Option<String> {
        None
    }

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

    #[test]
    fn parses_a_whole_file() {
        let file = FileConfig::parse(
            r#"
            server_url = "https://ops.example/promview"
            poll_interval_secs = 30

            [env]
            SSL_CERT_FILE = "/etc/promview/ca.pem"
            WEBKIT_DISABLE_DMABUF_RENDERER = "1"

            [[notifications.rules]]
            severity = "^critical$"
            team = "^core$"

            [[notifications.rules]]
            alertname = "(?i)^disk"
            "#,
        )
        .unwrap();
        assert_eq!(file.poll_interval_secs, Some(30));
        assert_eq!(file.env.len(), 2);
        assert_eq!(file.notifications.rules.len(), 2);

        let config = Config::resolve(&file, None, no_env).unwrap();
        assert_eq!(api_base(&config.server_url), "https://ops.example/promview");
        assert_eq!(config.poll_interval_secs, 30);
        assert_eq!(config.notification_rules.len(), 2);
    }

    #[test]
    fn an_empty_file_is_a_valid_file() {
        let config = Config::resolve(&FileConfig::parse("").unwrap(), None, no_env).unwrap();
        assert_eq!(api_base(&config.server_url), DEFAULT_SERVER_URL);
        assert_eq!(config.poll_interval_secs, DEFAULT_POLL_INTERVAL_SECS);
        assert!(config.notification_rules.is_empty());
    }

    #[test]
    fn refuses_a_key_it_does_not_know() {
        // The whole point of a schema: a typo the file ignored is a setting the
        // operator believes is in effect.
        assert!(FileConfig::parse("sever_url = \"http://x\"").is_err());
        assert!(FileConfig::parse("[notifiations]\nrules = []").is_err());
    }

    #[test]
    fn the_environment_beats_the_file() {
        let file = FileConfig::parse(
            r#"
            server_url = "https://from-file.example"
            poll_interval_secs = 30
            "#,
        )
        .unwrap();
        let config = Config::resolve(&file, None, |name| match name {
            SERVER_URL_ENV => Some("https://from-env.example".to_string()),
            POLL_INTERVAL_ENV => Some("45".to_string()),
            _ => None,
        })
        .unwrap();
        assert_eq!(api_base(&config.server_url), "https://from-env.example");
        assert_eq!(config.poll_interval_secs, 45);
    }

    #[test]
    fn a_bad_value_in_the_file_names_the_file_not_the_variable() {
        let file = FileConfig::parse("server_url = \"ftp://ops.example\"").unwrap();
        let err = Config::resolve(&file, None, no_env).unwrap_err();
        assert!(err.contains("config file"), "{err}");
        assert!(!err.contains(SERVER_URL_ENV), "{err}");

        let file = FileConfig::parse("poll_interval_secs = 1").unwrap();
        let err = Config::resolve(&file, None, no_env).unwrap_err();
        assert!(err.contains("poll_interval_secs"), "{err}");
    }

    #[test]
    fn the_launch_environment_wins_over_the_env_table() {
        let entries: BTreeMap<String, String> = [
            (
                "SSL_CERT_FILE".to_string(),
                "/etc/promview/ca.pem".to_string(),
            ),
            (
                "WEBKIT_DISABLE_DMABUF_RENDERER".to_string(),
                "1".to_string(),
            ),
        ]
        .into_iter()
        .collect();
        let plan = env_plan(&entries, |name| name == "SSL_CERT_FILE").unwrap();
        assert_eq!(
            plan,
            vec![
                EnvAction {
                    name: "SSL_CERT_FILE".to_string(),
                    value: "/etc/promview/ca.pem".to_string(),
                    already_set: true,
                },
                EnvAction {
                    name: "WEBKIT_DISABLE_DMABUF_RENDERER".to_string(),
                    value: "1".to_string(),
                    already_set: false,
                },
            ]
        );
    }

    #[test]
    fn refuses_an_unusable_variable_name() {
        let entries: BTreeMap<String, String> =
            [("A=B".to_string(), "1".to_string())].into_iter().collect();
        assert!(env_plan(&entries, |_| false).is_err());
    }

    #[test]
    fn default_paths_cover_both_locations_asked_for() {
        let paths = default_config_paths();
        if paths.is_empty() {
            // No home directory to look in; there is nothing to assert about.
            return;
        }
        assert!(
            paths
                .iter()
                .any(|path| path.ends_with("promview-desktop/config.toml")),
            "{paths:?}"
        );
        assert!(
            paths
                .iter()
                .any(|path| path.ends_with(".promview-desktop.toml")),
            "{paths:?}"
        );
    }
}
