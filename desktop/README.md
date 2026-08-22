# Promview desktop shell

A Tauri 2 shell around the same React console the browser serves. Not a separate
product and not a native rewrite: the frontend is `../web`, unchanged, pointed at
a configured server instead of at its own origin.

This is the walking skeleton from `docs/desktop-client-plan.md`, not the MVP.

## What works

- Builds and runs on Linux, loading the console in a window.
- A tray icon with a menu: open the console, toggle a compact always-on-top
  window, quit. Closing a window hides it; the tray owns the process lifetime.
- The tray tooltip shows firing counts by severity, polled by the Rust core.
- `PROMVIEW_SERVER_URL` selects the server, validated on the way in.

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

## What does not work yet

**The live stream.** SSE still uses the browser's `EventSource` directly, which
is cross-origin and blocked, so the console shows `stream: reconnecting` and
relies on its own refreshes. Moving the stream into the Rust core is the next
increment and the reason the plan chose Tauri over a PWA — a tray that keeps
working with every window closed needs the stream outside the webview anyway.

Also absent, all deliberately deferred: OIDC loopback PKCE, OS keychain storage,
native notifications, and the updater.

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

| Variable                      | Default                 | Meaning                                                                                                                                          |
| ----------------------------- | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `PROMVIEW_SERVER_URL`         | `http://localhost:8080` | Server to talk to. Must be absolute http/https, may carry a path prefix, must not carry a query or fragment.                                     |
| `PROMVIEW_POLL_INTERVAL_SECS` | `15`                    | Tray refresh cadence. Minimum 5, because a tray badge a few seconds stale costs nothing and a tight loop against a shared server costs everyone. |

Environment variables are the walking skeleton's answer. A settings surface and
multiple server profiles are later work in the plan.
