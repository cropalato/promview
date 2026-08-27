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
- `PROMVIEW_SERVER_URL` selects the server, validated on the way in, or an
  optional config file does — including a per-machine alert filter and variables
  to export before the webview starts.
- Signing in against an `oidc` deployment, from the tray menu.
- Native notifications for alerts the console's selector matches, narrowed by
  this machine's own filter if it has one.

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

### Servers behind a private CA

The core trusts the machine's own certificate store, via reqwest's
`rustls-tls-native-roots`. An internal Promview behind a corporate or private CA
is reached on the strength of a certificate the rest of the workstation already
accepts, with nothing to configure.

This is worth stating because the alternative was the default and was wrong here.
Built against plain `rustls-tls`, reqwest carries a compiled-in copy of the
Mozilla root set and never consults the platform's, so a private CA failed the
handshake and reported it as:

```
error sending request for url (https://promview.internal/api/v1/alerts?...)
```

which reads as a server that is down rather than one that is untrusted — and no
environment variable could correct it, because the roots were inside the binary.
If a client ever reports a host as unreachable that `curl` reaches from the same
machine, suspect the trust store before the network.

To point at a bundle kept outside the system store, `SSL_CERT_FILE` and
`SSL_CERT_DIR` are honoured (through `rustls-native-certs`, which probes them the
way OpenSSL does).

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

`notify-rust` is called directly rather than through `tauri-plugin-notification`.
The plugin shows on a spawned task and drops the result, so every failure — no
notification daemon, notifications switched off for the app, an
AppUserModelID Windows does not recognise — arrived as success. A page that never
appeared and reported success is the one failure mode nobody notices, so the
outcome is now waited for, returned to the console, and logged on stderr either
way.

### The local filter

One thing about _whether_ to notify is genuinely per-machine and cannot live in a
server-side selector every client shares: a laptop that should only ever buzz for
its owner's team. `[[notifications.rules]]` in the config file is that, and only
that — it narrows what the console already chose to send and never widens it, so
a machine with no rules behaves exactly as before the file existed.

Rules are ORed, fields within a rule ANDed, values are unanchored regular
expressions over the fields a stream event carries (`severity`, `alertname`,
`source`, `team`, `summary`):

```toml
[[notifications.rules]]
severity = "^critical$"
team = "^(core|platform)$"

[[notifications.rules]]
alertname = "(?i)disk"
```

A bad pattern or a field no event carries is refused at startup rather than at
the first alert. A suppressed notification is logged, because "the filter ate it"
and "the daemon is down" are otherwise the same silence. The tray's **Reload
alert filter** re-reads this section only, so a rule can be tried against live
alerts without relaunching.

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

### Build it through the Tauri CLI, not bare cargo

```sh
npm --prefix desktop run build                        # what CI does
cargo build --release --features tauri/custom-protocol # the same thing by hand
```

A plain `cargo build --release` compiles and links and produces a binary that
does not work. `tauri`'s build script sets `dev = !has_feature("custom-protocol")`,
so without that feature the shell loads `devUrl` — `http://localhost:5173` —
rather than the console embedded from `web/dist`. With no Vite server there, the
window shows **Could not connect to localhost: Connection refused** while stderr
looks perfectly healthy: the tray polls, the server answers, nothing reports an
error. The dev-mode binary is about 100KB smaller, which is the quickest way to
tell the two apart.

The `cargo build` in `make verify-desktop` is a compile check and correct as one.
It is not a way to produce a client you can run.

### A Linux rendering note

Since 2.42 WebKitGTK renders through a DMA-BUF path: the web process allocates
its buffers through GBM on a DRM render node and passes the file descriptors to
the UI process. It is developed against Mesa, and on the NVIDIA driver the
allocation fails — the window renders nothing at all and `Failed to create GBM
buffer` goes to stderr. It is a WebKitGTK issue rather than anything in this
crate, but the symptom is indistinguishable from this application being broken,
so the shell now guesses rather than leaving an operator to find out.

At startup it probes for a DRM render node and for the driver behind it, and
switches the renderer off where there is no node to allocate from — a container,
a VM with no GPU — or where the node is NVIDIA's. The guess is deliberately
biased: disabling the renderer where it would have worked costs shared-memory
buffers and some CPU, which for an alert console nobody notices; leaving it on
where it does not work costs the whole window. It says which way it went and why
on stderr.

`webkit_dmabuf` in the config file overrides the probe — `"on"` where the guess
is wrong about a machine, `"off"` to skip it. `WEBKIT_DISABLE_DMABUF_RENDERER`
in the environment beats both, and nothing removes a variable you exported:

```sh
WEBKIT_DISABLE_DMABUF_RENDERER=1 npm --prefix desktop run dev
```

## Configuration

| Variable                      | Default                 | Meaning                                                                                                                                                                                              |
| ----------------------------- | ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PROMVIEW_SERVER_URL`         | `http://localhost:8080` | Server to talk to. Must be absolute http/https, may carry a path prefix, must not carry a query or fragment.                                                                                         |
| `PROMVIEW_POLL_INTERVAL_SECS` | `60`                    | Fallback tray refresh, for before a stream is open and while one is down. The stream is what makes the tray prompt. Minimum 5, because a tight loop against a shared server costs everyone using it. |

`SSL_CERT_FILE` and `SSL_CERT_DIR` are read too, for a certificate bundle kept
outside the system trust store; see [Servers behind a private CA](#servers-behind-a-private-ca).

### The config file

Optional, TOML, and read from the first of these that exists:

| Platform | Location                                                                                                                                                                                                  |
| -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Linux    | `$XDG_CONFIG_HOME/promview-desktop/config.toml` (so `~/.config/…`), then `config` without the extension, then `~/.promview-desktop/config.toml`, `~/.promview-desktop/config`, `~/.promview-desktop.toml` |
| Windows  | `%APPDATA%\promview-desktop\config.toml`, then the `$HOME` fallbacks above                                                                                                                                |
| macOS    | `~/Library/Application Support/promview-desktop/config.toml`, then the `$HOME` fallbacks above                                                                                                            |

`PROMVIEW_DESKTOP_CONFIG` names a file outright and skips the search. It is an
error if that file is not there: an operator who said where their settings are
should not silently get defaults instead.

`desktop/config.example.toml` is a commented copy of everything below.

```toml
server_url = "https://promview.internal"
poll_interval_secs = 60
webkit_dmabuf = "auto"   # or "on" / "off"; see A Linux rendering note

[env]
SSL_CERT_FILE = "/etc/promview/internal-ca.pem"
WEBKIT_DISABLE_DMABUF_RENDERER = "1"

[[notifications.rules]]
severity = "^critical$"
team = "^(core|platform)$"
```

Unknown keys are **refused**, not ignored. A settings file whose typos pass is a
file you believe is in effect when it is not.

The `[env]` table exists for the settings that are not this application's own —
a private CA bundle, a WebKitGTK workaround — which otherwise need a wrapper
script around the desktop entry. It is applied before the Tauri builder exists,
which is the only moment early enough for WebKit to still read its own variables.
Names are logged, values never: this is exactly where somebody will put a path
they consider private.

Precedence, highest first:

1. a variable already exported in the environment,
2. the same variable set by the file's `[env]` table,
3. the file's own key (`server_url`, `poll_interval_secs`),
4. the built-in default.

The launch environment winning is deliberate. `SSL_CERT_FILE=… promview-desktop`
is how you test a bundle once, and a file that overwrote it would make that do
nothing, with no clue as to why — so the file says so on stderr when it yields.

A settings surface and multiple server profiles are still later work in the plan;
this is a file, not a UI.
