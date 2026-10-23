//! The bus interface against a real bus: a private `dbus-daemon`, the service
//! on one connection and a reader on another, which is how a program on the
//! user's machine meets it.

#![cfg(target_os = "linux")]

use std::io::{BufRead, BufReader};
use std::process::{Child, Command, Stdio};
use std::sync::Arc;
use std::time::Duration;

use futures_util::StreamExt;
use promview_desktop_lib::api::Client;
use promview_desktop_lib::credentials::Credentials;
use promview_desktop_lib::dbus::{serve, State, BUS_NAME, INTERFACE, OBJECT_PATH};
use url::Url;
use zbus::{connection, Connection};

/// A bus of its own, so the test never touches the user's session.
struct PrivateBus {
    child: Child,
    address: String,
}

impl PrivateBus {
    /// `None` where there is no `dbus-daemon`, which is a machine that cannot
    /// run this test. CI installs it and sets `CI`, and there a missing one is
    /// a failure: a test that skips itself everywhere proves nothing.
    fn start() -> Option<Self> {
        let spawned = Command::new("dbus-daemon")
            .args(["--session", "--nofork", "--print-address=1"])
            .stdout(Stdio::piped())
            .spawn();
        let mut child = match spawned {
            Ok(child) => child,
            Err(err) => {
                assert!(
                    std::env::var_os("CI").is_none(),
                    "dbus-daemon is required in CI: {err}"
                );
                eprintln!("skipping: no dbus-daemon ({err})");
                return None;
            }
        };
        let mut address = String::new();
        BufReader::new(child.stdout.take().expect("piped stdout"))
            .read_line(&mut address)
            .expect("read the bus address");
        Some(Self {
            child,
            address: address.trim().to_string(),
        })
    }

    async fn connect(&self) -> Connection {
        connection::Builder::address(self.address.as_str())
            .expect("bus address")
            .build()
            .await
            .expect("connect to the private bus")
    }

    fn builder(&self) -> connection::Builder<'_> {
        connection::Builder::address(self.address.as_str()).expect("bus address")
    }
}

impl Drop for PrivateBus {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

/// A server that answers every request with one status and body.
fn serve_forever(status: u16, body: &'static str) -> Url {
    let server = tiny_http::Server::http("127.0.0.1:0").expect("bind test server");
    let base = Url::parse(&format!("http://{}/", server.server_addr())).expect("test url");
    std::thread::spawn(move || {
        for request in server.incoming_requests() {
            let response = tiny_http::Response::from_string(body)
                .with_status_code(tiny_http::StatusCode(status));
            let _ = request.respond(response);
        }
    });
    base
}

fn client(base: Url, name: &str) -> Client {
    Client::new(
        reqwest::Client::new(),
        base,
        Duration::from_secs(5),
        Arc::new(Credentials::new(name)),
    )
}

async fn reader(bus: &PrivateBus) -> zbus::Proxy<'static> {
    zbus::Proxy::new(&bus.connect().await, BUS_NAME, OBJECT_PATH, INTERFACE)
        .await
        .expect("proxy")
}

#[tokio::test]
async fn a_reader_gets_the_servers_body_untouched() {
    let Some(bus) = PrivateBus::start() else {
        return;
    };
    let body = r#"{"alerts":[],"total":0,"severityCounts":{}}"#;
    let base = serve_forever(200, body);
    let _service = serve(
        bus.builder(),
        client(base.clone(), "dbus-body"),
        base.to_string(),
    )
    .await
    .expect("serve");

    let proxy = reader(&bus).await;
    let got: String = proxy
        .call("GetAlerts", &(50u32, vec!["team=platform".to_string()]))
        .await
        .expect("GetAlerts");
    assert_eq!(got, body);

    let server: String = proxy.get_property("Server").await.expect("Server");
    assert_eq!(server, base.to_string());
}

#[tokio::test]
async fn a_reader_can_tell_a_missing_session_from_an_unreachable_server() {
    let Some(bus) = PrivateBus::start() else {
        return;
    };

    let needs_session = serve_forever(401, "unauthorized");
    let _first = serve(
        bus.builder(),
        client(needs_session.clone(), "dbus-401"),
        needs_session.to_string(),
    )
    .await
    .expect("serve");
    let err = reader(&bus)
        .await
        .call::<_, _, String>("GetAlerts", &(10u32, Vec::<String>::new()))
        .await
        .expect_err("401 is an error");
    match err {
        zbus::Error::MethodError(name, _, _) => {
            assert_eq!(name.as_str(), "org.promview.Desktop.Error.SignInNeeded");
        }
        other => panic!("unexpected error: {other:?}"),
    }
}

#[tokio::test]
async fn a_server_error_is_reported_as_unreachable() {
    let Some(bus) = PrivateBus::start() else {
        return;
    };
    let broken = serve_forever(500, "boom");
    let _service = serve(
        bus.builder(),
        client(broken.clone(), "dbus-500"),
        broken.to_string(),
    )
    .await
    .expect("serve");

    let err = reader(&bus)
        .await
        .call::<_, _, String>("GetAlerts", &(10u32, Vec::<String>::new()))
        .await
        .expect_err("500 is an error");
    match err {
        zbus::Error::MethodError(name, _, _) => {
            assert_eq!(name.as_str(), "org.promview.Desktop.Error.Unreachable");
        }
        other => panic!("unexpected error: {other:?}"),
    }
}

#[tokio::test]
async fn readers_are_told_when_the_alerts_or_the_state_change() {
    let Some(bus) = PrivateBus::start() else {
        return;
    };
    let base = serve_forever(200, "{}");
    let service = serve(
        bus.builder(),
        client(base.clone(), "dbus-signals"),
        base.to_string(),
    )
    .await
    .expect("serve");
    let proxy = reader(&bus).await;

    let mut changed = proxy.receive_signal("AlertsChanged").await.expect("signal");
    let mut state = proxy.receive_property_changed::<String>("State").await;
    assert_eq!(
        proxy.get_property::<String>("State").await.expect("State"),
        State::Unreachable.as_str(),
        "nothing has been read yet, so nothing is known to be well"
    );

    service.alerts_changed().await;
    tokio::time::timeout(Duration::from_secs(5), changed.next())
        .await
        .expect("AlertsChanged arrives")
        .expect("stream open");

    service.set_state(State::Ok).await;
    // The first thing the stream yields can be the value as first read rather
    // than the change, so wait for the one this test caused.
    tokio::time::timeout(Duration::from_secs(5), async {
        while let Some(change) = state.next().await {
            if change.get().await.expect("value") == State::Ok.as_str() {
                return;
            }
        }
        panic!("the stream ended before the state changed");
    })
    .await
    .expect("PropertiesChanged arrives");
}

#[tokio::test]
async fn a_second_shell_does_not_queue_for_the_name() {
    let Some(bus) = PrivateBus::start() else {
        return;
    };
    let base = serve_forever(200, "{}");
    let _first = serve(
        bus.builder(),
        client(base.clone(), "dbus-first"),
        base.to_string(),
    )
    .await
    .expect("first shell");

    let second = serve(
        bus.builder(),
        client(base.clone(), "dbus-second"),
        base.to_string(),
    )
    .await;
    assert!(second.is_err(), "the name is taken, so this must say so");
}

#[tokio::test]
async fn the_interface_file_describes_what_the_service_serves() {
    let Some(bus) = PrivateBus::start() else {
        return;
    };
    let base = serve_forever(200, "{}");
    let _service = serve(
        bus.builder(),
        client(base.clone(), "dbus-xml"),
        base.to_string(),
    )
    .await
    .expect("serve");

    let introspectable = zbus::Proxy::new(
        &bus.connect().await,
        BUS_NAME,
        OBJECT_PATH,
        "org.freedesktop.DBus.Introspectable",
    )
    .await
    .expect("proxy");
    let live: String = introspectable
        .call("Introspect", &())
        .await
        .expect("Introspect");
    let file = include_str!("../../org.promview.Desktop1.xml");

    // Whitespace and attribute order are not the interface; the members and
    // their types are, so compare those.
    let members = |xml: &str| {
        let interface = xml
            .split(&format!("<interface name=\"{INTERFACE}\">"))
            .nth(1)
            .and_then(|rest| rest.split("</interface>").next())
            .unwrap_or_else(|| panic!("no {INTERFACE} in the XML"))
            .to_string();
        let mut found = Vec::new();
        for tag in interface.split('<').skip(1) {
            let tag = tag.split('>').next().unwrap_or("").trim_end_matches('/');
            let kind = tag.split_whitespace().next().unwrap_or("");
            if !["method", "signal", "property", "arg"].contains(&kind) {
                continue;
            }
            let attr = |key: &str| {
                tag.split(&format!("{key}=\""))
                    .nth(1)
                    .and_then(|rest| rest.split('"').next())
                    .unwrap_or("")
                    .to_string()
            };
            found.push(format!(
                "{kind} {} {} {} {}",
                attr("name"),
                attr("type"),
                attr("direction"),
                attr("access")
            ));
        }
        found.sort();
        found
    };
    assert_eq!(members(file), members(&live));
}
