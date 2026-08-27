//! Showing an operating-system notification, and deciding which ones this
//! machine wants.
//!
//! The console still owns *whether* an event deserves a page: the opt-in, the
//! server-side selector, and the ledger that stops a replayed event notifying
//! twice all stay there, so every client agrees on policy. What lives here is
//! the part that is per-machine and cannot live on the server — a local filter
//! for the operator who signs in from a laptop that should only ever buzz for
//! their own team, and the actual showing, which a WebKitGTK webview cannot do
//! at all.
//!
//! notify-rust is called directly rather than through `tauri-plugin-notification`
//! because the plugin shows on a spawned task and drops the result: every
//! failure — no notification daemon, notifications switched off for the app, an
//! AppUserModelID Windows does not know — arrives as `Ok(())`. A page that
//! never appeared and reported success is the one failure mode that must not be
//! silent.

use std::collections::{BTreeMap, HashMap};

use regex::Regex;

/// The event fields a rule may match on.
///
/// Deliberately the fields the stream event carries — the console has no
/// others to send, because the server denormalizes exactly these into the
/// stream record. A rule naming anything else is a typo, and is refused rather
/// than quietly never matching.
pub const RULE_FIELDS: &[&str] = &["severity", "alertname", "source", "team", "summary"];

/// One rule: field patterns that must all match.
#[derive(Debug)]
pub struct Rule {
    matchers: Vec<(String, Regex)>,
}

impl Rule {
    fn matches(&self, labels: &HashMap<String, String>) -> bool {
        self.matchers.iter().all(|(field, pattern)| {
            // A field the event did not carry is matched as empty rather than
            // skipped: `team = "^$"` is how an operator asks for the alerts
            // that have no team, and skipping would make that rule vacuous.
            pattern.is_match(labels.get(field).map(String::as_str).unwrap_or(""))
        })
    }
}

/// The local notification filter: rules are ORed, matchers within a rule ANDed.
///
/// No rules means no filtering, not "match nothing". The filter is an optional
/// narrowing of what the console already decided to show; an operator who has
/// not written one must keep getting what they got before the file existed.
#[derive(Debug, Default)]
pub struct NotificationRules {
    rules: Vec<Rule>,
}

impl NotificationRules {
    /// Compiles the rules as written in the config file.
    ///
    /// Both a bad field name and a bad pattern are refused here, at load, where
    /// the operator is looking at the file they just edited. Deferring to the
    /// first alert would fail at the least observable moment there is.
    pub fn compile(raw: &[BTreeMap<String, String>]) -> Result<Self, String> {
        let mut rules = Vec::with_capacity(raw.len());
        for (index, entry) in raw.iter().enumerate() {
            let position = index + 1;
            if entry.is_empty() {
                // An empty rule matches every alert, which silently defeats
                // every other rule in the file.
                return Err(format!(
                    "notification rule {position} is empty; a rule needs at least one field"
                ));
            }
            let mut matchers = Vec::with_capacity(entry.len());
            for (field, pattern) in entry {
                if !RULE_FIELDS.contains(&field.as_str()) {
                    return Err(format!(
                        "notification rule {position} matches on {field:?}, which no alert event carries; use one of {}",
                        RULE_FIELDS.join(", ")
                    ));
                }
                let compiled = Regex::new(pattern)
                    .map_err(|err| format!("notification rule {position} field {field}: {err}"))?;
                matchers.push((field.clone(), compiled));
            }
            rules.push(Rule { matchers });
        }
        Ok(Self { rules })
    }

    pub fn is_empty(&self) -> bool {
        self.rules.is_empty()
    }

    pub fn len(&self) -> usize {
        self.rules.len()
    }

    /// Whether any rule accepts this event. Vacuously true when there are none.
    pub fn allows(&self, labels: &HashMap<String, String>) -> bool {
        self.rules.is_empty() || self.rules.iter().any(|rule| rule.matches(labels))
    }
}

/// Puts one notification on screen, synchronously, and says whether it worked.
///
/// Blocking: on Linux this is a D-Bus round trip. Callers run it on the
/// blocking pool rather than the async runtime.
pub fn show(identifier: &str, title: &str, body: &str) -> Result<(), String> {
    let mut notification = notify_rust::Notification::new();
    notification.summary(title).body(body).auto_icon();

    #[cfg(windows)]
    {
        // Windows attributes a toast to an AppUserModelID, which exists only
        // for an installed app — the installer registers it with the Start
        // Menu shortcut. Setting it for a binary run straight out of
        // `target/` names an ID Windows does not know and the toast is
        // dropped, so there it is left unset and the toast is attributed to
        // the host process instead of not appearing at all.
        let separator = std::path::MAIN_SEPARATOR;
        let installed = tauri::utils::platform::current_exe()
            .ok()
            .and_then(|exe| exe.parent().map(|dir| dir.display().to_string()))
            .map(|dir| {
                !(dir.ends_with(&format!("{separator}target{separator}debug"))
                    || dir.ends_with(&format!("{separator}target{separator}release")))
            })
            .unwrap_or(false);
        if installed {
            notification.app_id(identifier);
        }
    }

    #[cfg(target_os = "macos")]
    {
        // macOS will only deliver for a registered bundle identifier; in a dev
        // run there is none, so the notification borrows the terminal's.
        let _ = notify_rust::set_application(if tauri::is_dev() {
            "com.apple.Terminal"
        } else {
            identifier
        });
    }

    #[cfg(not(any(windows, target_os = "macos")))]
    let _ = identifier;

    notification
        .show()
        .map(|_| ())
        .map_err(|err| err.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rule(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        pairs
            .iter()
            .map(|(name, value)| (name.to_string(), value.to_string()))
            .collect()
    }

    fn labels(pairs: &[(&str, &str)]) -> HashMap<String, String> {
        pairs
            .iter()
            .map(|(name, value)| (name.to_string(), value.to_string()))
            .collect()
    }

    #[test]
    fn no_rules_allows_everything() {
        // The filter narrows what the console already chose to show. An
        // operator with no file must not lose notifications to it.
        let rules = NotificationRules::default();
        assert!(rules.is_empty());
        assert!(rules.allows(&labels(&[("severity", "critical")])));
        assert!(rules.allows(&HashMap::new()));
    }

    #[test]
    fn matchers_within_a_rule_are_anded() {
        let rules =
            NotificationRules::compile(&[rule(&[("severity", "^critical$"), ("team", "^core$")])])
                .unwrap();
        assert!(rules.allows(&labels(&[("severity", "critical"), ("team", "core")])));
        assert!(!rules.allows(&labels(&[("severity", "critical"), ("team", "payments")])));
        assert!(!rules.allows(&labels(&[("severity", "warning"), ("team", "core")])));
    }

    #[test]
    fn rules_are_ored() {
        let rules = NotificationRules::compile(&[
            rule(&[("team", "^core$")]),
            rule(&[("alertname", "(?i)^disk")]),
        ])
        .unwrap();
        assert_eq!(rules.len(), 2);
        assert!(rules.allows(&labels(&[("team", "core")])));
        assert!(rules.allows(&labels(&[
            ("alertname", "DiskWillFill"),
            ("team", "payments")
        ])));
        assert!(!rules.allows(&labels(&[("alertname", "HighCPU"), ("team", "payments")])));
    }

    #[test]
    fn patterns_are_unanchored_substring_matches() {
        // Same as the regex an operator is used to from Prometheus tooling
        // being anchored is opt-in with ^ and $.
        let rules = NotificationRules::compile(&[rule(&[("alertname", "CPU")])]).unwrap();
        assert!(rules.allows(&labels(&[("alertname", "HighCPUThrottling")])));
    }

    #[test]
    fn a_missing_field_matches_as_empty() {
        let rules = NotificationRules::compile(&[rule(&[("team", "^$")])]).unwrap();
        assert!(rules.allows(&labels(&[("severity", "critical")])));
        assert!(!rules.allows(&labels(&[("team", "core")])));
    }

    #[test]
    fn refuses_a_field_no_event_carries() {
        let err = NotificationRules::compile(&[rule(&[("instance", ".*")])]).unwrap_err();
        // A rule on a field the stream does not carry would never match, and an
        // operator would read the silence as "no alerts".
        assert!(err.contains("instance"), "{err}");
        assert!(err.contains("severity"), "{err}");
    }

    /// Puts a real notification on screen. Ignored by default — a test run
    /// should not interrupt whoever started it — and run by hand to check that
    /// this machine's notification stack actually works:
    /// `cargo test -- --ignored show_reports_what_happened`.
    #[test]
    #[ignore = "shows a real desktop notification"]
    fn show_reports_what_happened() {
        let result = show("ca.cropa.promview", "Promview", "notification path check");
        // Either outcome is informative; what must not happen is the old
        // behaviour, where a failure was indistinguishable from success.
        println!("show() returned {result:?}");
        assert!(result.is_ok(), "{result:?}");
    }

    #[test]
    fn refuses_a_bad_pattern_and_an_empty_rule() {
        assert!(NotificationRules::compile(&[rule(&[("team", "(")])]).is_err());
        assert!(NotificationRules::compile(&[BTreeMap::new()]).is_err());
    }
}
