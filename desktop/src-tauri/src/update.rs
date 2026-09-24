//! Noticing that a newer client has been released.
//!
//! The shell only says so; it never installs anything. Tauri's updater can
//! replace an AppImage on Linux but not a deb, rpm or pacman install, and an
//! application overwriting files its package manager owns is a fight nobody
//! wins. So the check reads the GitHub releases, compares versions, and leaves
//! the upgrade to however the client was installed.

use std::time::Duration;

use semver::Version;
use serde::Deserialize;

/// Every release, pre-releases included. `/releases/latest` skips those, and
/// while the project ships betas that would report nothing at all.
const RELEASES_URL: &str = "https://api.github.com/repos/cropalato/promview/releases?per_page=30";

/// Where to send someone who wants to see every release, not just the newest.
pub const RELEASES_PAGE: &str = "https://github.com/cropalato/promview/releases";

/// How often to look again. Releases are rare; a client left running for days
/// should still notice one, and the anonymous API allows sixty requests an hour.
pub const CHECK_INTERVAL: Duration = Duration::from_secs(12 * 60 * 60);

#[derive(Debug, Clone, Deserialize)]
pub struct Release {
    pub tag_name: String,
    pub html_url: String,
    #[serde(default)]
    pub draft: bool,
}

/// A release newer than the running client.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Upgrade {
    pub version: Version,
    pub url: String,
}

/// Reads a release tag as a version. Tags are `v`-prefixed; anything that does
/// not parse is not a client release and is skipped.
fn tag_version(tag: &str) -> Option<Version> {
    Version::parse(tag.strip_prefix('v').unwrap_or(tag)).ok()
}

/// The newest release worth offering, if any.
///
/// A client on a final release is not offered pre-releases: someone who chose
/// a stable build should not be told a beta is an upgrade. A client already on
/// a pre-release is offered both, since the final it is heading for is one.
pub fn newest_upgrade(current: &Version, releases: &[Release]) -> Option<Upgrade> {
    releases
        .iter()
        .filter(|release| !release.draft)
        .filter_map(|release| Some((tag_version(&release.tag_name)?, release)))
        .filter(|(version, _)| !current.pre.is_empty() || version.pre.is_empty())
        .filter(|(version, _)| version > current)
        .max_by(|(a, _), (b, _)| a.cmp(b))
        .map(|(version, release)| Upgrade {
            version,
            url: release.html_url.clone(),
        })
}

/// The tray menu's version line.
pub fn menu_label(current: &Version, upgrade: Option<&Upgrade>) -> String {
    match upgrade {
        Some(upgrade) => format!("Update available: {} (running {current})…", upgrade.version),
        None => format!("Promview {current}"),
    }
}

/// Asks GitHub for the releases and picks the one to offer.
pub async fn check(current: &Version) -> Result<Option<Upgrade>, String> {
    // Its own client: the API proxy's carries the operator's server session
    // and cookies, none of which have any business going to GitHub.
    let http = reqwest::Client::builder()
        .timeout(Duration::from_secs(20))
        // GitHub refuses API requests without a user agent.
        .user_agent(format!("promview-desktop/{current}"))
        .build()
        .map_err(|err| format!("build update-check client: {err}"))?;
    let response = http
        .get(RELEASES_URL)
        .header("Accept", "application/vnd.github+json")
        .send()
        .await
        .map_err(|err| format!("check for updates: {err}"))?;
    if !response.status().is_success() {
        return Err(format!(
            "check for updates: GitHub returned HTTP {}",
            response.status()
        ));
    }
    let releases: Vec<Release> = response
        .json()
        .await
        .map_err(|err| format!("decode the release list: {err}"))?;
    Ok(newest_upgrade(current, &releases))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn release(tag: &str) -> Release {
        Release {
            tag_name: tag.to_string(),
            html_url: format!("https://github.com/cropalato/promview/releases/tag/{tag}"),
            draft: false,
        }
    }

    fn version(raw: &str) -> Version {
        Version::parse(raw).unwrap()
    }

    #[test]
    fn offers_the_newest_release() {
        let releases = [
            release("v0.1.0-beta.1"),
            release("v0.1.0-beta.3"),
            release("v0.1.0-beta.2"),
        ];
        let upgrade = newest_upgrade(&version("0.1.0-beta.1"), &releases).unwrap();
        assert_eq!(upgrade.version, version("0.1.0-beta.3"));
        assert!(upgrade.url.ends_with("/v0.1.0-beta.3"));
    }

    #[test]
    fn offers_nothing_when_up_to_date() {
        let releases = [release("v0.1.0-beta.1"), release("v0.1.0-beta.2")];
        assert_eq!(newest_upgrade(&version("0.1.0-beta.2"), &releases), None);
    }

    #[test]
    fn compares_pre_release_counters_numerically() {
        // A string comparison would put beta.10 before beta.9.
        let releases = [release("v0.1.0-beta.10")];
        assert!(newest_upgrade(&version("0.1.0-beta.9"), &releases).is_some());
    }

    #[test]
    fn a_pre_release_client_is_offered_the_final_release() {
        let releases = [release("v0.1.0")];
        let upgrade = newest_upgrade(&version("0.1.0-beta.2"), &releases).unwrap();
        assert_eq!(upgrade.version, version("0.1.0"));
    }

    #[test]
    fn a_final_release_client_is_not_offered_a_beta() {
        let releases = [release("v0.2.0-beta.1")];
        assert_eq!(newest_upgrade(&version("0.1.0"), &releases), None);
    }

    #[test]
    fn a_local_build_sorts_above_the_betas_of_its_version() {
        // An unstamped working-copy build carries tauri.conf.json's 0.1.0, and
        // should not nag the person building it.
        let releases = [release("v0.1.0-beta.2")];
        assert_eq!(newest_upgrade(&version("0.1.0"), &releases), None);
    }

    #[test]
    fn skips_drafts_and_tags_that_are_not_versions() {
        let mut draft = release("v0.2.0");
        draft.draft = true;
        let releases = [draft, release("nightly"), release("v0.1.1")];
        let upgrade = newest_upgrade(&version("0.1.0"), &releases).unwrap();
        assert_eq!(upgrade.version, version("0.1.1"));
    }

    #[test]
    fn decodes_the_fields_it_needs_from_the_api() {
        let body = r#"[{"tag_name":"v0.1.0-beta.3","html_url":"https://example/r","draft":false,"prerelease":true,"assets":[]}]"#;
        let releases: Vec<Release> = serde_json::from_str(body).unwrap();
        assert_eq!(releases[0].tag_name, "v0.1.0-beta.3");
    }

    #[test]
    fn the_menu_label_carries_the_version() {
        let current = version("0.1.0-beta.2");
        let upgrade = Upgrade {
            version: version("0.1.0-beta.3"),
            url: String::new(),
        };
        assert_eq!(menu_label(&current, None), "Promview 0.1.0-beta.2");
        assert_eq!(
            menu_label(&current, Some(&upgrade)),
            "Update available: 0.1.0-beta.3 (running 0.1.0-beta.2)…"
        );
    }
}
