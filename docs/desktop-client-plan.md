# Desktop And Widget Client Plan

## Direction

Use the same React application in a Tauri 2 shell. Treat desktop support as another client of the Promview REST and SSE contracts, not as a separate product or native UI rewrite.

A PWA can be shipped early for installability, but it cannot reliably maintain a background SSE connection or provide a consistent system tray and always-on-top window. Electron offers similar functionality to Tauri with a substantially larger runtime footprint. Platform-native widgets would require separate Windows, macOS, and Linux implementations.

## Target Experience

The desktop client should provide:

- system tray or menu-bar presence
- severity-aware active alert count
- compact always-on-top alert window
- native notifications filtered by labels and severity
- background SSE connection while UI windows are closed
- quick access to the complete console
- secure storage in the operating system keychain
- Linux, macOS, and Windows builds

The compact UI should reuse the browser console's alert list, filters, severity indicators, details, and action components.

## Required Server Contracts

Desktop compatibility must be designed into the server before the shell is built:

- REST and SSE accept revocable opaque bearer credentials in addition to browser sessions.
- SSE supports `Last-Event-ID` and explicit resynchronization after retention gaps.
- Events are typed and coarse enough to drive tray state and notifications without diffing full snapshots.
- The API client accepts injected base URL and credential providers.
- Authorization behavior is identical for browser and desktop clients.
- Notification preferences are server-side label selectors so policy is shared across future clients.

## Authentication

### OIDC

Open the system browser, use Authorization Code with PKCE, and return through a loopback callback. Support a registered custom protocol as a fallback when an identity provider rejects loopback redirects.

Exchange the callback result for revocable Promview desktop credentials and place them in the OS keychain. Do not expose identity-provider refresh tokens to React code.

### Open

Connect as the anonymous viewer without stored credentials. The client must not display enabled mutation controls.

## Tauri Structure

The Rust core owns:

- credential access
- REST/SSE transport
- reconnect and backoff
- tray icon and menu
- native notifications
- window creation and always-on-top behavior
- updater integration

React owns presentation and user interaction. The Rust core sends normalized stream events to open windows. This keeps credentials out of the webview and allows the stream to continue with no visible window.

## Desktop MVP

- one configured Promview server
- system tray icon and alert-count tooltip
- compact alert list window
- click tray icon to toggle the compact window
- open full console action
- background resumable SSE
- native critical/warning notifications
- OIDC and open mode login
- OS keychain storage
- view, acknowledge, assign, close, and notes according to server permissions
- manual update check

Keeping operator actions in the desktop MVP avoids an artificial behavior gap because the shared React controls and server endpoints already exist.

## Later Work

- multiple server profiles
- notification schedules and quiet hours
- automatic updater
- signed and notarized release pipeline
- offline snapshot and richer reconnect behavior
- Alertmanager silence actions after server support exists
- native badge APIs where available
- mobile exploration through Tauri 2 only after desktop behavior is stable

## Risks

- Linux WebKitGTK versions require early compatibility testing.
- Apple notarization and Windows signing add external accounts, certificates, and release work.
- Some OIDC providers restrict loopback or custom-protocol redirects.
- Corporate proxies may buffer SSE, requiring a polling fallback.
- Native notifications must be rate-limited and grouped during alert floods.
- A compromised webview must not gain direct access to stored credentials.

## Relative Effort

Assuming the browser application and client-neutral server contracts exist:

| Deliverable | Approximate effort |
| --- | --- |
| Installable PWA | 0.5 to 1 week |
| Server desktop-auth and SSE preparation | 1 to 2 weeks, preferably included in server MVP |
| Tauri tray and compact client | 3 to 5 weeks |
| Signing, notarization, and automatic updates | 1 or more additional weeks |

## Delivery Sequence

1. Establish client-neutral REST, authentication, and SSE contracts in the server MVP.
2. Keep shared React components independent of browser-only APIs.
3. Ship a PWA manifest with the browser console if it does not complicate caching or authentication.
4. Stabilize alert-list components and stream behavior under load.
5. Add the Tauri shell, secure credentials, tray state, compact window, and notifications.
6. Add signing and automatic updates when release channels are defined.
