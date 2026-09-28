# Product videos

Every frame of the Promview videos is produced by the scripts in this
directory: a disposable demo stack, scripted captures under a virtual X
screen, title cards drawn on a canvas, music computed from waveforms, and an
ffmpeg assembly. The plan, storyboards and the reasoning behind each choice are
in [`docs/video-plan.md`](../docs/video-plan.md).

```sh
make video-demo-up            # postgres, promview (local accounts), two Alertmanagers
make video-capture-web        # the ten console scenes, in story order, from an empty console
make video-capture-desktop    # the desktop client, one take, inside a sandbox desktop
make video-assemble TARGET=v2 # v1 trailer, v2 console tour, v3 desktop client, gifs, all, or a scene
make video-check              # what CI runs: scripts, music and cards, no display needed
make video-demo-down
```

Outputs land in `out/` (ignored). Needs Node 20+, Python 3 with numpy and
scipy, ffmpeg, Xvfb and JetBrains Mono; the desktop take also needs openbox,
tint2, snixembed, dunst, xdotool, alacritty and a client built with
`cargo build --release --features tauri/custom-protocol`.

Capture the web scenes on a freshly started stack: the first scene opens on an
empty console and seeds it on camera. `RETAKE=1` on the desktop take gives the
incident a fresh fingerprint, since only a never-seen alert is announced.
