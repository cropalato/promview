# Promview

[![CI](https://github.com/cropalato/promview/actions/workflows/ci.yml/badge.svg)](https://github.com/cropalato/promview/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/cropalato/promview?include_prereleases&sort=semver)](https://github.com/cropalato/promview/releases)
[![Docker pulls](https://img.shields.io/docker/pulls/cropalato/promview)](https://hub.docker.com/r/cropalato/promview)
[![License](https://img.shields.io/github/license/cropalato/promview)](LICENSE)

**An operational console for the alerts Prometheus Alertmanager is already sending you.**

Point one or more Alertmanagers at Promview as a webhook receiver. It keeps
current alert state in PostgreSQL, serves a live console over REST and resumable
server-sent events, and lets an on-call operator filter, group, acknowledge and
silence what is firing — in a browser, or in a desktop tray client sharing the
same UI. Alertmanager keeps routing, grouping, inhibition and notification.

![The Promview console showing fifteen firing alerts from two Alertmanager sources, sorted by severity with four criticals at the top, above columns for state, summary, team, instance, age and source](docs/images/console.png)

> [!IMPORTANT]
> Promview is **beta**. Every feature on the first-release list is implemented
> and the committed scale is measured, but it has not been run in anger by
> anyone but its author. Pin an exact version; do not track a moving tag in
> production. See [Project Status](#project-status).

## Contents

- [Why Promview](#why-promview)
- [Quick Start](#quick-start)
- [Run On Kubernetes](#run-on-kubernetes)
- [Filtering, Sorting, And Grouping](#filtering-sorting-and-grouping)
- [Live Updates](#live-updates)
- [Acknowledgement](#acknowledgement)
- [Assignment](#assignment)
- [Notes](#notes)
- [Close](#close)
- [Bulk Actions](#bulk-actions)
- [Alert Expiry](#alert-expiry)
- [Alertmanager Reconciliation](#alertmanager-reconciliation)
- [Silences](#silences)
- [Console Preferences](#console-preferences)
- [Desktop Client](#desktop-client)
- [OIDC Authentication](#oidc-authentication)
- [Project Status](#project-status)
- [Documentation](#documentation)
- [Development](#development)
- [License](#license)

## Why Promview

Alertmanager is very good at deciding who to wake up. It is not where an on-call
engineer wants to spend a shift: its own UI shows what is firing right now, with
no memory of what fired an hour ago and no record of who looked at it.

Promview is the layer above that, and it is deliberately narrow:

- **It stores state.** A dashboard that reads the live Alertmanager API can only
  show the present. Promview keeps current alerts, occurrences, and a lifecycle
  history, so "when did this start" and "who acknowledged it" have answers.
- **It spans Alertmanagers.** Several installations feed one console, each with
  its own credential, and identity is `source + fingerprint` so two independent
  fleets cannot collide.
- **It keeps your labels.** Every label and annotation is preserved as sent.
  Nothing is folded into a fixed schema of resources, events and tags, so the
  filter bar speaks the same vocabulary as your alerting rules.
- **Authorization is a label selector.** A role is combined with Prometheus
  label matchers and enforced in SQL, so a team-scoped viewer cannot list,
  count, stream, or open an alert outside its scope. Scope is not a UI filter.
- **It is one binary.** A Go server with the compiled React console embedded,
  plus a PostgreSQL you already run. No nginx, no process supervisor, no plugin
  runtime.

It is **not** a replacement for Alertmanager, and it does not poll Prometheus.
For the reference investigation this design came out of — what Alerta does, what
is worth reusing and what is not — see
[`docs/alerta-research.md`](docs/alerta-research.md).

## Quick Start

```sh
git clone https://github.com/cropalato/promview.git
cd promview
docker compose up --build
```

Open <http://localhost:8080>. The development stack runs in open mode: anonymous
read-only, with ingestion still authenticated.

To run the published image instead of building from a checkout, see the Compose
stack in [`docs/dockerhub.md`](docs/dockerhub.md). Images are published for
**linux/amd64** to both registries:

```sh
docker pull cropalato/promview:beta
docker pull ghcr.io/cropalato/promview:beta
```

Send an Alertmanager-compatible webhook to the bootstrapped `demo` source:

```sh
curl -X POST http://localhost:8080/api/v1/ingest/alertmanager/demo \
  -H 'Authorization: Bearer development-token' \
  -H 'Content-Type: application/json' \
  -d '{
    "version": "4",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "ExampleAlert", "severity": "warning"},
      "annotations": {"summary": "Example alert delivery"},
      "startsAt": "2026-08-14T12:00:00Z"
    }]
  }'
```

Each source has its own opaque bearer token. Promview stores only its SHA-256 hash. The Compose environment bootstraps `demo` when it is absent or has no credential; restarting the app does not overwrite a token rotated later.

Provision or rotate another source with the CLI:

```sh
docker compose run --rm app source set \
  --slug production \
  --name 'Production Alertmanager' \
  --token 'replace-with-at-least-16-characters'
```

The webhook URL source slug and bearer token must identify the same enabled source.

For the complete Prometheus rule, Alertmanager routing, authentication, TLS, validation, and token-rotation procedure, see [`docs/prometheus-alertmanager.md`](docs/prometheus-alertmanager.md).

Inspect the current principal:

```sh
curl 'http://localhost:8080/api/v1/me'
```

Open mode returns an anonymous global viewer and keeps ingestion authenticated. OIDC is the only interactive authentication mode; provider tokens remain on the server.

## Run On Kubernetes

Promview ships a Helm chart for an external PostgreSQL database:

```sh
helm upgrade --install promview oci://ghcr.io/cropalato/charts/promview \
  --namespace promview \
  --version 0.1.0-beta.1
```

Create the required database Secret before installation. The pinned chart version is
also the application version, so upgrading means moving that pin wherever it is held.
See [`docs/kubernetes.md`](docs/kubernetes.md) and [`charts/promview/README.md`](charts/promview/README.md) for the complete procedure, upgrades, OIDC values, migration lifecycle, and production checklist.

## Filtering, Sorting, And Grouping

List firing alerts:

```sh
curl 'http://localhost:8080/api/v1/alerts?status=firing&limit=100'
```

The list endpoint supports opaque cursor pagination, source/status filters, and repeated label matchers. Use `match=label=value` or `match=label!=value` for ANDed positive and negative label filters, plus `sort` and `order=asc|desc` for supported columns. The browser console applies these filters and sorts server-side; detail labels can add or replace a positive or negative filter.

Collapse a fan-out into one row per alert name and source, and expand one group
by asking for its members:

```sh
curl 'http://localhost:8080/api/v1/alerts?groupBy=alertname,source&status=firing'
curl 'http://localhost:8080/api/v1/alerts?match=alertname%3DPrometheusTimeseriesCardinality'
```

Grouping accepts `alertname`, `source`, `team`, `severity` and `instance`, up to three
at once. Group counts are computed under the caller's own read restrictions, so a group
never reports members the caller cannot open. Expanding a group is the ordinary alerts
query with a matcher, which is why sorting, cursors and the detail view behave
identically inside a group.

## Live Updates

Live alert changes are available as resumable server-sent events:

```sh
curl --no-buffer 'http://localhost:8080/api/v1/stream?cursor=0'
```

Each alerts snapshot includes `streamCursor`. Start the stream from that value to avoid missing changes between the snapshot and live updates. Reconnects may instead send `Last-Event-ID`.

Only created or materially changed alerts produce stream events. A repeated
identical delivery updates timestamps and counts without waking every open
console.

### Stream Retention

Stream events exist so a client that lost its connection can resume. They are
deleted once they are older than the retention window:

```sh
export PROMVIEW_STREAM_RETENTION=24h   # 0 keeps every event forever
```

A day covers the disconnections a resume is actually for — a closed laptop, a
rolling deploy, a proxy that dropped every connection at once. Past that, a
fresh snapshot is cheaper than replaying history.

Deleting events creates a case the stream has to handle honestly. A client
resuming from a cursor that has been pruned cannot be sent what no longer
exists, and sending it only the survivors would leave it reconnected, reporting
no error, and quietly wrong about which alerts are firing. Promview records the
highest id it has deleted, and a client resuming from below that is sent a
`stream.gap` event instead:

```text
event: stream.gap
data: {"resumeFrom":7,"retainedFrom":40}
```

The correct response is to take a fresh snapshot and resume from its
`streamCursor`. A client already at or past the watermark has missed nothing and
is never interrupted. `promview_stream_gaps_total` counts these, and a rising
count means the window is shorter than the disconnections this deployment sees.

## Acknowledgement

Authorized operators can acknowledge or unacknowledge an alert from its detail view. This records Promview-local state and timeline history but does not alter Alertmanager routing, notifications, or silences.

## Assignment

An authorized operator can record who owns an alert:

```sh
curl -X PUT 'http://localhost:8080/api/v1/alerts/42/assignee' \
  -H 'Content-Type: application/json' \
  -d '{"assignee":"platform-rota"}'
```

The assignee is free text, not a Promview user. An alert is routinely handed to
somebody who has never signed in here — a vendor, a rota address, the name in a
runbook — and requiring an account would turn each of those into an error
instead of an assignment. Who *decided* is recorded separately as `assignedBy`,
from the signed-in operator, because "who owns this" and "who said so" are
different questions and the second one is the accountability trail.

An empty assignee clears it; there is no separate unassign verb. Assigning to
whoever already owns the alert changes nothing and wakes no console.

Assignment is Promview-local and never reaches Alertmanager. It is cleared when
a resolved alert fires again, along with the acknowledgement: that is a new
occurrence, and the previous owner did not agree to own it.

## Notes

An operator can leave a note on an alert — what was checked, what was ruled out,
who was called:

```sh
curl -X POST 'http://localhost:8080/api/v1/alerts/42/notes' \
  -H 'Content-Type: application/json' \
  -d '{"body":"Paged the vendor, awaiting callback"}'
```

Notes are **append-only**. There is no endpoint that edits or deletes one,
deliberately: a note is what somebody relied on at the time, and a handover that
can be quietly rewritten afterwards is worth less than none. Deleting the alert
takes its notes with it.

Each note records the occurrence it was written against, so a note about a
previous incident stays attributable rather than reading as though it describes
the current one. Notes survive an alert resolving and firing again; the
assignment and acknowledgement do not.

The alert list carries a note *count* rather than the notes, because the list's
job is to show there is something to read and opening the alert is what reads
it. The full notes are on the detail response.

## Close

An operator can file an alert as handled without touching Alertmanager:

```sh
curl -X POST 'http://localhost:8080/api/v1/alerts/42/close' \
  -H 'Content-Type: application/json' \
  -d '{"closed":true}'
```

Closing is Promview-local. The source keeps reporting the alert exactly as
before and its status stays `firing`; `closed` is a separate flag, because an
alert can honestly be both still firing and already dealt with, and one field
cannot say both. Closing is not silencing: nothing is written to Alertmanager
and notifications continue.

Closed alerts leave the default list — otherwise closing would be an action with
no visible effect. `?closed=true` goes looking for them:

```sh
curl 'http://localhost:8080/api/v1/alerts?closed=true'
```

Two things reopen a closed alert. An operator, with `{"closed":false}`. Or a
delivery that **materially changes** it — new labels, a new annotation, a status
transition — because that is not the alert they closed. An identical repeat
deliberately does not: those arrive every `repeat_interval` and carry no new
information, and reopening on one would mean a close never outlived the next
notification.

## Bulk Actions

One operator decision applied to a selection. Every single-alert action has a
bulk form taking the same body plus the ids:

```sh
curl -X POST 'http://localhost:8080/api/v1/alerts/bulk/close' \
  -H 'Content-Type: application/json' \
  -d '{"ids":["42","43","44"],"closed":true}'
```

| Endpoint | Body |
| --- | --- |
| `POST /api/v1/alerts/bulk/acknowledge` | `{"ids":[…],"acknowledged":true}` |
| `PUT /api/v1/alerts/bulk/assignee` | `{"ids":[…],"assignee":"platform-rota"}` |
| `POST /api/v1/alerts/bulk/close` | `{"ids":[…],"closed":true}` |
| `POST /api/v1/alerts/bulk/notes` | `{"ids":[…],"body":"same root cause"}` |

**Selection is by explicit id, never by filter.** "Close everything matching this
query" reads the same whether it matches four alerts or four thousand, and the
operator cannot see which until it has happened. At most 500 ids per request.

**Every alert is judged on its own.** One outside the operator's scope does not
fail the rest — it comes back as `notFound`, the same answer the single-alert
endpoint gives, so a bulk reply cannot be read to discover what exists outside a
scope. The response reports each:

```json
{"applied":1,"unchanged":1,"notFound":1,
 "results":[{"id":42,"status":"applied"},
            {"id":43,"status":"unchanged"},
            {"id":44,"status":"notFound"}]}
```

`unchanged` is reported apart from `applied` so an operator can tell "I changed
forty" from "I changed two and the rest were already done". The status is `200`
when everything was visible and `207` when anything came back `notFound`.

The whole request is one transaction: a bulk action is one decision, and half of
it surviving a failure is a state nobody asked for.

## Alert Expiry

Alertmanager suppresses resolved notifications for silenced alerts, so an alert that
clears inside a maintenance window is never announced and would otherwise stay firing
forever. A background sweep marks alerts whose source went quiet as `expired`, which is
a weaker claim than `resolved`: the source stopped reporting, it never said the alert
was over. History records `alert.expired`; the alert stream carries `alert.resolved`,
since consumers only need to know it left the firing view.

```sh
export PROMVIEW_ALERT_STALE_AFTER=12h   # default; 0 disables expiry entirely
export PROMVIEW_ALERT_EXPIRY_INTERVAL=1m
```

The window must exceed the source Alertmanager's `repeat_interval`, or a live alert
expires between repeat notifications and flaps back on the next one. The default of 12h
is three times Alertmanager's own 4h default. Sources with a different `repeat_interval`
set their own window, which wins over the server default:

```sh
promview source set --slug primary --name Primary --token "$TOKEN" --stale-after 6h
```

An individual alert can shorten its own window with a numeric `timeout` label (in
seconds) on the rule, matching how Alerta reads the same label.

The window is measured from the last delivery *or* the last time reconciliation
confirmed the alert on its Alertmanager, whichever is later. That is what stops a
silenced alert — one the source deliberately sends no notifications for — being
retired while it is demonstrably still firing.

## Alertmanager Reconciliation

Expiry infers an ending from silence; reconciliation confirms one. Given a source's
Alertmanager URL, promview reads `GET /api/v2/alerts` on a loop and aligns what it
holds with what still exists: an alert the Alertmanager no longer lists is resolved,
and one it reports as `suppressed` is flagged silenced while remaining firing.

```sh
# On an existing source, without handling its token:
promview source update --slug primary --alertmanager-url http://alertmanager.monitoring:9093

# Or when first creating the source:
promview source set --slug primary --name Primary --token "$TOKEN" \
  --alertmanager-url http://alertmanager.monitoring:9093

export PROMVIEW_RECONCILE_INTERVAL=1m   # 0 disables reconciliation
export PROMVIEW_RECONCILE_TIMEOUT=10s
```

The API is read-only and used unauthenticated, so a source behind authentication is
not supported yet. A source without a URL is left to expiry alone.

Reconciliation also corrects expiry. It reads the source's `expired` alerts
alongside the firing ones and returns to firing any the Alertmanager still holds,
since expiry only ever inferred that ending and the source is now contradicting
it. The alert keeps its occurrence and its acknowledgement: a new occurrence is
what follows a *resolved* alert, where the source said it ended, and nothing
ended here. A `resolved` alert is never revived, for the same reason.

So that this does not become a cycle — the silence that caused the wrong expiry
is usually still in force — reconciliation records when it last confirmed each
alert, and expiry measures staleness from that as well as from the last delivery.
An alert the source still holds is not retired at all. A source that cannot be
reached stops providing that evidence and goes stale exactly as before, which is
what keeps expiry a working backstop rather than something reconciliation
silently disables.

Two rules keep a healthy Alertmanager from emptying the console. An alert must be
absent from two consecutive readings before it is resolved, so a dropped request
changes nothing. And an Alertmanager reporting _no_ alerts at all while promview holds
firing ones is treated as untrustworthy — a restarting Alertmanager looks exactly like
a fleet that went silent — so that reading only syncs suppression and leaves endings
to a reading that shows something.

## Silences

An operator can silence a single alert or a whole group from the console, which creates the silence on the source's Alertmanager.

- A single alert silences on its **full label set**, so only that series stops notifying. A group silences on its **grouping key**; `source` names a Promview source rather than an alert label, so it selects which Alertmanager to write to instead of becoming a matcher.
- A group whose members span several Alertmanagers produces one silence per Alertmanager, and the console reports the outcome for each. A partial application is reported as such (HTTP 207) rather than as success.
- Silences are attributed to the signed-in user, so this needs `PROMVIEW_AUTH_MODE=oidc` and an operator or administrator role binding. Open mode has no user to attribute a silence to and cannot create one.
- Where silencing is unavailable — open mode, a viewer, or a source with no `alertmanager_url` — the console hides the controls rather than showing ones that fail. If you deploy this and see no **Silence** buttons, that is the gate rather than a fault; check the auth mode, your role binding, and the source's Alertmanager URL.
- Every silence expires. `PROMVIEW_SILENCE_DEFAULT_DURATION` sets the window an operator gets by default (`2h`), and `PROMVIEW_SILENCE_MAX_DURATION` caps what they may ask for (`720h`).

Writing a silence is the only place Promview writes to an Alertmanager. Reads are unauthenticated in the deployments this targets, but writes are commonly protected, so a source can carry a credential for them:

```sh
promview source set --slug demo --name Demo --token <ingest-token> \
  --alertmanager-url http://alertmanager:9093 \
  --alertmanager-token <alertmanager-token>
```

The Alertmanager token is stored as given rather than hashed, because it has to be replayed on every request. Treat `alert_sources.alertmanager_token` as a secret at rest. A source with no token sends no credential, which is the right setting for an Alertmanager that allows anonymous writes.

## Console Preferences

Column choice and order, row density, grouping keys, the console palette, and notification policy are stored per user in `user_preferences` and served by `GET`/`PUT /api/v1/preferences`, so they follow an operator between machines.

Notification policy is an opt-in plus a label selector, edited in the view menu with the same syntax as the filter bar. It matches on `severity`, `alertname`, `source`, and `team` — the fields a stream event carries — and a selector naming anything else is refused rather than silently never firing. An empty selector notifies about nothing. Browser permission is separate: it belongs to one browser profile, no server can grant it, and the dedupe ledger that stops a replayed event notifying twice stays local for the same reason.

Permission is requested only after the user selects the notification control. Page-open notifications require HTTPS or localhost and an open Promview tab; closed-browser delivery would require a future Web Push service worker.

This requires a user to key against, which means `PROMVIEW_AUTH_MODE=oidc`. In `open` mode every reader is the same anonymous principal, the endpoint answers `404`, and the console keeps its preferences in the browser instead — the choices still work, they just do not travel.

The palette defaults to `system`, which follows the operating system's light/dark setting as the console always has. The alternatives are `dark`, `light`, `nord`, `gruvbox`, `solarized-light`, `high-contrast`, and `colorblind-safe`, picked from the status bar at the bottom of the console.

## Desktop Client

The same console ships as a Tauri desktop and tray client. Unsigned installers
for Linux (`.deb`, `.rpm`) and Windows (`.msi`, `.exe`) are attached to every
[release](https://github.com/cropalato/promview/releases); Arch users can build
`promview-desktop-bin` from
[`desktop/packaging/aur`](desktop/packaging/aur). macOS is not built: signing
needs certificates this project does not have.

### Desktop Sign-In

A desktop client cannot receive the cookie the browser flow ends in, so it signs
in through a loopback redirect:

1. It opens the system browser at `/api/v1/auth/oidc/login?desktop_redirect=http://127.0.0.1:<port>/callback`.
2. The server runs the usual OIDC exchange, then redirects to that loopback
   address with a one-time `code` rather than setting a cookie.
3. The client posts the code to `/api/v1/auth/desktop/exchange` and receives an
   ordinary session token, revocable like any other.

The desktop never talks to the identity provider and never holds a token issued
by one. The code is single-use, expires after a minute, is stored only as a
hash, and is redeemed over POST so the credential itself never appears in a URL
that could reach browser history or a proxy log.

`desktop_redirect` is validated strictly, because the server sends a freshly
minted credential to whatever it accepts. Only `127.0.0.1`, `::1`, or
`localhost` with an explicit port, over plain http, with no query, fragment, or
userinfo. A hostname that merely resolves to a loopback address is refused: that
is a promise which can change.

### Desktop Client Configuration

The desktop shell has no origin to be relative to — unlike the console a server
serves, it is a local webview that must be told where to look. `PROMVIEW_SERVER_URL`
answers that for a one-off run; an optional TOML file answers it durably, along
with the settings that would otherwise need a wrapper script around the desktop
entry.

It is read from the first of `$XDG_CONFIG_HOME/promview-desktop/config.toml`
(so `~/.config/…`), `~/.promview-desktop/config.toml`, or `~/.promview-desktop.toml`,
with `%APPDATA%` and `~/Library/Application Support` standing in for the first on
Windows and macOS. `PROMVIEW_DESKTOP_CONFIG` names one outright. Unknown keys are
refused rather than ignored, because a settings file whose typos pass is a file
you believe is in effect when it is not.

```toml
server_url = "https://promview.internal"
poll_interval_secs = 60
webkit_dmabuf = "auto"   # or "on" / "off"

[env]
SSL_CERT_FILE = "/etc/promview/internal-ca.pem"

[[notifications.rules]]
severity = "^critical$"
team = "^(core|platform)$"
```

Three things are worth knowing from here, with the rest in
[`desktop/README.md`](desktop/README.md) and a commented copy in
[`desktop/config.example.toml`](desktop/config.example.toml):

- **`[env]` exports variables before the webview starts**, which is the only
  moment early enough for a certificate bundle outside the system trust store or
  for WebKit's own variables to be read. A variable already exported wins, so
  `SSL_CERT_FILE=… promview-desktop` still does what it looks like it does.
- **`[[notifications.rules]]` narrows notifications per machine and never widens
  them.** Policy stays in [Console Preferences](#console-preferences), where it
  follows an operator between clients; this is for what is true of one machine,
  like a laptop that should only ever buzz for its owner's team. Rules are ORed,
  fields within a rule ANDed, values are unanchored regular expressions over the
  same fields a stream event carries. No rules means no filtering.
- **`webkit_dmabuf` overrides a guess.** WebKitGTK's DMA-BUF renderer fails on
  the NVIDIA driver and renders a blank window, so the shell probes for that at
  startup and switches the renderer off. `"on"` says the guess is wrong about a
  machine.

## OIDC Authentication

For an Okta-specific walkthrough, see [`docs/okta-oidc.md`](docs/okta-oidc.md).

Register this exact callback URL with the identity provider:

```text
https://promview.example.com/api/v1/auth/oidc/callback
```

Configure Promview through the Compose environment:

```sh
export PROMVIEW_AUTH_MODE=oidc
export PROMVIEW_OIDC_ISSUER_URL='https://identity.example.com'
export PROMVIEW_OIDC_CLIENT_ID='promview'
export PROMVIEW_OIDC_CLIENT_SECRET='replace-with-client-secret'
export PROMVIEW_OIDC_REDIRECT_URL='https://promview.example.com/api/v1/auth/oidc/callback'
docker compose up --build
```

Create at least one server-owned binding before the first OIDC login:

```sh
docker compose run --rm app access set \
  --name promview-administrators \
  --role administrator \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-administrators'
```

Create a scoped operator binding by repeating `--selector` for AND semantics:

```sh
docker compose run --rm app access set \
  --name platform-operators \
  --role operator \
  --oidc-issuer 'https://identity.example.com' \
  --oidc-group 'promview-platform' \
  --selector 'team=platform' \
  --selector 'environment!=development'
```

Promview uses provider discovery and Authorization Code with PKCE. It validates the ID token signature, issuer, audience, expiry, state, and nonce, then issues its own opaque 12-hour session in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. Provider access and ID tokens are not persisted.

The default scopes are `openid,profile,email,groups`; the default claims are `preferred_username`, `email`, `name`, and `groups`. Override them with `PROMVIEW_OIDC_SCOPES`, `PROMVIEW_OIDC_USERNAME_CLAIM`, `PROMVIEW_OIDC_EMAIL_CLAIM`, `PROMVIEW_OIDC_DISPLAY_NAME_CLAIM`, and `PROMVIEW_OIDC_GROUPS_CLAIM`. Unbound identities are denied. Bindings are evaluated from the database on every request, so policy changes affect existing sessions immediately. Provider group changes take effect when Promview next observes them during a successful login.

Selectors support `=`, `!=`, `=~`, and `!~`. Selectors within one binding are ANDed; multiple matching bindings are ORed. Viewer and operator bindings may be scoped, while administrator bindings are always global.

Bindings can be administered over the API as well as the CLI, by an
administrator:

```sh
curl 'http://localhost:8080/api/v1/access/bindings'

curl -X PUT 'http://localhost:8080/api/v1/access/bindings/platform-operators' \
  -H 'Content-Type: application/json' \
  -d '{"subjectKind":"oidc_group","oidcIssuer":"https://identity.example.com",
       "oidcGroup":"promview-platform","role":"operator",
       "matchers":[{"name":"team","operator":"=","value":"platform"}]}'

curl -X DELETE 'http://localhost:8080/api/v1/access/bindings/platform-operators'
```

Administrator only — not operator: changing who can do what is not an operator
action, and open mode has no identity to hold accountable for a policy change.

**A change that would leave no administrator binding is refused**, whether by
deleting the last one or demoting it. The check runs inside the same
transaction as the write, so two administrators removing each other at once
cannot both succeed. Creating the first administrator on a deployment that has
none is always allowed.

See [`docs/authorization.md`](docs/authorization.md) for binding administration, selector semantics, revocation behavior, and deployment-specific commands.

Production issuer and redirect URLs must use HTTPS. Loopback HTTP is supported for provider testing by setting `PROMVIEW_OIDC_COOKIE_SECURE=false`; insecure cookies are rejected for non-loopback redirect hosts.

## Project Status

Beta. Every item on the project's own first-release list is implemented, and
the scale the plan commits to is measured rather than assumed — 50,000 alerts
ingested at 1,236/s against PostgreSQL 18.6, with the first page and its counts
answered in 100ms.

Beta means the feature set is settled and the known gaps are written down, not
that it has been proven in production. What works today:

| Area | State |
| --- | --- |
| Authenticated webhook ingestion, multiple sources | Working |
| Current state, occurrences, lifecycle history | Working |
| Filtering, sorting, cursor pagination, grouping | Working |
| Resumable SSE and live console refresh | Working |
| Expiry and Alertmanager reconciliation | Working |
| Acknowledge / unacknowledge | Working |
| Create and remove silences | Working |
| OIDC sign-in, label-scoped roles enforced in SQL | Working |
| Helm chart, Compose, desktop client | Working |
| Assign | Working |
| Notes | Working |
| Close (local) | Working |
| Bulk actions | Working |
| Authorization administration API | Working |
| Stream event retention | Working |

A source being reconciled no longer has its alerts retired by expiry behind its
back: reconciliation records that the Alertmanager still holds an alert, and
expiry measures staleness from that as well as from the last delivery. An alert
expiry retired before this is returned to firing on the next pass, keeping its
occurrence and its acknowledgement. Expiry is unchanged where it is still the
only signal — a source with no Alertmanager URL, or one that cannot be reached.

### Known limits

- **Concurrent load is unmeasured.** The load test drives the store directly,
  with one client and no readers. A deployment ingesting while consoles poll the
  stream and reconciliation sweeps is a different question, and it is the one
  most likely to find a problem first.
- **`main` and `run` are untested**, as is the wrapper that records a created
  silence's provenance. They are process wiring and a database passthrough; the
  loops around them — expiry, reconciliation and stream pruning — are covered.

Issues and discussion are welcome. Beta means the feature set is settled and the
gaps above are written down, not that this has been proven in production; real
deployment feedback is the most useful thing it can get.

## Documentation

| Document | Covers |
| --- | --- |
| [`docs/project-plan.md`](docs/project-plan.md) | Lifecycle, data model, API, and planned work |
| [`docs/prometheus-alertmanager.md`](docs/prometheus-alertmanager.md) | Prometheus rules, Alertmanager routing, TLS, token rotation |
| [`docs/kubernetes.md`](docs/kubernetes.md) | Helm installation, upgrades, production checklist |
| [`docs/authorization.md`](docs/authorization.md) | Roles, selectors, binding administration |
| [`docs/okta-oidc.md`](docs/okta-oidc.md) | Worked OIDC provider setup |
| [`docs/metrics.md`](docs/metrics.md) | Exported Prometheus metrics |
| [`docs/desktop-client-plan.md`](docs/desktop-client-plan.md) | Desktop client design |
| [`docs/alerta-research.md`](docs/alerta-research.md) | Reference investigation behind the design |
| [`docs/dockerhub.md`](docs/dockerhub.md) | Published image: tags, ports, full configuration |

## Development

### Requirements

- Go 1.25 or newer
- Node.js 22 or newer
- Docker with Compose
- Helm 3.14 or newer when packaging or installing the Kubernetes chart
- PostgreSQL client tools only when running migration verification directly

### Verification

```sh
make verify
```

This runs Go formatting checks, tests, and build; frontend formatting, linting, typechecking, tests, and build; Docker Compose configuration validation; and the Helm chart checks.

Focused commands:

```sh
make verify-go
make verify-web
make verify-desktop
go test ./internal/alertmanager -run TestDecodeAndNormalize
npm --prefix web run test -- src/config/runtimeConfig.test.ts
make compose-check
make docker-build
make verify-helm
```

The scale the project commits to is measured rather than assumed:

```sh
make load-test              # seeds 50,000 alerts, reports ingest and read timings
PROMVIEW_LOAD_ALERTS=5000 make load-test
```

It writes tens of thousands of rows and takes minutes, so it is deliberate
rather than part of `make verify`. It drives the store directly: it does not
measure the HTTP path, the SSE fanout, or contention between ingestion and
reads.

Migration verification requires a disposable PostgreSQL database because it applies up, down, and up migrations:

```sh
export PROMVIEW_TEST_DATABASE_URL='postgres://promview:promview@localhost:5432/promview_test?sslmode=disable'
make migration-check
make test-postgres
```

Every command above has a matching GitHub Actions job in `.github/workflows/ci.yml`, which is the executable source of truth for what must pass.

Do not use `go test ./...` after installing frontend dependencies: Go can discover `.go` files inside `web/node_modules`. Use the package boundaries in `make verify-go` instead.

To reset the development database and rerun initialization:

```sh
docker compose down --volumes
```

The Go module is `github.com/cropalato/promview`. Repository conventions live in
[`AGENTS.md`](AGENTS.md); the project uses
[Conventional Commits](https://www.conventionalcommits.org/) and records user-visible
changes in [`CHANGELOG.md`](CHANGELOG.md).

## License

[MIT](LICENSE)
