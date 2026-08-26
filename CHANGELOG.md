# Changelog

All notable changes to this project will be documented in this file.

The project uses [Conventional Commits](https://www.conventionalcommits.org/) and follows [Semantic Versioning](https://semver.org/). Entries are grouped by the conventional commit type that affects users or maintainers.

## [Unreleased]

### Added

- **chart:** an alert on the one failure promview cannot report itself. Reconciliation is what learns an alert ended while it was silenced; when the loop stops it emits no errors, and the only symptom is alerts that finished hours ago still sitting in the console — which nobody reads as a promview fault. The chart now ships `PromviewReconciliationStalled` as a `PrometheusRule`, gated on the operator CRD like the ServiceMonitor. It is the only rule shipped by default: thresholds for request errors or latency depend on what a deployment considers normal, and a chart guessing at them produces alerts that get silenced rather than fixed. `metrics.prometheusRule.additionalRules` appends to the same group.

## [0.1.0-alpha.32] - 2026-08-26

### Added

- **chart:** scraping configures itself. Where the Prometheus Operator's CRD exists the chart renders a `ServiceMonitor`; where it does not, it falls back to `prometheus.io/scrape` pod annotations. The two are mutually exclusive, so a pod is never scraped twice under two job names, and enabling one cannot leave a cluster with neither. The metrics port joins the Service so a ServiceMonitor can select it — the Ingress routes to the port named `http`, so another named port is not somewhere it can send traffic. Note that Prometheus selects ServiceMonitors by a label its own installation chooses; set `metrics.serviceMonitor.labels` to match, or the object is created and quietly ignored.
- **server:** promview reports on itself at `/metrics`. It is the console an operator opens when something else breaks, which makes its own failures the easiest to miss — a schema one migration behind turned every read into a 500, and the first person to notice was someone trying to silence an alert. What is exported is aimed at that: request outcomes by matched route, whether each source is still reconciling and when it last managed to, whether silences reach their Alertmanager, and whether their provenance was stored. Alert counts are deliberately absent; Prometheus already knows what is firing. The endpoint listens on `PROMVIEW_METRICS_ADDRESS` (default `:9090`), never on the public listener, and the chart keeps it off the Service and the Ingress — the labels name sources, and a port nothing publishes cannot leak them. See [`docs/metrics.md`](docs/metrics.md).
- **server:** the event stream and the database pool report their load. `docs/kubernetes.md` says to measure both before scaling past one replica, and until now there was no way to. Each connected console reads the event stream on a 500ms timer and re-authenticates every fifteen seconds, so one idle console costs roughly two queries a second and twenty left open cost forty before anybody has done anything. The new counters also give the ratio of events delivered to reads made — measured at 22 reads for 0 events with two idle clients — which is what would justify replacing the timer with `LISTEN`/`NOTIFY` rather than guessing at it.
- **server:** the binary knows its own version, stamped at build time and reported as `promview_build_info`. Confirming that a rollout actually landed previously meant reading the image tag off the Deployment.

## [0.1.0-alpha.31] - 2026-08-26

### Fixed

- **desktop:** the installers are named after the release they are. `tauri.conf.json` carried a fixed `0.1.0`, and that is what named every bundle, so alpha.29 and alpha.30 both shipped as `Promview_0.1.0_amd64.deb` and could only be told apart by download date. Only the Arch package escaped it, because that job derives its version separately. The release workflow now stamps the tag in rather than committing it, so no release needs a commit that only moves a number.
- **desktop:** the RPM carries a version RPM allows. A hyphen is illegal in the Version field — it separates name, version and release — and Tauri does not sanitize one, so stamping `0.1.0-alpha.30` there produced a package whose own metadata could not be parsed back. Version now holds `0.1.0` and Release holds `0.alpha.30`, the convention that also sorts a pre-release below the final version instead of above it. MSI takes a four-number product version derived the same way.

### Build System

- **release:** the desktop bundles can be built without a tag. CI compiles the Tauri crate but never packages it, and MSI and NSIS exist only on a Windows runner, so a packaging change was first exercised by the release that depended on it. A manual run now produces the installers and nothing else; publishing stays tag-only.

## [0.1.0-alpha.30] - 2026-08-25

### Fixed

- **server:** promview refuses to start when the database has migrations it has not applied, naming them. A binary newer than its schema does not degrade gracefully: the alert queries name columns that do not exist yet, so every read answers 500 and the console is simply down. Upgrading the image without running `promview migrate` produced exactly that, and nothing said so. Crash-looping with `unapplied: 000015_silence_provenance.up.sql` is strictly better than serving errors that name nothing.
- **api:** a 500 records its cause. Handlers took the error the store returned, answered with a generic message and threw the error away, so an operator had no way to tell a schema mismatch from a dead connection pool — the response is deliberately uninformative, and the log was too. The client still learns nothing it should not; the log now carries the method, the path and the error.
- **silence:** a console newer than its server can silence again. The group silence body gained `expectedMatchers`, and the endpoint's decoder rejects unknown fields, so a desktop client that had updated ahead of its server failed every silence with "request body is invalid". The server now advertises `silencePreviewSupported` and the console only asks for a scope preview, or sends the field, where that is present; otherwise it silences on the grouping key as it always did and says plainly that it could not confirm the exact match. It also no longer echoes back the grouping key when a preview failed, which the server could only ever disagree with — a guaranteed 409 on a matched pair.

## [0.1.0-alpha.29] - 2026-08-25

### Fixed

- **silence:** silencing a group now matches on every label its firing members agree on, not just the two or three keys they were grouped by. A group keyed on `alertname` alone used to write `alertname="HighCPU"` and hide that rule everywhere, for every cluster and team, including alerts nobody had seen yet. The match is resolved per Alertmanager rather than once per request: a group spanning two of them usually differs between them on exactly the label worth matching on, and a single shared match would drop it and silence both places. The confirmation dialog now asks the server what the silence would actually match before offering to write it, and echoes that match back on confirm so a member joining in between is refused rather than silently widening the scope.

### Added

- **silence:** promview re-reads a source right after writing a silence to it, instead of leaving the console to wait for the next reconcile pass. An operator who silenced an alert and saw it still listed as plainly firing for up to a minute had no way to tell a slow console from a silence that never landed. The re-read syncs suppression and nothing else: it carries no missing set, so it can never conclude an alert has ended, and it never touches the counters the ordinary pass uses to decide that. It is skipped entirely where reconciliation is disabled, since promview does not read suppression from anywhere in that configuration.
- **console:** silenced alerts are visible as silenced. Rows are dimmed and chipped rather than hidden, group rows say how many of their members are held back, and a segmented control switches between all alerts, unsilenced only, and silenced only — stored with the rest of the layout preferences. It defaults to showing them: an alert vanishing because somebody else silenced it is the failure silencing is meant to replace, not cause.
- **console:** the detail drawer says why an alert is not notifying. A silence Promview created names its author, expiry and comment; a silence made straight on the Alertmanager is reported as exactly that rather than given an invented author; and an inhibition is called an inhibition, because nobody chose it and it lifts itself when its parent alert clears.
- **api:** `POST /api/v1/groups/silence/preview` answers what a group silence would match, and `GET /api/v1/alerts` accepts `suppressed=true|false`. Alert payloads carry `silencedBy`, group payloads carry `silenced`, and the alert detail envelope carries a `silences` list.

## [0.1.0-alpha.28] - 2026-08-25

### Changed

- **console:** the group row's silence control is a round moon button rather than a text button. A crescent instead of the slashed bell the domain usually reaches for: that bell already belongs to the browser-notification toggle, and one glyph standing for both a local preference and a shared Alertmanager silence is worse than an unfamiliar one standing for a single thing. A moon also reads as quiet for a while, which is what a silence is — matched, time-bounded, expiring — where a mute reads as off for good. The hover text now names the group and says what silencing does, since the word is no longer written on the control, and it matches the name a screen reader announces.

### Build System

- **web:** name esbuild's install script as allowed. npm 12 blocks package install scripts unless a project names them, and esbuild has one — it links the platform binary vite compiles through. Blocked, `npm ci` still reports success and the failure surfaces later as a vite build that cannot find an esbuild binary, which does not name its cause. CI takes the npm bundled with Node 22 and never blocked; this is for developing on a newer one.

## [0.1.0-alpha.27] - 2026-08-25

### Fixed

- **desktop:** trust the operating system's certificate store. reqwest was built against `rustls-tls`, which compiles in the Mozilla root set and never consults the platform's own, so a server behind a private or corporate CA — the ordinary case for an internal console — failed the TLS handshake. It surfaced as `error sending request for url (...)`, which reads like the server is unreachable rather than untrusted, and no environment variable could correct it because the roots were baked into the binary. `rustls-tls-native-roots` keeps rustls and loads the machine's own roots, so a certificate the rest of the system already accepts is accepted here too; `SSL_CERT_FILE` and `SSL_CERT_DIR` now work for pointing at a bundle kept outside the system store.
- **desktop:** authenticate the tray's alert count read. The tray built a client of its own with neither the session token nor the cookie jar, so against a server that requires a session every poll came back 401 while the console — which goes through the proxy — listed the alerts perfectly well. The tooltip then reported that the server could not be reached, sending anyone who read it after the network instead of the sign-in they were missing. The tray now shares the proxy's client, and reads the token on each poll rather than capturing one at startup: it outlives signing in, so a token taken before there was one would never arrive.

## [0.1.0-alpha.26] - 2026-08-23

### Build System

- **ci:** build an Arch Linux package for the desktop client and attach it to the release. Tauri offers no pacman target, so the deb it already produces is repackaged by a PKGBUILD rather than compiled again — a second from-source build could only disagree with the binary shipped everywhere else. The tag becomes the `pkgver` with `-` replaced by `_`, which pacman reserves.

## [0.1.0-alpha.25] - 2026-08-23

### Fixed

- **ci:** attach the desktop installers correctly. The upload preserved each bundle's directory, so the release step handed `gh` a directory rather than a file and 0.1.0-alpha.24 published its images without a GitHub release. The step now collects files and is re-runnable, filling in a release left behind by a failed upload instead of colliding with it.

## [0.1.0-alpha.24] - 2026-08-23

### Build System

- **ci:** build the desktop client for Linux and Windows on a tag and attach the installers to a GitHub release — `.deb`, `.rpm`, `.msi`, and an NSIS `.exe`. Each platform builds its own, since a Tauri bundle cannot practically be cross-compiled. The installers are unsigned, which the release notes say plainly. AppImage is left out: `linuxdeploy` fails to produce one here, and a format nobody has seen succeed is not worth shipping.

### Fixed

- **desktop:** correct the paths in `beforeDevCommand` and `beforeBuildCommand`. They resolve from the Tauri project root rather than from the directory holding `tauri.conf.json`, unlike `frontendDist` beside them, so both pointed one level too high and any `tauri build` failed outright.

## [0.1.0-alpha.23] - 2026-08-23

### Features

- **desktop:** show native notifications. A webview may have no usable Notification API — WebKitGTK does not — so the host puts them on screen and the console's notifications appear at all. Only the showing moves: the opt-in, the label selector, and the dedupe ledger stay in the console, the same split the stream's reconnect policy has. Clicking a notification does nothing yet, since the host has no click callback to offer.
- **desktop:** sign in from the tray. The system browser handles the identity provider — a login form inside our own webview is the shape phishing takes — and a loopback listener receives a one-time code, which the core exchanges for a session. The session lives in the platform secret store, keyed by server URL, and is attached to requests and to the stream by the core; it never reaches the webview. Where no secret store is reachable the token is held in memory for the run rather than written to disk, and the operator is told, because a bearer token in a file is readable by anything running as the user.
- **auth:** let a client that cannot hold a cookie sign in. `GET /api/v1/auth/oidc/login?desktop_redirect=…` runs the same OIDC flow but ends by sending a one-time code to a loopback address instead of setting a cookie, and `POST /api/v1/auth/desktop/exchange` redeems that code for an ordinary session. The credential never travels in a URL: what does is single-use, expires in a minute, and is stored only as a hash. Only literal loopback addresses with a port are accepted as redirects — this is the open-redirect boundary of the flow, so a hostname that merely resolves locally, a URL carrying its own query, and anything not plain http are all refused. The browser flow is unchanged.

- **desktop:** drive the tray from the stream instead of a timer. It re-reads the counts whenever the stream reports a change, rather than applying events as deltas — the same choice the console makes, because deriving totals from a delta stream means tracking every alert's severity and state and being wrong in a way nobody notices until the number is. A burst settles for half a second first, so an alert storm costs one request rather than hundreds. `PROMVIEW_POLL_INTERVAL_SECS` is now the fallback for before a stream is open and while one is down, and defaults to 60 seconds rather than 15.
- **desktop:** hold the alert stream in the Rust core rather than the webview. The core keeps the SSE connection open and pushes each frame into the page, so the console reports `stream: live` and updates without a refresh — and the connection no longer dies with the window, which is what the tray needs and why the plan chose a shell over a progressive web app. Reconnect policy stays in the console, which already has a tested one; the core reports open, message and error and does as it is told. This also removes the last request the page was making cross-origin.

## [0.1.0-alpha.22] - 2026-08-22

### Features

- **desktop:** add a Tauri 2 shell in `desktop/`, wrapping the same React console the browser serves. A tray icon reports firing counts by severity, its menu opens the console or toggles a compact always-on-top window, and closing a window hides it rather than ending the process. `PROMVIEW_SERVER_URL` selects the server. This is the walking skeleton from the desktop plan, not its MVP: the live stream still uses the browser's `EventSource` and is blocked cross-origin, and OIDC, keychain storage, notifications and the updater are all still to come.

### Changed

- **console:** route API requests through the host when one is present. A shell embedding the console installs a transport, and every client module follows because they all default to it. This is what lets a local webview talk to a remote server without the server growing CORS, and it puts the cookie jar in the host where page script cannot read it. The page names a path and never a host, so it cannot redirect the host's credentials; the host forwards only headers that are the page's business. Nothing changes in a browser, which installs no transport and keeps its own fetch.

### Build System

- **ci:** drop the QEMU setup step from the release workflow. Releases are `linux/amd64` only, so there was no foreign architecture to emulate and the step did nothing but cost time. A comment now records that the single platform is deliberate.

## [0.1.0-alpha.21] - 2026-08-22

### Changed

- **console:** store notification preferences with the operator instead of in one browser. The opt-in and a new label selector live in `user_preferences` alongside columns, density and palette, so the policy follows an operator to whatever client they sign in from — which is what the planned desktop shell needs. The selector replaces the hardcoded critical-only rule and is edited in the view menu using the filter bar's own syntax; it matches on `severity`, `alertname`, `source`, and `team`, the fields a stream event actually carries, and the server refuses a selector naming anything else rather than letting it silently never fire. An empty selector notifies about nothing, never everything.
- **console:** the dedupe ledger stays in local storage. It records what this device already showed, which is not policy, and it writes on every qualifying event — a server round trip there would land on the hot path of an alert storm.

### Note

- The previous opt-in, stored as `promview.notifications.enabled` in local storage, is not migrated. Notifications were off by default, and the browser permission grant is unaffected, so re-enabling is one click with no prompt.

## [0.1.0-alpha.20] - 2026-08-22

### Changed

- **console:** resolve every API path against a configurable base URL, defaulting to the same-origin relative paths the browser build uses. `setApiBaseUrl` points the client at a server that is not its own origin, which is what the planned desktop shell needs — a local webview has no origin to be relative to. The base is validated on the way in: a relative one, or one carrying a query or fragment, is refused rather than silently misrouting requests.

## [0.1.0-alpha.19] - 2026-08-22

### Changed

- **console:** let a caller supply the fetch used for silence requests, matching the alerts, detail, session, and config clients. The desktop shell keeps credentials in its Rust core, out of the webview, so it cannot inherit the browser's cookie jar.

### Fixed

- **console:** stop offering the group silence control to readers who cannot use it. It was shown whenever the deployment could reach an Alertmanager, without checking the reader's own rights, so in open mode — where every reader is an anonymous viewer — clicking it could only ever return 403. The alert detail drawer was already gated correctly on the server's per-alert permission; group rows now check the session the same way.

## [0.1.0-alpha.18] - 2026-08-21

### Features

- **alerts:** silence an alert or a whole group on its Alertmanager. A single alert silences on its full label set, so only that series is affected; a group silences on its grouping key, and fans out to every Alertmanager its members span, reporting the outcome per target rather than as one result. Silences require operator rights, are attributed to the signed-in user, and always expire — the window defaults to two hours (`PROMVIEW_SILENCE_DEFAULT_DURATION`) and is capped at thirty days (`PROMVIEW_SILENCE_MAX_DURATION`).
- **sources:** add `--alertmanager-token`, an optional bearer credential used when writing silences. Reads stay unauthenticated; writes are the direction deployments usually protect.

### Removed

- **console:** remove the per-column filter button added in 0.1.0-alpha.17. Seeding an empty matcher and handing over the caret was more steps than typing the filter, and the alert detail drawer's label actions already start a filter from a value the operator can see.

## [0.1.0-alpha.17] - 2026-08-21

### Features

- **console:** add a filter button to each column row in the view menu. Columns that name an alert label — severity, alert (`alertname`), team, instance, and any label column — can start a filter on that label or drop one already applied.

### Fixed

- **console:** stop the status bar claiming `read-only` for every session; the top bar's role badge already reports what the operator may do.
- **console:** give the view menu popover a background again. It referenced `--surface`, which no theme defines, so the declaration was dropped and the menu rendered transparent over the alert table.

## [0.1.0-alpha.16] - 2026-08-21

### Features

- **console:** add a palette picker to the status bar with five new themes — Nord, Gruvbox, Solarized Light, High Contrast, and Colorblind Safe — alongside the existing dark and light ones. The choice is stored with the rest of a user's preferences, so it follows them between machines wherever there is a signed-in user; `system` remains the default and keeps following the operating system.

## [0.1.0-alpha.15] - 2026-08-20

### Features

- **groups:** support an explicit sort order for grouped alerts, binding the group cursor to the sort key, order, and value so a page token rejects a query it was not issued for. The default ordering remains severity then recency.
- **console:** add move-up and move-down column controls to the view menu.

## [0.1.0-alpha.14] - 2026-08-20

### Fixed

- **console:** use the available browser width for the live alert view instead of capping the console at 1440px.

## [0.1.0-alpha.13] - 2026-08-20

### Build System

- **ci:** cache amd64 image builds in the release workflow.

## [0.1.0-alpha.12] - 2026-08-19

### Changed

- **console:** collapse the group aggregate summary — severity mix, age, and acknowledgement ratio — into the group control column, removing the separate group summary column.

## [0.1.0-alpha.11] - 2026-08-19

### Changed

- **console:** render shared grouping values in their corresponding table columns and keep the group control focused on severity and member count.

## [0.1.0-alpha.10] - 2026-08-19

### Features

- **console:** allow users to customize alert grouping keys while preserving the default `alertname,source` grouping, and persist grouping and column-width preferences locally.
- **console:** add accessible resizable table columns, including keyboard resizing and reset, and use available desktop space for long alert fields.

### Fixed

- **console:** open details directly from single-alert groups, preserve expanded groups during live refreshes, and show single-member group columns from the underlying alert.
- **console:** restrict grouped summaries and expanded children to firing alerts so grouped counts match the severity strip and flat alert view.

## [0.1.0-alpha.9] - 2026-08-19

### Features

- **cli:** add `promview source update`, which changes a source's name, stale-after window or Alertmanager URL without touching its token. Previously the only way to add a URL was `source set`, which requires the token and rewrites it, so adjusting how a source is read meant handling the credential its deliveries authenticate with. Only the flags given are applied; an explicitly empty URL clears the setting.

## [0.1.0-alpha.8] - 2026-08-19

### Features

- **alerts:** reconcile against the source Alertmanager. Given a source's Alertmanager URL, promview reads `GET /api/v2/alerts` on a loop and confirms what is still firing, which is the only way to learn that an alert ended while silenced. An alert the Alertmanager no longer holds is recorded as resolved rather than expired, since the source is authoritative there. Configure per source with `promview source set --alertmanager-url`, and globally with `PROMVIEW_RECONCILE_INTERVAL` and `PROMVIEW_RECONCILE_TIMEOUT`; a source without a URL is left to expiry alone.
- **alerts:** track suppression as a flag rather than a status, so an alert silenced at the source is still reported as firing. The console shows both, which is the distinction an operator needs during a maintenance window.
- **console:** add a Last seen column, so an operator questioning an expired alert can see how long its source has been quiet instead of inferring it.

### Fixed

- **alerts:** never resolve alerts from an Alertmanager that reports none at all while promview holds firing ones. A restarting Alertmanager is indistinguishable from a fleet going quiet, and the consecutive-readings rule alone does not cover it: a restart easily outlasts two intervals, which would clear the console in one pass. Such a reading now syncs suppression only.

## [0.1.0-alpha.7] - 2026-08-19

### Features

- **console:** resolve table density from the area the console has rather than a single stored row height. The new `auto` density, now the default, tightens rows on a short viewport and relaxes them on a tall one, and re-resolves on resize so moving a window between screens needs no reload. An explicit choice still wins on every screen, and the view menu shows what `auto` currently resolves to. Layouts that already store a density keep it.
- **console:** collapse optional columns against the table panel's own width using a container query instead of the window's width, so a console in a split view or dashboard tile behaves the same as a narrow window.

### Fixed

- **web:** resolve every pending alerts request in the loading-state test, which could otherwise strand the request the console was waiting on and leave it loading.

## [0.1.0-alpha.6] - 2026-08-18

### Features

- **alerts:** expire alerts whose source stops reporting them without a resolved notification, using a per-source window that must exceed that Alertmanager's repeat interval, with an optional per-alert `timeout` label. Expired is a state of its own: the source went quiet, which is a weaker claim than resolved.
- **alerts:** summarise alerts into groups in the store, ordered by worst severity then recency, with counts computed under the caller's own read restrictions so a group never reports members the caller cannot open.
- **api:** serve grouped alerts from `GET /api/v1/alerts` via `groupBy`; without it the response is unchanged. Expanding a group is the ordinary alerts query with a matcher, so cursors, sorting and access rules are identical inside a group.
- **console:** collapse alert fan-out into expandable groups, so one rule firing once per offending series no longer buries the rest of the page. A group of one renders as a plain row.
- **console:** store column, density and grouping preferences per user so a layout follows an operator between machines; deployments without a signed-in user keep them in the browser instead.
- **console:** bind a table column to any alert label, which surfaces a dimension the built-in columns do not cover without overloading a label that already means something else.
- **helm:** configure role bindings from chart values.

### Fixed

- **web:** unmount components before removing global test stubs, which was surfacing failures against unrelated tests.
- **ci:** initialize the migration ledger after checks and finalize the migration checker state.

### Build System

- **database:** add migrations for alert expiry, alert grouping lookups, and user preferences.

## [0.1.0-alpha.5] - 2026-08-17

### Features

- **web:** add server-backed positive and negative label filtering, sortable alert columns, and label-to-filter actions from alert details.

### Fixed

- **web:** apply Prometheus-style filter expressions to the full authorized alert result instead of treating them as literal text over loaded rows.

## [0.1.0-alpha.4] - 2026-08-17

### Features

- **web:** add opt-in browser notifications for newly created critical alerts while Promview is open.
- **auth:** persist OIDC identities and enforce database-backed role and label-selector bindings across alert queries and streams.
- **alerts:** let authorized operators acknowledge and unacknowledge alerts with occurrence-aware state, history, and live updates.
- **auth:** add `promview access inspect` for privileged OIDC identity, group, and binding diagnostics without exposing provider tokens or sessions.

### Changed

- Remove the unfinished LDAP mode and require explicit server-owned OIDC role bindings.
- Invalidate existing alpha sessions when migrating to database-authoritative authorization.

### Build System

- **helm:** add a hardened Kubernetes chart with migration hooks, external Secret integration, OIDC, Ingress, and health tests.
- **release:** publish multi-architecture images to GHCR and Docker Hub, and publish the Helm chart to GHCR from version tags.

### Documentation

- Add Kubernetes installation, upgrade, rollback, OIDC, and production-operation guidance.
- Add OIDC role-binding and label-selector administration guidance.

## [0.1.0-alpha.1] - 2026-08-15

### Features

- **api:** ingest and normalize authenticated Prometheus Alertmanager webhooks from multiple sources.
- **auth:** provision independent Alertmanager source credentials, store only token hashes, and track source deliveries.
- **auth:** protect query and stream APIs with open-mode or opaque-session authentication and expose the current principal.
- **auth:** add OIDC discovery and Authorization Code sign-in with PKCE, replay protection, validated ID tokens, group-to-role mapping, and logout.
- **api:** expose filtered cursor pagination, severity counts, alert details, raw payloads, and immutable occurrence history.
- **stream:** publish durable, resumable server-sent events for created, changed, resolved, and reopened alerts.
- **web:** provide a responsive live alert console with filtering, pagination, connection state, deep-linked details, lifecycle timeline, and raw payload views.
- **web:** gate protected alerts and streams on OIDC identity, expose sign-in and logout, and recover when sessions expire.

### Build System

- **container:** build a non-root multi-stage application image and Docker Compose development stack.
- **database:** apply ordered PostgreSQL migrations before application startup and validate up/down migration paths.
- **ci:** run backend, frontend, migration, Compose, and container verification in GitHub Actions.

### Documentation

- Document the Alerta reference investigation, Promview architecture, implementation roadmap, desktop client direction, and developer workflows.
- Add an Okta-specific OIDC application, claims, role-mapping, deployment, and troubleshooting guide.
- Add a Prometheus and Alertmanager configuration procedure for authenticated Promview webhook delivery.
