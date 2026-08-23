# Promview desktop shell

A Tauri 2 shell around the same React console the browser serves. Not a separate
product and not a native rewrite: the frontend is `../web`, unchanged, pointed at
a configured server instead of at its own origin.

This is the walking skeleton from `docs/desktop-client-plan.md`, not the MVP.

## What works

- Builds and runs on Linux, loading the console in a window.
- A tray icon with a menu: open the console, toggle a compact always-on-top
  window, quit. Closing a window hides it; the tray owns the process lifetime.
- The tray tooltip shows firing counts by severity, refreshed whenever the stream
  reports a change and on a slow timer as a fallback.
- `PROMVIEW_SERVER_URL` selects the server, validated on the way in.
- Signing in against an `oidc` deployment, from the tray menu.
- Native notifications for alerts the console's selector matches.

- The console loads and works: alerts, groups, detail, filters, preferences. Its
  API requests go through the Rust core over Tauri's `invoke`, not from the
  webview.

## How requests flow

The webview never calls the server. `web/src/config/hostBridge.ts` installs a
transport that hands each request to the `api_request` command, and every client
module already defaults to it.

Two things fall out of that, and the second is the reason:

- No CORS is needed. A webview talking to a remote server is cross-origin, and a
  server that serves its own console same-origin has no reason to send CORS
  headers.
- Credentials stay out of the webview. The cookie jar lives in the Rust client,
  where page script cannot read it — which is what makes OIDC sessions safe to
  add next.

The page names a **path**, never a host. The core resolves it against the server
this process was configured with, so a compromised page cannot point the client
somewhere else and hand it whatever the jar holds. It also forwards only headers
that are the page's business; anything identifying the caller is the core's.

The live stream goes the same way. The core holds the SSE connection and pushes
each frame into the page, so the console reports `stream: live` and updates
without a refresh. Reconnect policy stays in the console, which already has a
tested one with backoff and cursor resumption; duplicating it in Rust would give
the two halves separate opinions about when to give up.

The tray reads from that same connection. It re-reads the counts on each change
rather than applying events as deltas, which is what the console does too:
deriving totals from a delta stream means tracking every alert's severity and
state, and being wrong in a way nobody notices until the number is. A burst of
events settles for half a second first, so an alert storm costs one request
rather than hundreds.

One caveat: the console opens the stream, so the tray only gets prompt updates
once a window has loaded. Closing a window hides it rather than destroying it,
so this holds for the life of the process — and the fallback timer covers the
gap before the first load either way. Moving stream ownership into the core
entirely would remove the caveat, at the cost of the core needing its own
snapshot cursor.

## Signing in

The tray's **Sign in…** opens the _system_ browser, not a window of ours. The
identity provider's login form belongs somewhere the operator has an address bar
to check and their own password manager to hand — a login form inside our
webview is the shape phishing takes.

A loopback listener is bound first, on a port the operating system picks, and
its address is handed to the server as the place to send the result. What comes
back is a one-time code, not a credential; the core posts it for an ordinary
session token. The desktop never speaks to the identity provider and never holds
a token issued by one.

The session goes into the platform secret store, keyed by server URL so pointing
the client elsewhere does not hand it the previous one's session. It is attached
to every request and to the stream by the core, and never reaches the webview:
the proxy refuses an `Authorization` header the page tries to set, so the only
credential that can leave this process is the one the core holds.

**When there is no secret store** — a minimal Linux desktop, a container, no
keyring daemon — the token is kept in memory for the run and the operator is
told. They stay signed in until the process exits, then sign in again. Nothing
secret is written to disk: a bearer token in a file is readable by anything
running as the user, and surviving a restart is not worth that.

## Notifications

Shown by the host, because WebKitGTK has no usable Notification API — without
this the console's notifications never appear at all in a shell built on it.

Only the _showing_ moves. Whether to notify stays in the console, which owns the
opt-in, the label selector, and the ledger that stops a replayed event notifying
twice. That is the same split as the stream's reconnect policy, and for the same
reason: two implementations of one rule eventually disagree.

Two limits worth knowing. Clicking a notification does nothing yet — the host
has no click callback to hand back, so deep-linking to the alert is still to
come. And the console only notifies while its window is hidden, as it does in a
browser, so a visible window shows the alert in the table instead.

## Installers

Tagging a release builds the client on Linux and Windows and attaches the
installers to the GitHub release: `.deb` and `.rpm`, `.msi` and an NSIS `.exe`.
Each platform builds its own, because cross-compiling a Tauri bundle is not
practical — the installer formats are produced by that platform's own tooling.

They are **unsigned**. Windows SmartScreen warns on first run. Signing needs
certificates this project does not have.

Arch Linux gets a `.pkg.tar.zst` too. Tauri has no pacman target, so the deb is
repackaged by `desktop/packaging/arch/PKGBUILD` rather than compiled a second
time — a second from-source build could only disagree with the binary shipped
everywhere else. To build one by hand, copy the deb next to that PKGBUILD as
`promview-desktop.deb` and run `makepkg`.

AppImage is deliberately not built. It is in Tauri's target list but
`linuxdeploy` fails here even with FUSE present, and shipping a format nobody
has seen succeed is worse than not shipping it. Adding `"appimage"` to
`bundle.targets` is all it takes once someone confirms it works.

macOS is not built at all: it needs an Apple runner and a developer certificate.

## What does not work yet

The updater. Tauri's verifies signatures, so it waits on signing.

## Running it

Needs Rust, Node 22, and the Tauri 2 Linux dependencies (`webkit2gtk-4.1`,
`libsoup-3.0`, `gtk+-3.0`, `libayatana-appindicator3`).

```sh
npm --prefix desktop install          # Tauri CLI
PROMVIEW_SERVER_URL=http://localhost:8080 npm --prefix desktop run dev
```

`make verify-desktop` runs `cargo fmt --check`, `clippy -D warnings`, `test`, and
`build`; it is part of `make verify` and has its own CI job.

### A Linux rendering note

On some GPU and compositor combinations WebKitGTK fails to allocate its buffers
and the window renders blank, with `Failed to create GBM buffer` on stderr. It is
a WebKitGTK issue rather than anything in this crate:

```sh
WEBKIT_DISABLE_DMABUF_RENDERER=1 npm --prefix desktop run dev
```

## Configuration

| Variable                      | Default                 | Meaning                                                                                                                                                                                              |
| ----------------------------- | ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PROMVIEW_SERVER_URL`         | `http://localhost:8080` | Server to talk to. Must be absolute http/https, may carry a path prefix, must not carry a query or fragment.                                                                                         |
| `PROMVIEW_POLL_INTERVAL_SECS` | `60`                    | Fallback tray refresh, for before a stream is open and while one is down. The stream is what makes the tray prompt. Minimum 5, because a tight loop against a shared server costs everyone using it. |

Environment variables are the walking skeleton's answer. A settings surface and
multiple server profiles are later work in the plan.
