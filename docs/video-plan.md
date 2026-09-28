# Promview Video Plan

Plan for a small set of product videos presenting the browser console and the
desktop client. Every frame is produced by scripts checked into this
repository: a disposable demo stack, scripted captures, synthesized music, and
an ffmpeg assembly. Nothing is recorded by hand, so a video can be re-rendered
after any UI change the same way a screenshot can.

## 1. Message

Promview is deliberately narrow, and the videos should be too. One sentence
carries every video:

> Alertmanager decides who to wake up. Promview is where the shift happens.

Four claims, each shown rather than stated, and each mapped to one scene:

| Claim | What the viewer sees |
| --- | --- |
| It remembers | Rows arriving live, then a timeline with "started", "acknowledged by", notes |
| It spans Alertmanagers | A second source firing into the same table, `source` column, group by source |
| It keeps your labels | A PromQL-style selector typed into the filter bar and applied server-side |
| One binary | `docker compose up`, then a console; the desktop tray running the same UI |

Tone: calm control room, not a product launch. Monospace type, the console's
own palette, short captions, no voice-over. The music is a pulse, not a drop.
The audience is an on-call engineer who already runs Alertmanager and has
thirty seconds of patience.

Language: English captions only. Subtitles can be added later from the same
caption file.

## 2. Deliverables

| Id | Title | Length | Purpose | Where it goes |
| --- | --- | --- | --- | --- |
| V1 | Trailer | 30 s | Hook. Motion-graphic title cards cut with the four best captures | README top, GitHub social preview, X |
| V2 | Console tour | ~100 s | Feature walk-through of the browser console, one feature per scene | README "Why Promview", YouTube, docs site |
| V3 | Desktop client | ~45 s | Tray, compact window, native notification, sign-in through the system browser | README "Desktop Client", release notes |
| G1..Gn | Feature GIFs | 5–10 s each | Cut from V2/V3 scenes | README sections (filtering, grouping, bulk, silences, themes) |

V4 "Deploy in sixty seconds" (Compose and Helm, terminal capture) is worth
doing but is out of the first pass; the terminal-capture pipeline is a
different tool (asciinema + agg) and should not block the three above.

Output formats: 1920×1080 at 60 fps H.264 (`.mp4`, `yuv420p`, CRF 18) for
YouTube and X, VP9 `.webm` for the docs site, and palette-optimised GIF for the
README clips. Audio AAC 192 kbps, loudness normalised to −14 LUFS.

## 3. Pipeline

```
make video
  ├─ video-demo-up        docker compose (postgres, promview, 2× alertmanager)
  ├─ video-seed           post realistic alerts to both Alertmanagers
  ├─ video-capture-web    Playwright in Xvfb, ffmpeg x11grab, one file per scene
  ├─ video-capture-desktop  promview-desktop in Xvfb + panel, xdotool, x11grab
  ├─ video-cards          canvas title cards + trailer, frame-exact render
  ├─ video-audio          numpy synth: three tracks + sound effects
  └─ video-assemble       ffmpeg concat, xfade, captions, mix, loudnorm
```

Each step is idempotent and writes to `video/out/` (gitignored). A scene can be
re-captured alone with `make video-capture-web SCENE=filter`.

### 3.1 Demo stack (`video/demo/`)

A Compose file that reuses the root `Dockerfile` and adds what a convincing
capture needs:

- `postgres`, `migrate`, `app` as in the root `compose.yaml`.
- `PROMVIEW_AUTH_MODE=local` with one account created at startup
  (`promview user create` + `access set --role operator`). Local mode gives a
  real sign-in form, a named operator in every timeline entry, and attributable
  silences, none of which open mode can show. No identity provider needed.
- Two real Alertmanagers, `alertmanager-prod` and `alertmanager-staging`
  (`prom/alertmanager`), each routing every alert to its own Promview source
  with its own bearer token. Alerts are injected through Alertmanager's
  `POST /api/v2/alerts`, so the webhook, grouping, repeat intervals, silences
  and reconciliation are all the real thing. Nothing is mocked.
- Both sources registered with `--alertmanager-url`, so the **Silence** scene
  writes a real silence and reconciliation flags the row as silenced on its
  next pass (`PROMVIEW_RECONCILE_INTERVAL=5s` for the demo).
- Fonts pinned inside the capture image so a re-render on another machine
  produces the same frames.

### 3.2 Seed data (`video/demo/seed.mjs`)

The fifteen alerts from `docs/images/console.png` are the reference set: four
criticals, nine warnings, two infos, six teams, two sources. The seed script
posts them with `startsAt` offsets relative to now so the Age column reads
"7h", "24m", "2d" instead of a wall of "1m". A second batch (`seed.mjs --wave
staging`) fires the staging alerts on cue for the multi-source scene, and a
third (`--wave incident`) fires a new critical for the notification scene.
The seed is the only place alert content lives; every scene and caption reads
from it.

### 3.3 Web capture (`video/scenes/web/*.mjs`)

Playwright, headed Chromium, inside `xvfb-run` at 1920×1080, recorded with
`ffmpeg -f x11grab -framerate 60`. This is preferred over Playwright's own
`recordVideo` because that output is variable-rate WebM at roughly 25 fps and
looks like a screen recording; x11grab gives a constant 60 fps and draws the
real cursor. The same capture method serves the desktop client, so there is one
recorder to maintain.

Each scene is one script with a fixed duration in beats (section 4). Actions
are scheduled against the scene clock (`at(beat, () => ...)`), so a cut in the
assembly lands on a beat boundary and an action lands where the music expects
it. Pointer movement uses `page.mouse.move(x, y, { steps })` with an ease-out
curve so the cursor reads as a hand, not a teleport. Typing uses
`page.keyboard.type(text, { delay })` at a steady rate.

The status bar clock is frozen with `page.clock.setFixedTime`, so the trailer
can cut between scenes without the time jumping.

Two-context scenes: the "live" scene opens two browser windows side by side
under one Xvfb screen, an operator on the left acknowledging and a viewer on
the right receiving the update over SSE. That is the resumable stream shown,
not described.

### 3.4 Desktop capture (`video/scenes/desktop/*.mjs`)

The Tauri client needs a display, a window manager, a status-notifier host for
the tray, and a notification daemon. Under `xvfb-run`: `openbox` for windows,
`tint2` with `snixembed` bridging StatusNotifier to XEmbed for the tray, and
`dunst` for notifications. `xdotool` drives clicks and hover; `x11grab` records.
`WEBKIT_DISABLE_DMABUF_RENDERER=1` (or `webkit_dmabuf = "off"` in the client
config) keeps WebKitGTK on the software path Xvfb can render.

The client is pointed at the demo stack with a `config.toml` that also carries a
`[[notifications.rules]]` block, so the config-file scene shows a real file.

The demo runs local accounts, so sign-in happens in the client's own window:
the loopback-redirect flow through the system browser exists only for OIDC,
and filming it would need an identity provider in the demo stack. The tray
tooltip with severity counts is not filmable either: the Linux tray goes
through the appindicator protocol, which carries no tooltip.

What was learned getting this to work, kept here so nobody relearns it:

- The sandbox needs its own D-Bus session (`dbus-run-session`), or the tray
  and the notifications land on the real desktop. `snixembed --fork` must be
  up before the client starts, or the icon never registers.
- openbox's click-to-focus mouse bindings swallow the button events the
  webview needs. The sandbox config has none on the client area; windows are
  focused with `xdotool windowactivate` instead.
- `xdotool windowclose` destroys a window. The client hides on a WM close, so
  the scenes send Alt+F4 through an openbox keybinding.
- The tray icon window exists before the panel places it; its geometry is
  read again on every use (`xwininfo -tree`, since `xdotool search` does not
  reach a window embedded in another client's).
- The V3 take is one continuous scene (`scenes/desktop/tour.mjs`); the
  build overlays captions by beat and appends the config-file and end cards.

### 3.5 Title cards and trailer (`video/cards/`)

The same approach as the reference project: an `index.html` with a pure
`renderFrame(t)` on a 2D canvas, driven frame by frame from a headless browser
and piped to ffmpeg. Deterministic, 60 fps, no screen recording involved.

The cards use the console's own CSS tokens from `web/src/styles/global.css`
(the Nord palette, as in the README screenshot) and its monospace face, so a
card and a capture sit next to each other without a visible seam. Motion is
limited to what the console itself does: fades, slides with ease-out, a pulse
on the logo line. No particles.

Cards needed: opening title, one caption card per claim in section 1, four
lower-third captions per scene (rendered as transparent overlays, not burned
into the capture), and an end card with the repository URL and the install
commands.

### 3.6 Soundtrack (`video/audio/synth.py`)

Synthesized with numpy from waveform arithmetic, seeded, and committed as code.
There is no sample library and no licensed track, so the music is owned by the
repository and released under its MIT licence. That is the only way to be sure
a README video does not get a claim later. (A CC-BY track would need attribution
in every place the video is embedded; CC0 catalogues are thin at this tempo.
Synthesis is less risk and more control.)

Musical intent follows the message: a pulse, steady, never a drop.

| Element | Synthesis | Role |
| --- | --- | --- |
| Pulse | Sine with a fast pitch envelope, soft, every beat | The heartbeat under the logo's pulse line |
| Tick | Band-passed noise, 8 ms, every eighth | A clock, not a hi-hat |
| Pad | Seven detuned sawtooths through a low-pass with slow cutoff movement | Warmth; the chord changes on scene cuts |
| Sub | Sine one octave under the pad root | Weight, felt not heard |
| Riser | Filtered noise with a rising cutoff over four bars | Only in the trailer, before the end card |

Tempo 100 BPM, 4/4, so one beat is 0.6 s and one bar is 2.4 s. All scene
lengths are whole bars. Key: D minor, chords i – VI – III – VII, one chord per
scene. A gentle side-chain duck on each pulse keeps it breathing without
sounding like a club.

Three arrangements from one generator: `trailer` (30 s, builds to the riser),
`tour` (loops cleanly, low energy, sits under captions), `desktop` (tour with
the pad only).

Sound effects, same generator: a short click on each pointer click (3 ms noise
burst), a two-note ping when a batch of alerts lands, and a lower single note
for a critical. Effects are placed by the scene scripts, which log each action
with its timestamp to `scene.marks.json`; assembly reads the marks and drops
the effect at the exact frame.

### 3.7 Assembly (`video/build.mjs`)

ffmpeg only. Per video: trim each capture to its beat length, `xfade` on beat
boundaries (6 frames), overlay the caption cards with alpha, concat, mix the
arrangement with the effects at the marks, `loudnorm` to −14 LUFS, encode the
three outputs. GIFs are cut from the marks (`--gif filter` produces the filter
scene at 12 fps, palette-optimised, under 3 MB).

A `make video-check` target renders the first two seconds of each video and
the audio generator at low resolution. That target runs in CI on every change
under `video/`, so a broken scene script or a synth regression is caught
without rendering the full set. Full rendering is `workflow_dispatch` and
attaches the results to the run.

## 4. Storyboards

Beats at 100 BPM. One bar = 4 beats = 2.4 s.

### V2 Console tour (42 bars, ~101 s)

| Bars | Scene | Action | Caption |
| --- | --- | --- | --- |
| 0–2 | `cold-open` | Empty console, "0 firing", status bar "stream: live" | — |
| 2–6 | `ingest` | Prod Alertmanager fires fifteen alerts; rows arrive, severity strip counts climb | Alertmanager sends. Promview remembers. |
| 6–9 | `sources` | Staging fires; `source` column shows two values; group by `source` | Many Alertmanagers. One console. |
| 9–13 | `filter` | Type `{severity="critical", team!="infra"}`; count drops to 3; click a label chip in a row to add `team=data` | Your labels are the filter. |
| 13–16 | `group` | Group by `alertname, source`; `NodeDiskSpaceLow` collapses to one row with a count; expand it | Fan-out, folded. |
| 16–21 | `detail` | Open `PostgresReplicationLag`; overview, labels, timeline; switch to raw payload and back | When did it start. Who has looked. |
| 21–26 | `operate` | Acknowledge, assign `platform-rota`, add note "Paged the vendor, awaiting callback"; timeline gains three entries with the operator's name | Acknowledge. Assign. Leave a note. |
| 26–29 | `live` | Split screen: operator closes an alert on the left; right window updates without a refresh | Every open console, at once. |
| 29–32 | `bulk` | Select three `KubeDeploymentReplicas…` rows, close from the action bar; result reads "applied 3" | One decision, many alerts. |
| 32–36 | `silence` | Silence `TargetDown` from the drawer, 2 h default; row shows silenced after reconciliation | Silenced in Alertmanager, from here. |
| 36–38 | `themes` | Cycle palette from the status bar: nord → gruvbox → solarized-light → high-contrast | Yours to look at all night. |
| 38–42 | `end` | End card: logo, `docker compose up --build`, repository URL | github.com/cropalato/promview |

### V3 Desktop client (19 bars, ~46 s)

| Bars | Scene | Action | Caption |
| --- | --- | --- | --- |
| 0–2 | `tray` | Panel with the Promview icon; hover shows "4 critical · 9 warning · 2 info" | The console, in the tray. |
| 2–5 | `menu` | Open the menu: version at top, Open console, Compact view, Sign in…, Quit; open the console window | Same console. Same server. |
| 5–8 | `compact` | Toggle compact view; small always-on-top window over a terminal; a warning resolves and the count changes | Always on top. Out of the way. |
| 8–11 | `notify` | Incident wave fires one critical; native notification appears from `dunst`; tray tooltip updates | Your machine tells you. |
| 11–15 | `signin` | Sign in… opens Chromium at the login form; sign in; the browser tab closes; menu shows the user | Sign in where your password manager lives. |
| 15–17 | `config` | Editor shows `config.toml`: `server_url`, `[env] SSL_CERT_FILE`, `[[notifications.rules]] severity = "^critical$"` | Per machine, in one file. |
| 17–19 | `end` | End card: `.deb` `.rpm` `.msi` `.exe`, releases URL | — |

### V1 Trailer (12.5 bars, 30 s)

Cards and cuts, no interaction. Chord changes on every card.

| Bars | Content |
| --- | --- |
| 0–1.5 | Black. Pulse only. Logo line draws in. |
| 1.5–3 | Card: "Alertmanager decides who to wake up." |
| 3–4.5 | Cut: `ingest`, rows arriving (from V2) |
| 4.5–5.5 | Card: "Promview is where the shift happens." |
| 5.5–7 | Cut: `detail` timeline, then `operate` note landing |
| 7–8 | Cut: `sources`, two sources grouped |
| 8–9 | Cut: `filter`, selector typed |
| 9–10 | Cut: `tray` and `notify` (from V3) |
| 10–11.5 | Riser. Card: "One binary. Your PostgreSQL. Your labels." |
| 11.5–12.5 | End card: `docker compose up --build`, URL. Pulse stops. |

## 5. Repository layout

```
video/
  README.md               how to render, and what each target does
  package.json            playwright
  demo/
    compose.yaml          postgres, migrate, app (local auth), alertmanager-prod, alertmanager-staging
    alertmanager-prod.yml
    alertmanager-staging.yml
    bootstrap.sh          user create, access set, source set --alertmanager-url
    seed.mjs               alert fixtures and the three waves
  scenes/
    lib/                  beat clock, eased pointer, marks logger, x11grab start/stop
    web/                  one .mjs per V2 scene
    desktop/              tour.mjs, session.sh and etc/ for the sandbox desktop
  cards/
    index.html            renderFrame(t): title cards, captions, trailer motion
    render.mjs            headless frame loop → ffmpeg
  audio/
    synth.py              instruments, arrangements, effects
  build.mjs               assembly
  out/                    gitignored
```

Makefile targets: `video`, `video-demo-up`, `video-demo-down`, `video-seed`,
`video-capture-web`, `video-capture-desktop`, `video-cards`, `video-audio`,
`video-assemble`, `video-check`.

## 6. Order of work

1. Demo stack and seed. Verify the README screenshot can be reproduced from
   it; that is the acceptance test for the fixtures.
2. Capture library and one web scene (`filter`) end to end through assembly,
   with a placeholder pulse track. This proves the beat clock, x11grab, marks
   and xfade before any more scenes are written.
3. Remaining V2 scenes. Cut V2.
4. Audio generator with the three arrangements and effects. Replace the
   placeholder.
5. Cards and V1.
6. Desktop session under Xvfb, starting with the tray host. Then V3 scenes.
7. GIF cuts, `video-check` in CI, `workflow_dispatch` full render.

## 7. Assumptions and open points

- Captions in English, no voice-over. A voice track would need either a
  recorded human or a TTS licence review, and neither is needed for the
  first cut.
- The Nord palette is the house look because the README screenshot uses it.
  The `themes` scene is where the others appear.
- Local auth is used for the demo instead of OIDC so the stack has no external
  dependency. The sign-in form it shows is the real one.
- The desktop tray under Xvfb is the one step that may not render; the
  fallback is stated in section 3.4.
- Kubernetes and Helm get no scene in the first pass; V4 covers them later.
