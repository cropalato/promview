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

## What does not work yet

**The console in the window cannot reach the API.** The Rust core can — the tray
counts are live — but the webview's own requests are cross-origin and the server
sends no CORS headers, so every fetch from the page is blocked.

That is a design decision, not an oversight, and it has two answers:

1. Add CORS to the Go server. Small, but it means the webview holds credentials
   and talks to the server directly, which is the posture the plan avoids.
2. Route the console's requests through the Rust core over Tauri's `invoke`,
   using the `fetchImpl` seam every client module already accepts. No CORS
   needed, and credentials stay out of the webview — which is what
   `docs/desktop-client-plan.md` asks for ("The Rust core owns … REST/SSE
   transport").

Also absent, all deliberately deferred: OIDC loopback PKCE, OS keychain storage,
background SSE in the Rust core, native notifications, and the updater.

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
