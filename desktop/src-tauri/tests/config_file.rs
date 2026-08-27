//! The config file end to end: found on disk, parsed, its `[env]` table
//! exported, and the settings resolved from the environment that results.
//!
//! One test, deliberately. It mutates the process environment, which every
//! other test in this binary would see, and the point is the whole path from a
//! file on disk to a loaded `Config` — the pieces are covered by unit tests in
//! `config.rs`.

use std::fs;
use std::path::PathBuf;

use promview_desktop_lib::config::{
    api_base, Config, CONFIG_PATH_ENV, POLL_INTERVAL_ENV, SERVER_URL_ENV,
};

fn scratch_file(contents: &str) -> PathBuf {
    let path = std::env::temp_dir().join(format!("promview-desktop-{}.toml", std::process::id()));
    fs::write(&path, contents).expect("write the config file");
    path
}

#[test]
fn loads_a_file_named_by_the_environment() {
    let path = scratch_file(
        r#"
        server_url = "https://ops.example/promview"
        poll_interval_secs = 15

        [env]
        PROMVIEW_TEST_CA = "/etc/promview/ca.pem"

        [[notifications.rules]]
        severity = "^critical$"
        team = "^core$"

        [[notifications.rules]]
        alertname = "(?i)disk"
        "#,
    );
    std::env::set_var(CONFIG_PATH_ENV, &path);
    // Cleared because they win: whoever runs this may well have the real
    // client's server exported, and the file only speaks for what the
    // environment left unsaid.
    std::env::remove_var(SERVER_URL_ENV);
    std::env::remove_var(POLL_INTERVAL_ENV);

    let config = Config::load().expect("load the config file");

    assert_eq!(api_base(&config.server_url), "https://ops.example/promview");
    assert_eq!(config.poll_interval_secs, 15);
    assert_eq!(config.notification_rules.len(), 2);
    assert_eq!(config.source.as_deref(), Some(path.as_path()));
    // The `[env]` table is exported, which is the only reason it exists: the
    // variables it carries are read by libraries, not by this crate.
    assert_eq!(
        std::env::var("PROMVIEW_TEST_CA").ok().as_deref(),
        Some("/etc/promview/ca.pem")
    );

    // Reloading reads the same file, which is what the tray's menu item does.
    let reloaded = Config::reload_rules(config.source.as_deref()).expect("reload the rules");
    assert_eq!(reloaded.len(), 2);

    // A file that does not parse leaves the loaded rules alone rather than
    // silently turning the filter off mid-edit.
    fs::write(&path, "[[notifications.rules]]\nseverity = \"(\"\n").expect("rewrite");
    assert!(Config::reload_rules(Some(path.as_path())).is_err());

    // A named file that is not there is an error, not a fallback to defaults.
    std::env::set_var(CONFIG_PATH_ENV, path.with_extension("missing"));
    assert!(Config::load().is_err());

    std::env::remove_var(CONFIG_PATH_ENV);
    std::env::remove_var("PROMVIEW_TEST_CA");
    let _ = fs::remove_file(&path);
}
