# View Groups And Automatic Alert Filter Plan

Status: phase 1 is implemented on the server (preferences, `view` parameter,
SQL predicate, stream parity, `viewMatch`, hidden count). The console, view
groups, and notification alignment are still planned.

## Problem

Not everybody wants to see every alert. Authorization scope already stops a
user from seeing alerts outside their team, but a user whose scope is broad, or
unrestricted, opens the console to everything and has to rebuild their own
filter on every machine. The console has no notion of "the alerts I normally
care about".

## Principles

1. **A view is a convenience, never a permission.** Authorization scope stays
   the only security boundary. A view can only narrow what scope already
   allows. Joining, leaving or editing a view group cannot reveal an alert the
   user could not already read.
2. **The server applies the view.** List, counts, grouping and the stream must
   agree, and the desktop client inherits the behaviour for free. Clients only
   say which view they want.
3. **Preferences live on the server** (`user_preferences`) and follow the user
   to web and desktop. Open mode has no user, so it has no view: it shows
   everything, as today.
4. **The filter must never be silent.** A console that quietly hides alerts is
   how an on-call operator misses one. Everything hidden is counted and one
   click away.

## Semantics

Effective result for a caller:

```text
result = scope ∩ ( view  ∪  critical   if criticalBypass )
```

- `scope` is the union of the caller's grants, enforced in SQL today.
- `view` is the union (OR) of the selectors of every view source the caller
  has, below. A view with no sources is **no filter**, not an empty list.
- `critical` is the alert's normalized severity, not a raw label, so sources
  cannot disagree about it. The bypass sits inside `scope`: it can never show an
  alert the caller is not authorized to read.
- `criticalBypass` defaults to **true**. A user can turn it off explicitly in
  their preferences to have critical alerts filtered like any other.

View sources, all unioned:

| Source | Defined by | Membership | Can the user leave? |
| --- | --- | --- | --- |
| Derived | matchers of the caller's scoped grants, when they also hold an unrestricted one | automatic | only via the global switch |
| Internal view group | administrator: name, description, selector | self-service join/leave | yes |
| Pinned internal group | administrator pins it to a user or to a directory group | forced | no |

Rules that need to be explicit:

- **Derived filter.** Built from the matchers of the caller's scoped viewer and
  operator grants, ORed. It only exists for a caller who also has an
  unrestricted grant (an unscoped binding or the administrator role): scope is
  then everything, and the scoped groups say what they normally look at. For a
  caller whose every grant is scoped, a view derived from those grants would
  equal scope and restrict nothing, so there is no derived filter and no hidden
  count to compute. A first version of this plan said the opposite (no filter
  when any grant is unrestricted), which would have made the derived filter a
  no-op for everyone; the real value for scoped-only users arrives with
  internal view groups in phase 2. Grants are read from the database on every
  request, so a binding change applies at once; only the directory group
  membership lags until the next login, as it does for authorization today.
- **Pinned is a default, not a restriction.** A pinned group cannot be left,
  and is always part of the union, but the global switch
  (`view.enabled = false`, "show everything in my scope") stays available.
  Anything that must truly be unavailable to a user is a binding scope, not a
  view. This is deliberate: a hard view would be an authorization rule that
  looks like a preference, and it would trap an on-call operator who needs to
  look at the neighbouring team's alerts.
- **Selector syntax** is the one binding scopes already use
  (`team=platform,severity=~warn.*`), so there is one grammar to learn,
  validate and test.
- **Joining a group whose selector falls outside the user's scope** is allowed
  and yields nothing for that group. Rejecting it would let a user probe which
  selectors overlap with scope they cannot see.
- **Group visibility.** Every authenticated user can list group names and
  descriptions so they can join. Selectors are shown only to administrators: a
  selector can name teams or labels the reader has no access to. Membership
  lists are visible to administrators only; a user sees their own groups.
- **Open mode and anonymous callers** get no view and no preference endpoints
  for it.

## Data Model

Migration `000025_view_groups` (up and down; keep `scripts/check-migrations.sh`
in step):

- `view_groups(id, name UNIQUE, description, matchers, pinned, created_at,
  updated_at)`. Matchers are stored the way `role_bindings` stores its own.
- `view_group_members(view_group_id, user_id, joined_at, PRIMARY KEY
  (view_group_id, user_id))`: self-service membership only.
- `view_group_pins(view_group_id, subject_kind, user_id, subject_issuer,
  subject_group)`: who a pinned group is forced on, mirroring the subject
  columns and CHECK constraints of `role_bindings` (user, `oidc_group`,
  `ldap_group`). Evaluated per request, like bindings.

A user's internal groups are `self members ∪ groups pinned to them`. Leaving a
group deletes only the self row; leaving a pinned group answers 409.

`user_preferences` is a JSON document. Add:

```json
"view": { "enabled": true, "criticalBypass": true }
```

A row written before this field exists has no key at all, and a missing
boolean must resolve to **true**, not Go's zero value, so `view` is a pointer
and reads fall back to the defaults. A write that omits `view` (an older
console saving its layout) keeps the stored choice rather than erasing it.
Both are covered by `TestStoreViewPreferences`.

## API

Additive only; older clients keep working.

- `view` query parameter on the alert list (flat and grouped) and stream
  endpoints:
  `view=mine` or `view=all`. Absent means the stored preference. `all` is a
  per-request override so a client can offer a "show everything" toggle without
  persisting it. It still returns only what scope allows.
- Snapshot and group responses add view metadata:
  `view: { active, sources: [...], hidden: <count>, criticalBypass }`.
  `hidden` is the number of alerts in scope that are neither in the view nor
  bypassed. Measure its cost on the largest test dataset before shipping; if it
  is too slow, compute it lazily behind its own endpoint.
- Every alert (and stream event) carries `viewMatch`: `view`, `critical`
  (shown only by the bypass) or `outside` (present only when `view=all`). This
  one field drives the "outside your view" chip and lets notifications decide
  without recomputing the view in each client.
- `GET /api/v1/view-groups`: groups visible to the caller, with their
  membership state.
- `POST|PUT|DELETE /api/v1/view-groups[/{id}]`: administrator.
- `PUT|DELETE /api/v1/view-groups/{id}/membership`: the caller, self-service.
- `POST|DELETE /api/v1/view-groups/{id}/pins`: administrator.
- `GET|PUT /api/v1/preferences`: extended with `view`.
- CLI mirroring `promview source set`: `promview view-group
  create|list|set|delete|pin|unpin`.

These mutations fall under whatever CSRF protection the browser mutations get;
the project plan still lists that as planned, so do not ship them ahead of it
without noting the gap.

## Stream Contract

The snapshot-before-stream contract is preserved and unchanged.

- The stream filters per connection with the same predicate as the snapshot,
  evaluated against the label snapshots events already carry. An alert moving
  out of the view is handled like moving out of scope: the client receives a
  removal for it.
- The durable `streamCursor` and `Last-Event-ID` stay global, so resume still
  works. Changing the view (the toggle, joining or leaving a group, changing
  `criticalBypass`) means a **new snapshot and a new stream connection** with
  the new cursor, never a mid-stream filter change.
- Events and removals respect the bypass: a critical alert outside the view
  produces events marked `viewMatch: critical`.

## Notifications

By default notifications follow the effective view, bypass included: an alert
notifies only if it is shown. The existing label selector in
`notifications` stays and is applied on top (AND). A `notifications.followView`
flag, default true, lets a user opt out. Clients decide from `viewMatch`, so
web and desktop behave identically.

## UI Requirements (contract level only)

Detailed UI planning is delegated to `ui-plan`; implementation to `ui`. What the
UI must satisfy:

- A persistent indicator of the active view: which sources apply, how many
  alerts are hidden, and a one-click "show everything in my scope".
- Alerts shown through the bypass are marked as outside the view.
- Preferences: the global switch, the critical bypass toggle, and the list of
  joinable groups with join/leave. Pinned groups are shown as locked with the
  reason.
- Administrator screen for groups and pins, alongside the existing binding
  administration.
- Same behaviour in the Tauri client; only bearer transport differs.

## Phases

1. **Derived filter.** `view` preference, `view` parameter, SQL predicate,
   stream parity, `viewMatch`, hidden count. No new tables. The server side is
   implemented; the console work is not. It is useful on its own only for
   callers with an unrestricted grant plus a scoped one (see Semantics).
2. **View groups.** Migration, store, REST, CLI, membership, pins.
3. **Notifications and desktop parity.** `followView`, desktop tray counts and
   notifications from `viewMatch`.

Each phase gets its own UI plan and ships behind a complete verification run.

## Verification

All of it runs in the existing CI jobs; no new command is introduced, so no CI
change is expected. If one is added, update `.github/workflows/ci.yml` with it.

- Predicate composition: table tests over scope, view, bypass on and off.
- **The bypass cannot leave scope**: a critical alert outside the caller's
  scope is never returned, counted or streamed. This is the test that matters.
- Scoped-only caller means no derived filter; unrestricted plus scoped grants
  means the scoped ones are the view; no sources means no filter, not an empty
  result.
- Pinned group cannot be left (409); leaving a self group removes only the
  self row; a user cannot learn selectors they are not an administrator for.
- A legacy preferences row resolves `enabled` and `criticalBypass` to true.
- Stream parity: snapshot and stream agree for the same view, including an
  alert entering and leaving the view, and resume after a view change.
- Migration up, down, up through `make migration-check`.
- Frontend: `make verify-web` for each UI phase.

## Open Questions

- Should the derived filter ever be switchable per source, rather than only
  through the global switch? It needs the grant's origin carried on `Grant`,
  which it does not have today. Deferred until someone asks.
- Is the hidden count affordable on a busy installation, or does it need a
  cached or approximate form?
