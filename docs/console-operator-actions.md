# Console work: operator actions

The server carries seven operator actions and a stream signal that no client can
reach. This is the specification for closing that gap, written from the shipped
API rather than from intent: every field and status below was read off the
handlers in `internal/httpapi`.

Both clients are covered by one body of work. The desktop shell proxies any
method and path without an allowlist (`desktop/src-tauri/src/proxy.rs`) and its
SSE reader parses any event name (`desktop/src-tauri/src/sse.rs`), so it needs no
Rust change and inherits everything done in `web/src`.

Suggested order is the numbering below: recovery first, then correctness, then
the controls, then the largest piece.

## Conventions that apply to all four

- **Alert ids are strings** on the wire, everywhere, including inside bulk
  arrays. A JSON number loses precision in a browser before a `bigint` does in
  the database.
- **Every mutation returns the full detail envelope** — `{alert, history,
  silences, notes}` — so a caller replaces its cached detail wholesale rather
  than patching fields. `setAlertAcknowledgement` in `web/src/alerts/detail.ts`
  is the pattern to copy.
- **`403` means the server withheld a permission the UI gated on.** It is not a
  bug to be hidden: surface it like any other failure. It happens legitimately
  when a binding changes between the page load and the click.
- **Gate every control on the server's own `actions` object**, never on a locally
  inferred role. `parseActions` in `detail.ts` returns all-false for an absent or
  malformed envelope, and must keep doing so as fields are added.

---

## 1. A way back to closed alerts

**The problem.** Closing an alert removes it from the default list and the
console offers no way to see it again. From an operator's seat that is
indistinguishable from data loss, and it is live today: the API shipped in
`v0.1.0-alpha.39` and the filter defaults to open-only.

**API.** The list endpoint takes `closed`:

- absent — open alerts only (the default the server applies)
- `closed=true` — only closed alerts
- `closed=false` — only open alerts, stated explicitly

Any other value is `400`.

Each alert in the list and detail payloads carries `closed`, and the detail also
carries `closedAt` and `closedBy`.

**Work.**

- `web/src/alerts/api.ts` — add `closed?: boolean` to the query type and set the
  param alongside `suppressed`, which is the existing three-state precedent
  (`params.set('suppressed', …)` only when not undefined).
- A filter control. The **Silenced alerts** segmented control in the toolbar
  (`All` / `Unsilenced` / `Silenced`) is the established shape for exactly this
  three-state choice; an **Open** / `Closed` / `All` control beside it would read
  consistently.
- Render closed state on the row and in the drawer — a closed alert reached by
  deep link must not look like an ordinary firing one.

**Watch for.** The severity summary counts and the group counts come from the
same query, so they follow the filter automatically. Confirm that rather than
assuming it: a count that includes closed alerts while the table excludes them
is worse than either alone.

**Done when** an operator can close an alert, find it again, and reopen it
without leaving the console.

---

## 2. Acting on `stream.gap`

**The problem.** Retention deletes stream events after `PROMVIEW_STREAM_RETENTION`
(24h by default). A client resuming from a deleted cursor is sent a `stream.gap`
event telling it what it missed. The console does not subscribe to that event, so
it reconnects, reports no error, and shows a snapshot that is quietly wrong. This
is the only item here that is a correctness bug rather than a missing feature.

**API.** On the SSE stream:

```
event: stream.gap
data: {"resumeFrom":7,"retainedFrom":40}
```

`resumeFrom` is the cursor the client asked for; `retainedFrom` is the oldest
point the stream can honestly serve. It is emitted once per gap — the server
advances the cursor past the watermark immediately, so the next poll is ordinary.

**Work.**

- `web/src/alerts/stream.ts` — `ALERT_STREAM_EVENT_TYPES` currently lists the four
  `alert.*` types and the subscription loop is driven from it. `stream.gap` is not
  an alert event and should not be folded into that union; subscribe to it
  separately so its payload stays typed as its own thing.
- On receipt: discard the current snapshot and refetch from scratch, then resume
  the stream from the new snapshot's `streamCursor`. The existing reconnect path
  already does a snapshot-then-resume; reuse it rather than writing a second one.
- Do not surface it as an error. It is normal after a long disconnection, and a
  red banner would train operators to ignore a real one.

**Watch for.** The desktop holds its EventSource in the host process; confirm the
gap event arrives in the webview as well as the browser. The parser does not
filter, so it should — but this is the one place the two clients could differ.

**Done when** a console left disconnected past the retention window comes back
with correct state instead of a stale one, in both clients.

---

## 3. Single-alert assign, close and notes

**The problem.** Three actions exist with no control. The console already
*displays* an assignee and a note count — those columns were built before the
data existed — so the gap is input, not output.

**API.**

| Action | Request |
| --- | --- |
| Assign | `PUT /api/v1/alerts/{id}/assignee` `{"assignee":"platform-rota"}` |
| Unassign | the same, with `""` |
| Close / reopen | `POST /api/v1/alerts/{id}/close` `{"closed":true\|false}` |
| Add note | `POST /api/v1/alerts/{id}/notes` `{"body":"…"}` → `201` |

All return the detail envelope. `alert.actions` carries `canAssign` and
`canClose` beside the existing `canAcknowledge` and `canSilence`.

Server-side rules the UI should not re-implement but must not contradict:

- The assignee is **free text**, not a user picker. An alert is routinely handed
  to somebody who has never signed in — a vendor, a rota address, a name in a
  runbook. Do not constrain the field to known users.
- Notes are **append-only**. There is no edit or delete endpoint, so offer
  neither. Each note carries the `occurrence` it was written against; render that
  where a note belongs to a previous incident, or it reads as describing this one.
- Assignment clears when a resolved alert fires again; notes survive. Nothing to
  implement, but the drawer should not cache across a reopen.

**Work.**

- `web/src/alerts/detail.ts` — three clients beside `setAlertAcknowledgement`,
  and extend `parseActions` and the `AlertActions` type with `canAssign` and
  `canClose`, keeping the all-false default.
- Parse `notes` from the detail envelope. It is a sibling of `history` and
  `silences`, currently dropped on the floor.
- `web/src/components/AlertDetailDrawer.tsx` — an assignee field, a close/reopen
  button, and a notes panel with a compose box, each gated on its `can*` flag the
  way the acknowledge button already is.

**Watch for.** `assignee` on a list row is `""` when unassigned, not absent. The
existing column renders a placeholder for empty, so it should already be right —
worth confirming rather than assuming.

**Done when** every action the API offers for one alert can be performed from the
drawer, and refused cleanly when the server says no.

---

## 4. Selection and bulk actions

**The problem.** The largest piece, and the one the actions exist for: after one
incident an operator faces forty rows, and clicking through them individually is
how alerts stop being acknowledged at all. The console has no concept of a
selected row.

**API.** Four endpoints, each taking the single-alert body plus `ids`:

```
POST /api/v1/alerts/bulk/acknowledge  {"ids":["42","43"],"acknowledged":true}
PUT  /api/v1/alerts/bulk/assignee     {"ids":[…],"assignee":"platform-rota"}
POST /api/v1/alerts/bulk/close        {"ids":[…],"closed":true}
POST /api/v1/alerts/bulk/notes        {"ids":[…],"body":"same root cause"}
```

At most **500 ids**. An empty array is `400`.

The response is **not** a detail envelope:

```json
{"applied":1,"unchanged":1,"notFound":1,
 "results":[{"id":42,"status":"applied"},
            {"id":43,"status":"unchanged"},
            {"id":44,"status":"notFound"}]}
```

- `200` when nothing came back `notFound`, `207` when anything did.
- `results[].id` is a **number** here, unlike the string ids sent in. That
  asymmetry is real; do not assume symmetry when matching results to rows.
- `notFound` means the alert is not visible to this operator. It is deliberately
  indistinguishable from one that does not exist, so do not word it as a
  permission error.
- `unchanged` means the alert already held the requested state. Report it apart
  from `applied` so an operator can tell "I changed forty" from "I changed two
  and the rest were already done".

**Work.**

- Selection state — per-row checkboxes, a header select-all bounded by the page,
  and a clear affordance. Selection must survive a stream-driven refetch: a
  refresh landing mid-selection and silently emptying it is worse than no bulk
  action at all.
- A bulk action bar appearing only with a selection, carrying the four actions
  gated on the operator's permissions.
- A result summary honest about all three outcomes. Do not collapse `unchanged`
  into success or `notFound` into failure.
- Selection interacts with grouping: decide whether selecting a collapsed group
  selects its members, and make it visible either way.

**Watch for.** The 500 ceiling is a server error, not a client guess — either cap
the selection in the UI or handle the `400`. And a bulk close empties rows out of
the current view, so the list must settle sensibly rather than jumping.

**Done when** an operator can select a page of alerts, apply one decision, and
see exactly what happened to each.
