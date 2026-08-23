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

use std::sync::Mutex;

const SERVICE: &str = "promview-desktop";

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
}

impl Credentials {
    pub fn new(server: &str) -> Self {
        Self {
            account: server.to_string(),
            cached: Mutex::new(None),
        }
    }

    fn entry(&self) -> Option<keyring::Entry> {
        keyring::Entry::new(SERVICE, &self.account).ok()
    }

    /// The stored token, if there is one. Reads the cache first so a locked or
    /// slow secret store is not consulted on every request.
    pub fn token(&self) -> Option<String> {
        if let Ok(cached) = self.cached.lock() {
            if let Some(token) = cached.as_ref() {
                return Some(token.clone());
            }
        }
        let token = self.entry()?.get_password().ok()?;
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
        match self.entry() {
            Some(entry) if entry.set_password(token).is_ok() => Durability::Keychain,
            _ => Durability::Memory,
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
        if let Some(entry) = self.entry() {
            let _ = entry.delete_credential();
        }
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
    fn a_second_server_does_not_inherit_the_first_ones_session() {
        let first = Credentials::new("https://one.example");
        let second = Credentials::new("https://two.example");
        first.store("first-token");
        assert_eq!(second.token(), None);
        second.clear();
        first.clear();
    }
}
