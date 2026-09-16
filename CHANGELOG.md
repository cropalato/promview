# Changelog

All notable changes to this project will be documented in this file.

The project uses [Conventional Commits](https://www.conventionalcommits.org/) and follows [Semantic Versioning](https://semver.org/). Entries are grouped by the conventional commit type that affects users or maintainers.

## [Unreleased]

### Documentation

- Documentation that had fallen behind the code is brought up to date: the metrics reference gained `promview_stream_gaps_total` and `promview_stream_events_pruned_total`, the authorization guide stopped describing the operator role as acknowledge-only when it now covers assign, close, note and silence, the Helm chart gained a `stream.retention` value so the retention window is configurable on Kubernetes at all, and the Docker Hub listing's environment table gained `PROMVIEW_STREAM_RETENTION`. The project plan no longer lists assignment, close and notes as planned work, and says plainly what is left: the console can display an assignee and a note count but has no controls to use any of the three, and does not act on `stream.gap`.

### Build System

- `make docs-check` fails when an installation example names a release older than the current one, and runs in CI. This had drifted four separate times — the Docker Hub listing shipped a version 33 releases old, the README's Helm example six, the Kubernetes guide nine — and each was a reader following an instruction that installed something other than what the page described. Image tags in examples now follow the moving `alpha` pointer, which cannot go stale; chart versions have to be exact, so those are what the check watches. Prose naming a version historically is deliberately not checked: "carried in values since 0.1.0-alpha.35" is a fact about the past and rewriting it would be wrong.

## [0.1.0-alpha.39] - 2026-09-16

### Added

- **alerts:** an operator can file an alert as handled. `POST /api/v1/alerts/{id}/close` with `{"closed":true}` closes it and `false` reopens it. Closing is Promview-local and never reaches Alertmanager — it is not silencing, and the source keeps reporting the alert exactly as before. It is a flag rather than a fourth `status` for that reason: `firing`, `resolved` and `expired` are claims about what the source reports, `closed` is a claim about what somebody decided, and an alert can honestly be both still firing and already dealt with. Closed alerts leave the default list, since closing an alert that stayed in it would be an action with no visible effect; `?closed=true` finds them again. Besides an operator reopening it by hand, a delivery that materially changes the alert reopens it — new labels, a new annotation, a status transition — because that is not the alert that was closed. An identical repeat deliberately does not: those arrive every `repeat_interval` and carry no new information, so reopening on one would mean a close never outlived the next notification.
- **alerts:** an operator can leave a note on an alert — what was checked, what was ruled out, who was called. `POST /api/v1/alerts/{id}/notes` appends one; notes appear on the alert detail oldest first, which is the order a handover is read in. They are deliberately append-only, with no endpoint that edits or deletes one: a note is what somebody relied on at the time, and a handover that can be quietly rewritten afterwards is worth less than none. Each records the occurrence it was written against, so a note about a previous incident stays attributable instead of reading as though it describes the current one — and unlike the assignment and the acknowledgement, notes survive an alert resolving and firing again. They live in their own table rather than in alert history, because every history entry is written by promview about an event while a note is written by a person about a judgement, and flattening the two would bury the sentence somebody typed among a hundred generated ones. The list payload carries a `notes` count rather than the notes themselves, filling the console column that has been waiting for it; the list's job is to show there is something to read, and opening the alert is what reads it.
- **alerts:** an operator can record who owns an alert. `PUT /api/v1/alerts/{id}/assignee` sets it and an empty assignee clears it, which is why it is a PUT rather than two verbs: the request states the assignment in full, and sending it twice leaves the same owner. The assignee is free text rather than a reference to a Promview user, because an alert is routinely handed to somebody who has never signed in — a vendor, a team rota address, the name in a runbook — and a foreign key would turn every one of those into an error instead of an assignment. The operator who decided is recorded separately as `assignedBy`, since "who owns this" and "who said so" are different questions and the second is the accountability trail. `assignee` is on the list payload as well as the detail, because "what is on my plate" is asked of the list; the console already had a column waiting for it and now fills it. Assigning to the existing owner writes nothing and wakes no console. Assignment is cleared when a resolved alert fires again, alongside the acknowledgement: that is a new occurrence, and the previous owner never agreed to own it.

## [0.1.0-alpha.38] - 2026-09-16

### Added

- **stream:** stream events are now deleted once they pass `PROMVIEW_STREAM_RETENTION`, default 24h, `0` to keep everything. They exist only so a client that lost its connection can resume, which makes almost all of them dead weight within minutes, and nothing had ever removed one. A day covers the disconnections a resume is actually for — a closed laptop, a rolling deploy, a proxy that dropped every connection at once — and past that a fresh snapshot is cheaper than replaying history. The sweep shares the expiry sweep's ticker rather than adding a knob: a retention window is measured in hours and the interval enforcing it in minutes, so the exact interval never mattered. `promview_stream_events_pruned_total` counts what it removes.
- **stream:** a client resuming from a cursor that retention has deleted is now told so, with a `stream.gap` event carrying its own cursor and the oldest point the stream can serve. This is the half that made deletion safe to ship at all: the events between are gone, and handing back only the survivors would have left a console reconnected, reporting no error, and quietly wrong about which alerts are firing — worse than the unbounded growth being fixed. The correct response is a fresh snapshot, resumed from its `streamCursor`. A client at or past the watermark has missed nothing and is never interrupted, so a caught-up console is not asked to discard a view that is still correct. The watermark is a stored column rather than `min(id)`, because the table being emptied completely is exactly when the question matters and exactly when `min(id)` has no answer. `promview_stream_gaps_total` counts them, and a rising count means the window is shorter than the disconnections a deployment actually sees.

## [0.1.0-alpha.37] - 2026-09-16

### Added

- **packaging:** the published image now carries a moving `alpha` tag pointing at the newest pre-release, so trying Promview no longer starts with finding out which alpha is current. It is applied only to tags carrying a hyphen, which is exactly the set the metadata action's own `latest=auto` declines to move `latest` for: the two pointers can never name the same image, and neither claims to be something it is not. `latest` still appears by itself at the first release without a pre-release suffix, and there has not been one.
- **packaging:** the Docker Hub listing is now generated from `docs/dockerhub.md` and pushed by its own workflow whenever that file lands on `main`. The listing was the one published artifact nothing in this repository owned, and it had drifted accordingly: it told readers to `docker pull YOUR_DOCKERHUB_USERNAME/promview:0.1.0-alpha.3`, pinned a Helm chart 33 releases old, and promised `linux/arm64` images that have not been built since alpha.12 — so anyone on Apple Silicon or arm64 nodes read a published promise and got an exec format error. Sourcing it from the tree makes a wrong claim there an ordinary diff. It is deliberately not part of the release workflow: a documentation fix should not wait for the next tag, and failing to update prose should not paint a release red. It authenticates with the same `DOCKERHUB_TOKEN` the release job pushes images with, and skips when that secret is unset rather than failing a fork that has no credential.
- **packaging:** the desktop client can be published to the AUR as `promview-desktop-bin`, so Arch users can install and update it with a helper instead of downloading an installer from each release. Publishing is held behind the `AUR_PUBLISH` repository variable and is off by default: it needs a maintainer account, and the AUR closed new registrations while this was written. Until that variable is set the job is skipped, so a release neither publishes nor fails on it. The AUR distributes recipes rather than packages, so this is a second PKGBUILD rather than a reuse of the one `desktop-arch` already builds: that one repackages a deb sitting beside it in a CI workspace, which the AUR rejects twice over — a source entry without a URL has to be committed to the package repository, and the repository refuses any blob past a quarter of a megabyte. The published recipe names the release asset by URL and pins its digest, which is safe because uploaded assets are byte-stable, unlike the source tarballs GitHub generates. It also installs the license, which the existing package never did: its `install` line was guarded with `|| true` and pointed at a file that was never among its sources, so it silently shipped nothing. Publishing runs after the release job rather than beside it, since the recipe downloads an asset that job uploads.

### Fixed

- **alerts:** reconciliation now revives an alert expiry retired while its Alertmanager was still holding it. Expiry infers an ending from a source going quiet, and a silenced alert is quiet by design — Alertmanager sends no notifications for one — so a maintenance window would retire alerts that were demonstrably still firing, and nothing brought them back. Measured against production on 2026-08-19, that was 30 alerts hidden from the console while live and suppressed on the source. Reconciliation now reads a source's `expired` alerts alongside its firing ones and returns to `firing` any the live view still carries, clearing the `ends_at` expiry invented. The alert keeps its occurrence and its acknowledgement: a new occurrence is what follows a `resolved` alert, where the source itself said it ended, and nothing ended here — ingestion has always treated a webhook for an expired alert as an ordinary update, and reconciliation now agrees with it. `resolved` alerts are not examined, and an Alertmanager reporting nothing revives nothing. The revival streams `alert.updated` rather than `alert.created`, because the console raises a desktop notification for a newly created critical and a deployment correcting thirty wrong expiries should not announce thirty new alerts.
- **alerts:** an alert its Alertmanager still holds is no longer expired in the first place. Reviving alone would have cycled — the silence that caused the wrong expiry is usually still in force, so the alert would be retired again one window later and revived again indefinitely — so reconciliation records `reconciled_at` on every alert it confirms live, and expiry now measures staleness from the later of that and `last_seen`. This is a new column rather than a wider reading of `last_seen`, which means one specific thing, when a webhook last delivered the alert, and is also the console's default sort key and its pagination cursor: restamping it every pass would reshuffle the table under a reader and carry rows across cursor boundaries. Expiry is unchanged where it is still the only signal, since a source with no Alertmanager URL or one that cannot be reached has no `reconciled_at` moving. The expired half of the reconciliation query is restricted to fingerprints the live reading actually carries: expired alerts accumulate, nothing retires them, and selecting all of them would make every pass scan a set that only ever grows in order to decide almost every time that there is nothing to do.
- **security:** three dependency advisories, all reachable from code this project runs. `github.com/jackc/pgx` 5.7.6 carried a SQL injection through placeholder confusion with dollar-quoted string literals (GO-2026-5004), reachable from the migration runner; `github.com/go-jose/go-jose` 4.0.5 panics on certain JWE decryption (GO-2026-4945), reachable from OIDC ID token verification, where a panic in the authentication path is a way to take the server down without credentials; and `rustls` 0.23.43 in the desktop client accepted TLS 1.3 handshake messages across encryption level boundaries (RUSTSEC-2026-0285), published two days before it was found here. Upgraded to 5.9.2, 4.1.4 and 0.23.45. Nothing in the repository was scanning for any of this, which is the more useful half of the finding; see the `vuln` job below.

### Build System

- `make changelog-check` verifies that every released version in the changelog still extracts as release notes, and runs as its own CI job. The extraction script otherwise runs once per release and nowhere else, which is the trap the release workflow's comments already describe: the first time a change to it is exercised would be the release that depends on it. It checks the changelog end too — a heading that drifts from the `## [version] - date` shape, or a version whose section is empty, is a release that would publish notes saying nothing. All 34 released versions pass.
- Release notes are now the changelog entry for the version being tagged, followed by the image and chart references and the unsigned-installer warning that were previously the whole of them. Every release said the same three sentences about packaging and nothing about what had changed, while the changelog said it all in the one place a reader of a release never looks. `scripts/changelog-section.sh` extracts one version's section, and the release job checks out the tree it never used to need. A tag whose version has no changelog entry is annotated as a warning rather than failing the job: thin notes are not a reason to withhold the binaries.
- Advisory scanning now runs on every change as the `vuln` job, covering all four toolchains: `govulncheck` for Go, `npm audit --omit=dev` for both npm projects, and `cargo audit` for the Tauri crate. `--omit=dev` because the question is what ships — a vitest advisory is worth knowing and is not a vulnerability in the console anyone runs. Scanning is `make vuln` locally and is deliberately not part of `make verify`: it reads a database over the network, and an advisory published this morning should not fail a local run of work that has nothing to do with it. In CI, going red is exactly the point.
- Go tests now also run under the race detector, as `make test-race` and its own CI job. The server fans one ingestion out to every open console over SSE and runs the expiry and reconcile loops beside it, so its concurrency is not incidental and was never being checked. It is a separate job so a data race reports as a data race rather than as "the Go job failed", and out of `make verify` because the race build is several times slower. All 237 tests pass under it.
- CodeQL analyses Go and TypeScript on every change and again weekly, with the `security-and-quality` queries. The weekly run matters as much as the per-change one: queries improve after code is written, so the same commit is worth re-scanning later. Rust is left to clippy and `cargo audit`.
- CI ran every job twice for any branch with a pull request open on it, because `push` carried no branch filter alongside `pull_request`. Pushes are now watched on `main` only, where commits land in this project, and everything else arrives as a pull request. A concurrency group cancels superseded runs on every ref except `main`, where each commit is a state someone may later bisect to and deserves its own answer.
- Dependabot now watches all five dependency manifests, none of which had anything watching them: the Go module, both npm projects, the Tauri crate, the Dockerfile's base images, and the workflows' own action versions. Minor and patch updates are grouped into one pull request per ecosystem, because a project this size cannot absorb one pull request per dependency and a queue nobody reads updates nothing. Major versions stay ungrouped, since those are the ones worth reading. GitHub's vulnerability alerts, Dependabot security updates, and private vulnerability reporting are enabled alongside it — the last is what `SECURITY.md` sends reporters to.

### Documentation

- The project has community health files for the first time: `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, issue templates for bugs and features, and a pull request template. `CONTRIBUTING.md` puts the workflow that was only in `AGENTS.md` somewhere an outside contributor will find it, including the two traps that bite everyone — `go test ./...` discovering Go files under `web/node_modules`, and the rule that a check which does not run in CI stops running. The bug template asks for the version, the deployment shape and the auth mode up front, since a report missing those costs a round trip before it can be read. `SECURITY.md` states the scope in terms of what Promview actually guards — scope enforced in SQL, hashed per-source ingestion tokens, the OIDC and desktop loopback flows, the one Alertmanager write path — and separates that from four documented behaviors that are not vulnerabilities, including `alertmanager_token` being stored unhashed because it must be replayed on every request.
- The README opens with a screenshot of the console. A monitoring tool asks a visitor to imagine a dense alert table from prose, and no amount of prose competes with the picture. The capture is synthetic throughout — invented clusters, nodes and services across two Alertmanager sources — rather than a real deployment, so no internal hostname, team or alert name is published to make a point about layout.
- The README is organized for someone deciding whether to run Promview rather than for someone already working on it. It opened with a Go and Node toolchain list and a `make verify` walkthrough, so a reader wanting to know what this is had to scroll past the contributor workflow to reach `docker compose up`; those sections now sit under **Development** at the end. Added: build and release badges, a **Why Promview** section stating the narrow claim (it stores state, spans several Alertmanagers, keeps labels verbatim, enforces scope in SQL, ships as one binary) and what it explicitly is not, a **Project Status** table separating what works from what is planned, the known reconciliation gap stated in the README rather than only in the plan, a documentation index, and a contents list.
- Four subjects were filed under **OIDC Authentication** that had nothing to do with authentication — list pagination and label matchers, acknowledgement, the SSE stream, and resetting the development database — because they had been appended to the end of the file as it grew. They now have sections of their own. The stale browser-notification sentence describing critical-only alerts was dropped: notification policy has been an opt-in selector since the preferences work, which the Console Preferences section already describes.
- The Helm example pinned `0.1.0-alpha.30`, six releases behind, and is now `0.1.0-alpha.36`.
- The Docker Hub listing now documents what the image actually is: amd64-only and why, a Compose stack that pulls the published image rather than building from a checkout nobody pulling an image has, the full environment variable surface with its real defaults, both listening ports, and the alpha status with the advice to pin an exact tag. The previous text described none of it.

## [0.1.0-alpha.36] - 2026-09-15

### Added

- **silence:** a silence can be lifted from the console. `DELETE /api/v1/alerts/{id}/silences/{silenceId}` expires it on the source's Alertmanager, and the detail drawer grows a **Remove** control beside each silence holding the alert back. Removal is addressed through the alert rather than by silence id alone, and that is the authorization: a silence id is an opaque token, so a bare id endpoint would let anyone who may operate on anything un-hide anything. The server re-checks in SQL that the id is one currently suppressing an alert this operator may act on — the same per-alert permission the silence button reads, since creating and lifting a silence are one right. The removal then triggers the same re-read a new silence does, so the suppression releases and the row un-dims within seconds rather than at the next reconcile tick, and the stored record is marked expired immediately so the drawer stops calling it live even where reconciliation is switched off. The control is offered only where the server advertises `silenceRemoveSupported`, so a newer console against an older server does not offer a button whose request would fall through to the SPA route. Outcomes are counted in `promview_silence_removals_total`. A preventive silence matching no alert has no row to act from and is not reachable this way; that needs a silences view of its own.
- **alerts:** reconciliation now reads each source's silences (`GET /api/v2/silences`) alongside its alerts. The alert list cannot report a silence ending for an alert it no longer carries — an alert that cleared inside a maintenance window takes the evidence of its silence with it, and the console kept showing both the SILENCED chip and the alert until the expiry sweep hours later. The silence listing closes that: an alert whose silences have all vanished from the active set has its suppression released and streams an update, so every open console clears the chip without anyone reaching for refresh. The listing is deliberately read even when the alert list is untrusted (an Alertmanager reporting no alerts at all), because Alertmanager persists silences across restarts and alerts not at all — an empty silence listing is a real answer where an empty alert list is usually a restart. A failed listing degrades the pass to what it did before and shows up as `silences-unreadable` in `promview_reconcile_runs_total`. An alert suppressed with no silence ids is inhibited, and the listing says nothing about it.
- **alerts:** the silence table is now the synced inventory of what each Alertmanager holds, not only promview's own diary. Every listed silence is stored, matched to alerts or not — a preventive silence written ahead of a maintenance window matches nothing yet, one whose alerts already cleared matches nothing anymore, and both are real — so a silence made straight on the Alertmanager now explains a dimmed row with its actual author instead of "created outside Promview". Matchers are kept verbatim in a new full-fidelity list (`matcherList` on the record payload), including the regex and negation forms promview itself never writes; the legacy equality map keeps only what it can say honestly rather than flattening a regex into an equality it does not mean. Records carry the live `state` (active, pending, expired), and a silence deleted on the Alertmanager has its recorded end brought forward so it stops claiming it ran its full course.

### Fixed

- **desktop:** signing in from the console's own gate now opens the system browser instead of the identity provider's login form inside the webview. The gate's control was a plain link, so it navigated the whole shell to the server's OIDC endpoint: the operator lost the address bar the flow is meant to be checked in, and the session ended up as a cookie in the webview rather than in the platform secret store, where the tray and the API proxy could not reach it. It now runs the same loopback flow the tray menu has always used, says that it is waiting on the browser, and reports a refusal instead of appearing to do nothing. A plain browser keeps the navigation it always had.
- **desktop:** the console picks up a sign-in without being told to refresh. Nothing announced a session change to the webview, so an operator who signed in — from the gate, or from the tray while the window sat on it — kept looking at "Sign in required" until they reloaded by hand. The shell now announces sign-in and sign-out to every open window, including the compact one, and the console re-checks its session and loads alerts on its own; a sign-out drops it back to the gate the same way an expired session does.

### Documentation

- The Kubernetes guide claimed a source's Alertmanager URL could only be set per source through the CLI; the chart's `sources` list has carried it in values since 0.1.0-alpha.35. The guide now shows the declarative path first and keeps `source update` for sources managed outside chart values.

## [0.1.0-alpha.35] - 2026-09-09

### Added

- **chart:** `sources` declares any number of Alertmanager sources in values, one post-install and post-upgrade Job per entry running `promview source set`. `bootstrapSource` stays what it was — one source, written only while the row has no credential — which made it safe to re-run but also made it a dead end: it cannot register a second source and it cannot rotate a token. The Jobs overwrite the stored ingestion token on every upgrade, so rotating the Secret and syncing rotates the credential. The token never appears in a manifest: the kubelet expands it into the argument list from the Secret-backed environment. `name` and `tokenKey` default to the slug so one Secret carries every source, and `staleAfter`, `alertmanagerURL`, and `alertmanagerTokenKey` are passed only when set, because `source set` keeps the stored value for a flag it is not given.

### Documentation

- Document the desktop client's config file in the project README: where it is read from, the `[env]` table that exports variables before the webview starts, the per-machine notification rules and how they relate to the server-side policy in `user_preferences`, and the WebKitGTK renderer override.

## [0.1.0-alpha.34] - 2026-08-27

### Added

- **desktop:** the shell decides for itself whether WebKitGTK may use its DMA-BUF renderer. Since WebKitGTK 2.42 that renderer is the default, and on the NVIDIA driver its GBM allocations fail — the window renders nothing at all, `Failed to create GBM buffer` goes to stderr, and the symptom is indistinguishable from promview being broken, so every affected operator had to find `WEBKIT_DISABLE_DMABUF_RENDERER` for themselves. At startup the shell now probes for a DRM render node and the driver behind it, and switches the renderer off where there is nothing to allocate from — a container, a VM with no GPU — or where the node is NVIDIA's. The guess is biased on purpose: disabling the renderer where it would have worked costs shared-memory buffers and some CPU, which for an alert console nobody notices, while leaving it on where it does not work costs the whole window. It says which way it went and why. `webkit_dmabuf = "on" | "off"` in the config file overrides the probe, `WEBKIT_DISABLE_DMABUF_RENDERER` in the environment beats both, and nothing ever removes a variable an operator exported.
- **desktop:** the shell reads an optional config file, so the settings that used to need a wrapper script around the desktop entry survive a reboot on their own. TOML, found at `~/.config/promview-desktop/config.toml` or one of the `$HOME` fallbacks, named outright by `PROMVIEW_DESKTOP_CONFIG`, and refusing keys it does not know — a settings file whose typos pass is a file you believe is in effect when it is not. It carries the server and the poll interval, and an `[env]` table for the variables that are not this application's own: `SSL_CERT_FILE` for a bundle outside the system trust store, `WEBKIT_DISABLE_DMABUF_RENDERER` for the GPU and compositor combinations where WebKitGTK renders a blank window. The table is applied before the Tauri builder exists, which is the only moment early enough for WebKit to still read its own variables, and a variable already exported wins — `SSL_CERT_FILE=… promview-desktop` is how you test a bundle once, and a file that overwrote it would make that do nothing. Names are logged, values never. See [`desktop/config.example.toml`](desktop/config.example.toml).
- **desktop:** notifications can be narrowed per machine. Whether an alert deserves a page still belongs to the console — the opt-in, the label selector and the dedupe ledger stay there, so policy follows an operator between clients — but one thing about it is genuinely per-machine and cannot live in a selector every client shares: a laptop that should only ever buzz for its owner's team. `[[notifications.rules]]` is that and only that. Rules are ORed, fields within a rule ANDed, values are unanchored regular expressions over the fields a stream event carries (`severity`, `alertname`, `source`, `team`, `summary`; arbitrary alert labels are not among them, because the server denormalizes only these into the stream record). No rules means no filtering, never "match nothing". A bad pattern or a field no event carries is refused at startup rather than at the first alert, a suppressed notification is logged because "the filter ate it" and "the daemon is down" are otherwise the same silence, and the tray's **Reload alert filter** re-reads the rules so one can be tried against live alerts without relaunching.

### Fixed

- **desktop:** signing in no longer hangs forever against a locked keyring. An absent secret store was already handled — the token stays in memory for the run and the operator is told — but a _locked_ one is not absent: the D-Bus unlock call blocks until something prompts for the password, and on a session with no prompter running nothing ever does, so the call never returns. What that produced was a sign-in that never finished and nothing on screen to explain it, on a machine where every other part of the client worked. Every call into the store now has a three-second deadline and falls back to the in-memory path, and a store that misses it is not consulted again for the rest of the run, because paying the deadline on each request would make the whole client feel broken rather than just the part that remembers a session. A store that was merely slow because it raised a prompt may still answer afterwards and store the token, so the message an operator gets is pessimistic rather than wrong.
- **desktop:** a notification that never appeared no longer reports success. `tauri-plugin-notification` shows on a spawned task and drops the result, so every failure there is — no notification daemon, notifications switched off for the application, an AppUserModelID Windows does not recognise — arrived as `Ok(())`, and the console's own error handling could never fire. That is the one failure mode nobody notices: the operator learns of it by missing a page. The shell now calls notify-rust directly on the blocking pool, waits for the outcome, logs it on stderr and returns it to the console, which reports it too. The plugin's per-platform identifier handling is kept, so an installed Windows build still attributes its toast to Promview.

## [0.1.0-alpha.33] - 2026-08-26

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
