//! A read-only view of the firing alerts for other programs on this machine,
//! offered on the session bus.
//!
//! The shell already holds what a local reader needs: the alert stream, the
//! signed-in session and the way to sign in again. A program that wants to show
//! alerts (a desktop widget, a wallpaper) would otherwise poll the server with
//! a second copy of the credentials; asking this process instead keeps the
//! session in one place and lets the stream make the reader prompt.
//!
//! Reading only. The bus is open to every process of the user, so nothing that
//! changes an alert, and no credential, goes over it.
//!
//! The interface is written down in `org.promview.Desktop1.xml`.

/// The name the shell owns on the session bus.
pub const BUS_NAME: &str = "org.promview.Desktop";
/// Where the interface is served.
pub const OBJECT_PATH: &str = "/org/promview/Desktop";
/// The interface itself. Versioned in its name so a change that breaks a
/// reader is a new interface rather than a surprise.
pub const INTERFACE: &str = "org.promview.Desktop1";

/// How the shell stands with the server, as readers see it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum State {
    /// The last read worked.
    Ok,
    /// The server could not be reached, or answered with an error.
    Unreachable,
    /// The server wants a session this shell does not have.
    SignInNeeded,
}

impl State {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Ok => "ok",
            Self::Unreachable => "unreachable",
            Self::SignInNeeded => "signin-needed",
        }
    }
}

#[cfg(target_os = "linux")]
pub use linux::{serve, start, Handle};

#[cfg(not(target_os = "linux"))]
pub use other::{start, Handle};

#[cfg(target_os = "linux")]
mod linux {
    use std::sync::{Arc, Mutex};

    use zbus::object_server::{InterfaceRef, SignalEmitter};
    use zbus::{connection, fdo, interface, Connection};

    use super::{State, BUS_NAME, OBJECT_PATH};
    use crate::api::{ApiError, Client};

    /// What a failed `GetAlerts` is reported as. Readers tell the two apart:
    /// one is fixed by signing in, the other only by waiting.
    #[derive(Debug, zbus::DBusError)]
    #[zbus(prefix = "org.promview.Desktop.Error")]
    enum Error {
        #[zbus(error)]
        ZBus(zbus::Error),
        SignInNeeded(String),
        Unreachable(String),
    }

    impl From<ApiError> for Error {
        fn from(err: ApiError) -> Self {
            match err {
                ApiError::SignInNeeded(message) => Self::SignInNeeded(message),
                ApiError::Other(message) => Self::Unreachable(message),
            }
        }
    }

    struct Service {
        /// The runtime that reads are made on. A call arrives on zbus's own
        /// executor, which is not tokio, and the HTTP client needs tokio's
        /// reactor: polled anywhere else it hangs instead of failing.
        runtime: tokio::runtime::Handle,
        client: Client,
        server: String,
        state: Arc<Mutex<State>>,
    }

    #[interface(name = "org.promview.Desktop1")]
    impl Service {
        /// The firing alerts, as the server's own `GET /api/v1/alerts` answered.
        async fn get_alerts(&self, limit: u32, matches: Vec<String>) -> Result<String, Error> {
            let client = self.client.clone();
            let read = self
                .runtime
                .spawn(async move { client.firing_alerts(limit, &matches).await });
            match read.await {
                Ok(result) => Ok(result?),
                Err(err) => Err(Error::Unreachable(format!(
                    "the read did not finish: {err}"
                ))),
            }
        }

        /// Something the stream reported may have changed the alerts. Carries
        /// nothing: the reader asks again, with its own limit and matchers.
        #[zbus(signal)]
        async fn alerts_changed(emitter: &SignalEmitter<'_>) -> zbus::Result<()>;

        #[zbus(property)]
        fn state(&self) -> String {
            self.state
                .lock()
                .map(|state| *state)
                .unwrap_or(State::Unreachable)
                .as_str()
                .to_string()
        }

        #[zbus(property)]
        fn server(&self) -> String {
            self.server.clone()
        }
    }

    /// The running service: how the rest of the shell tells readers things.
    #[derive(Clone)]
    pub struct Handle {
        // Held so the connection, and with it the name, outlives `serve`.
        _connection: Connection,
        iface: InterfaceRef<Service>,
        state: Arc<Mutex<State>>,
    }

    impl Handle {
        /// Records how the last read went, and tells readers if that changed.
        pub async fn set_state(&self, new: State) {
            let changed = match self.state.lock() {
                Ok(mut slot) => std::mem::replace(&mut *slot, new) != new,
                Err(_) => false,
            };
            if changed {
                if let Err(err) = self
                    .iface
                    .get()
                    .await
                    .state_changed(self.iface.signal_emitter())
                    .await
                {
                    eprintln!("promview-desktop: dbus: cannot announce the state: {err}");
                }
            }
        }

        /// Tells readers the alerts may have changed.
        pub async fn alerts_changed(&self) {
            if let Err(err) = Service::alerts_changed(self.iface.signal_emitter()).await {
                eprintln!("promview-desktop: dbus: cannot announce a change: {err}");
            }
        }
    }

    /// Starts serving on the user's session bus. Never fatal: with no bus to
    /// talk to, or the name already taken by another shell, the tray carries on
    /// without offering anything, and says why.
    pub async fn start(client: Client, server: &url::Url) -> Option<Handle> {
        let builder = match connection::Builder::session() {
            Ok(builder) => builder,
            Err(err) => {
                eprintln!("promview-desktop: dbus: no session bus, not offering alerts: {err}");
                return None;
            }
        };
        match serve(builder, client, server.to_string()).await {
            Ok(handle) => {
                eprintln!("promview-desktop: dbus: offering alerts as {BUS_NAME}");
                Some(handle)
            }
            Err(err) => {
                eprintln!("promview-desktop: dbus: not offering alerts: {err}");
                None
            }
        }
    }

    /// Starts serving on the bus the builder points at.
    ///
    /// The name is requested without queueing: a second shell must not sit
    /// waiting to take over from the first and then answer for a server the
    /// user did not choose; it should find out now that it is not the one.
    pub async fn serve(
        builder: connection::Builder<'_>,
        client: Client,
        server: String,
    ) -> zbus::Result<Handle> {
        let state = Arc::new(Mutex::new(State::Unreachable));
        let connection = builder
            .serve_at(
                OBJECT_PATH,
                Service {
                    runtime: tokio::runtime::Handle::current(),
                    client,
                    server,
                    state: Arc::clone(&state),
                },
            )?
            .build()
            .await?;
        // Taken after the object is served, so a call that arrives the moment
        // the name appears already has somewhere to go.
        connection
            .request_name_with_flags(BUS_NAME, fdo::RequestNameFlags::DoNotQueue.into())
            .await?;
        let iface = connection
            .object_server()
            .interface::<_, Service>(OBJECT_PATH)
            .await?;
        Ok(Handle {
            _connection: connection,
            iface,
            state,
        })
    }
}

/// D-Bus is Linux's; elsewhere the shell reports to nobody, so what it would
/// say is dropped rather than every call site growing a platform check.
#[cfg(not(target_os = "linux"))]
mod other {
    use super::State;

    #[derive(Clone)]
    pub struct Handle;

    pub async fn start(_: crate::api::Client, _: &url::Url) -> Option<Handle> {
        None
    }

    impl Handle {
        pub async fn set_state(&self, _: State) {}
        pub async fn alerts_changed(&self) {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn states_have_stable_names() {
        // Readers match on these strings, so they are part of the interface.
        assert_eq!(State::Ok.as_str(), "ok");
        assert_eq!(State::Unreachable.as_str(), "unreachable");
        assert_eq!(State::SignInNeeded.as_str(), "signin-needed");
    }
}
