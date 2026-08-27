//! Where the session token lives.
//!
//! In the platform secret store when there is one, and in memory when there is
//! not. Never on disk: a bearer token in a file is readable by anything running
//! as the user, and "it survives a restart" is not worth that. A machine with
//! no keyring daemon — a minimal Linux desktop, a container — signs in again
//! next launch, and is told so rather than left to guess.
//!
//! The token never reaches the webview. Page script cannot read it, which is
//! the whole reason transport lives in this process.
//!
//! Every call into the secret store is given a deadline. "No keyring daemon"
//! is not the only way a store fails to answer: a locked collection makes the
//! D-Bus unlock call block until something prompts for a password, and on a
//! session with no prompter running nothing ever does. Untimed, that is not a
//! slow sign-in but one that never returns, with nothing on screen to say why.
//! A deadline turns it back into the case this module already handles — a
//! store that cannot be used, and an operator who is told they will sign in
//! again next launch.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{mpsc, Mutex};
use std::time::Duration;

const SERVICE: &str = "promview-desktop";

/// How long a secret store gets to answer.
///
/// A working one answers in milliseconds; this is long enough that a busy
/// machine is not mistaken for a broken one, and short enough that a window
/// waiting on it does not look hung. A store that is only slow because it put
/// an unlock prompt on screen may well answer after this, and the write still
/// lands - what the operator is told is pessimistic rather than wrong.
const KEYRING_TIMEOUT: Duration = Duration::from_secs(3);

/// Runs a secret-store call with a deadline, on a thread of its own.
///
/// The thread is abandoned rather than cancelled when the deadline passes,
/// because a D-Bus call that is waiting on a prompt cannot be interrupted. It
/// costs one blocked thread for the life of the process, which is the price of
/// the alternative being a blocked application.
fn with_deadline<T, F>(timeout: Duration, op: F) -> Option<T>
where
    F: FnOnce() -> Option<T> + Send + 'static,
    T: Send + 'static,
{
    let (sender, receiver) = mpsc::channel();
    std::thread::spawn(move || {
        // A send that fails means the deadline already passed and nobody is
        // listening any more, which is not this thread's problem.
        let _ = sender.send(op());
    });
    receiver.recv_timeout(timeout).ok().flatten()
}

/// Whether the token outlives the process.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Durability {
    /// Saved in the platform secret store.
    Keychain,
    /// Held for this run only, because no secret store was reachable.
    Memory,
}

pub struct Credentials {
    /// Keyed by server, so pointing the client at a different Promview does not
    /// hand it the previous one's session.
    account: String,
    cached: Mutex<Option<String>>,
    /// Set the first time the store misses its deadline. Every later call skips
    /// it outright: a store that hung once will hang again, and paying the
    /// deadline on every request would make the whole client feel broken
    /// instead of just the part that remembers a session.
    unavailable: AtomicBool,
}

impl Credentials {
    pub fn new(server: &str) -> Self {
        Self {
            account: server.to_string(),
            cached: Mutex::new(None),
            unavailable: AtomicBool::new(false),
        }
    }

    /// Runs one secret-store call, unless the store has already given up on
    /// this process. The entry is built inside the call so nothing that is not
    /// this thread's to move crosses the boundary.
    fn with_store<T, F>(&self, what: &str, op: F) -> Option<T>
    where
        F: FnOnce(keyring::Entry) -> Option<T> + Send + 'static,
        T: Send + 'static,
    {
        if self.unavailable.load(Ordering::Relaxed) {
            return None;
        }
        let account = self.account.clone();
        let result = with_deadline(KEYRING_TIMEOUT, move || {
            op(keyring::Entry::new(SERVICE, &account).ok()?)
        });
        if result.is_none() {
            // Not distinguishable from here: a store that refused and a store
            // that never answered both mean the token lives in memory. Only
            // the second is worth a line, and only the first time.
            if !self.unavailable.swap(true, Ordering::Relaxed) {
                eprintln!(
                    "promview-desktop: the secret store did not answer within {}s ({what}); keeping the session in memory for this run",
                    KEYRING_TIMEOUT.as_secs()
                );
                eprintln!(
                    "promview-desktop: a locked keyring with nothing to prompt for it does this; unlock it and sign in again to have the session survive a restart"
                );
            }
        }
        result
    }

    /// The stored token, if there is one. Reads the cache first so a locked or
    /// slow secret store is not consulted on every request.
    pub fn token(&self) -> Option<String> {
        if let Ok(cached) = self.cached.lock() {
            if let Some(token) = cached.as_ref() {
                return Some(token.clone());
            }
        }
        let token = self.with_store("read", |entry| entry.get_password().ok())?;
        if let Ok(mut cached) = self.cached.lock() {
            *cached = Some(token.clone());
        }
        Some(token)
    }

    /// Stores a token, reporting whether it will survive a restart.
    ///
    /// A secret store that refuses is not an error the operator can act on
    /// mid-flow: they are signed in either way, and the difference is only
    /// whether they will be again tomorrow.
    pub fn store(&self, token: &str) -> Durability {
        if let Ok(mut cached) = self.cached.lock() {
            *cached = Some(token.to_string());
        }
        let token = token.to_string();
        match self.with_store("store", move |entry| entry.set_password(&token).ok()) {
            Some(()) => Durability::Keychain,
            None => Durability::Memory,
        }
    }

    /// Forgets the token here and in the secret store.
    ///
    /// The in-memory copy is cleared first and unconditionally: signing out
    /// must not leave a usable token behind because a keyring call failed.
    pub fn clear(&self) {
        if let Ok(mut cached) = self.cached.lock() {
            *cached = None;
        }
        let _ = self.with_store("clear", |entry| entry.delete_credential().ok());
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reports_a_token_it_was_given_without_consulting_the_store() {
        // Whether a keyring exists on the machine running these tests is not
        // something to depend on; the cache is what every request reads.
        let credentials = Credentials::new("https://promview.example");
        assert_eq!(credentials.token(), None);

        credentials.store("session-token");
        assert_eq!(credentials.token(), Some("session-token".to_string()));
    }

    #[test]
    fn clearing_forgets_the_token_even_where_no_store_exists() {
        let credentials = Credentials::new("https://promview.example");
        credentials.store("session-token");
        credentials.clear();
        // Signing out must not leave a usable token behind, whatever the
        // keyring did or failed to do.
        assert_eq!(credentials.token(), None);
    }

    #[test]
    fn a_call_that_never_answers_gives_up_instead_of_blocking() {
        // The regression this guards is not hypothetical: a locked keyring
        // with no prompter running makes the D-Bus unlock call block forever,
        // and sign-in never returned.
        let started = std::time::Instant::now();
        let answer: Option<()> = with_deadline(Duration::from_millis(50), || {
            std::thread::sleep(Duration::from_secs(30));
            Some(())
        });
        assert_eq!(answer, None);
        assert!(started.elapsed() < Duration::from_secs(5), "it waited");
    }

    #[test]
    fn a_call_that_answers_in_time_is_not_thrown_away() {
        assert_eq!(
            with_deadline(Duration::from_secs(5), || Some("token".to_string())),
            Some("token".to_string())
        );
        // A store that answers "nothing here" is not a store that failed, but
        // both leave the caller with no token.
        assert_eq!(
            with_deadline(Duration::from_secs(5), || None::<String>),
            None
        );
    }

    #[test]
    fn a_store_that_gave_up_once_is_not_consulted_again() {
        let credentials = Credentials::new("https://timed-out.example");
        credentials.unavailable.store(true, Ordering::Relaxed);

        let started = std::time::Instant::now();
        assert_eq!(credentials.store("session-token"), Durability::Memory);
        assert_eq!(credentials.token(), Some("session-token".to_string()));
        credentials.clear();
        // Every call skipped the store outright rather than paying the
        // deadline again; a client that stalled on each request would read as
        // broken everywhere, not just where sessions are remembered.
        assert!(
            started.elapsed() < KEYRING_TIMEOUT,
            "it consulted the store"
        );
    }

    #[test]
    fn a_second_server_does_not_inherit_the_first_ones_session() {
        let first = Credentials::new("https://one.example");
        let second = Credentials::new("https://two.example");
        first.store("first-token");
        assert_eq!(second.token(), None);
        second.clear();
        first.clear();
    }
}
