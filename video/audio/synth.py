#!/usr/bin/env python3
"""Music and sound effects for the Promview videos, from arithmetic alone.

No samples, no licensed audio: every sound is a waveform computed here, so the
soundtrack is owned by the repository and released with it. The musical
intent follows the message of the videos: a pulse, steady, never a drop.

    python3 video/audio/synth.py scene    <marks.json> <out.wav> [--style tour|desktop]
    python3 video/audio/synth.py sequence <scenes.json> <out.wav> [--style tour|desktop]
    python3 video/audio/synth.py trailer  <out.wav> [--bars 12.5]
    python3 video/audio/synth.py preview  <out.wav>

`scenes.json` is a list of the scenes' marks files' contents, in order. One
chord per scene, from a four-chord loop in D minor; the change lands on the
cut. Sound effects are placed at the marks the scene scripts logged.
"""

import json
import sys
import wave

import numpy as np
from scipy.signal import butter, sosfilt, sosfilt_zi

RATE = 48_000
BPM = 100
BEAT = 60 / BPM
BAR = 4 * BEAT

# --- pitch -------------------------------------------------------------------

_NAMES = {"C": 0, "D": 2, "E": 4, "F": 5, "G": 7, "A": 9, "B": 11}


def hz(note):
    """'Bb3' -> frequency. Middle C is C4."""
    name, octave = note[:-1], int(note[-1])
    semis = _NAMES[name[0]] + (1 if name.endswith("#") else 0) - (1 if name.endswith("b") else 0)
    return 440.0 * 2 ** ((octave - 4) + (semis - 9) / 12)


# D minor, one chord per scene. Voiced low and close so the pad sits under
# the captions rather than competing with them.
CHORDS = {
    "i": ["D3", "F3", "A3", "D4"],
    "VI": ["Bb2", "D3", "F3", "Bb3"],
    "III": ["F3", "A3", "C4", "F4"],
    "VII": ["C3", "E3", "G3", "C4"],
}
PROGRESSION = ["i", "VI", "III", "VII"]

# --- helpers -----------------------------------------------------------------


def n_samples(seconds):
    return int(round(seconds * RATE))


def env(length, attack=0.002, decay=0.2):
    """Attack then exponential decay, in seconds."""
    t = np.arange(length) / RATE
    return np.clip(t / attack, 0, 1) * np.exp(-np.clip(t - attack, 0, None) / decay)


def fade(length, attack, release):
    """Linear in, sustain, linear out."""
    a = min(n_samples(attack), length // 2)
    r = min(n_samples(release), length // 2)
    shape = np.ones(length)
    shape[:a] = np.linspace(0, 1, a, endpoint=False)
    shape[length - r :] = np.linspace(1, 0, r)
    return shape


def lowpass(signal, cutoff, order=2):
    sos = butter(order, cutoff, btype="low", fs=RATE, output="sos")
    return sosfilt(sos, signal)


def bandpass(signal, low, high):
    sos = butter(2, [low, high], btype="band", fs=RATE, output="sos")
    return sosfilt(sos, signal)


def sweep_lowpass(signal, cutoffs, chunk=0.02):
    """A low-pass whose cutoff moves: filtered in short chunks with the filter
    state carried across, so the sweep is continuous rather than stepped."""
    out = np.empty_like(signal)
    size = n_samples(chunk)
    zi = None
    for start in range(0, len(signal), size):
        end = min(start + size, len(signal))
        fc = float(cutoffs[min(start, len(cutoffs) - 1)])
        sos = butter(2, fc, btype="low", fs=RATE, output="sos")
        if zi is None:
            zi = sosfilt_zi(sos) * signal[0]
        out[start:end], zi = sosfilt(sos, signal[start:end], zi=zi)
    return out


def place(track, sample, at_seconds, gain=1.0):
    start = n_samples(at_seconds)
    if start < 0 or start >= len(track):
        return
    end = min(len(track), start + len(sample))
    track[start:end] += sample[: end - start] * gain


# --- instruments -------------------------------------------------------------


def pulse(length=n_samples(0.35)):
    """A soft kick: a sine whose pitch falls from 150 Hz to 48 Hz in 60 ms.
    The heartbeat under the logo's pulse line."""
    t = np.arange(length) / RATE
    freq = 48 + 102 * np.exp(-t / 0.02)
    phase = 2 * np.pi * np.cumsum(freq) / RATE
    return np.sin(phase) * env(length, attack=0.001, decay=0.12)


def tick(length=n_samples(0.012), seed=3):
    """Band-passed noise, 8 ms. A clock, not a hi-hat."""
    noise = np.random.default_rng(seed).standard_normal(length)
    return bandpass(noise, 5_000, 9_000) * env(length, attack=0.0005, decay=0.004)


def saw(freq, length, phase=0.0):
    t = np.arange(length) / RATE
    return 2 * ((t * freq + phase) % 1.0) - 1


def pad(chord, seconds, cutoff=900.0, seed=11):
    """Seven detuned sawtooths per note through a low-pass. Warmth."""
    length = n_samples(seconds)
    rng = np.random.default_rng(seed)
    detune_cents = np.array([-12, -8, -4, 0, 4, 8, 12])
    mix = np.zeros(length)
    for note in CHORDS[chord]:
        base = hz(note)
        for cents in detune_cents:
            mix += saw(base * 2 ** (cents / 1200), length, phase=rng.random())
    mix /= len(CHORDS[chord]) * len(detune_cents)
    # The cutoff breathes slowly, so a held chord is not a held tone.
    t = np.arange(length) / RATE
    cutoffs = cutoff * (1 + 0.25 * np.sin(2 * np.pi * 0.08 * t))
    return sweep_lowpass(mix, cutoffs) * fade(length, 0.4, 0.3)


def sub(chord, seconds):
    """A sine an octave under the chord root. Weight, felt not heard."""
    length = n_samples(seconds)
    t = np.arange(length) / RATE
    return np.sin(2 * np.pi * (hz(CHORDS[chord][0]) / 2) * t) * fade(length, 0.05, 0.2)


def riser(seconds, seed=5):
    """Filtered noise with a rising cutoff and level. Trailer only."""
    length = n_samples(seconds)
    noise = np.random.default_rng(seed).standard_normal(length)
    t = np.linspace(0, 1, length)
    cutoffs = 200 * (9_000 / 200) ** t
    return sweep_lowpass(noise, cutoffs) * (t**2)


def sidechain(length, beats, depth=0.45, recovery=0.14):
    """Gain that dips on every beat and recovers. What makes the pad breathe."""
    gain = np.ones(length)
    kernel_len = n_samples(recovery * 5)
    kernel = depth * np.exp(-np.arange(kernel_len) / RATE / recovery)
    for beat_time in beats:
        start = n_samples(beat_time)
        end = min(length, start + kernel_len)
        if start < length:
            gain[start:end] -= kernel[: end - start]
    return np.clip(gain, 0, 1)


# --- sound effects ---------------------------------------------------------------


def fx_click(length=n_samples(0.012)):
    noise = np.random.default_rng(7).standard_normal(length)
    return noise * env(length, attack=0.0005, decay=0.003)


def fx_ping(length=n_samples(0.5)):
    """Two notes, a fifth apart. Something arrived."""
    t = np.arange(length) / RATE
    tone = 0.5 * np.sin(2 * np.pi * hz("A5") * t) + 0.35 * np.sin(2 * np.pi * hz("E6") * t)
    return tone * env(length, attack=0.003, decay=0.14)


def fx_low(length=n_samples(0.6)):
    """One low note. A critical arrived."""
    t = np.arange(length) / RATE
    return np.sin(2 * np.pi * hz("D3") * t) * env(length, attack=0.005, decay=0.25)


def fx_done(length=n_samples(0.35)):
    """A short rising pair. Applied."""
    t = np.arange(length) / RATE
    tone = np.sin(2 * np.pi * hz("D5") * t) * (t < 0.12) + np.sin(2 * np.pi * hz("A5") * t) * (t >= 0.12)
    return tone * env(length, attack=0.003, decay=0.1)


# What each mark name sounds like, and how loud.
EFFECTS = {
    "click": (fx_click, 0.45),
    "apply": (fx_done, 0.35),
    "rows": (fx_ping, 0.35),
    "updated": (fx_ping, 0.25),
    "critical": (fx_low, 0.45),
}

# --- arrangement ---------------------------------------------------------------

LEVELS = {"pulse": 0.55, "tick": 0.10, "pad": 0.22, "sub": 0.28}


def render(sections, total_bars, effects=(), silence=(), riser_at=None):
    """Mixes layers over `sections` ({start, end} in bars, chord, layers).

    `effects` are (seconds, name); `silence` is a list of (start, end) in
    seconds where every track is cut; `riser_at` is (start_bar, end_bar).
    """
    total = n_samples(total_bars * BAR)
    beats = [b * BEAT for b in range(int(np.ceil(total_bars * 4)))]
    tracks = {name: np.zeros(total) for name in LEVELS}
    for section in sections:
        start, end = section["start"] * BAR, section["end"] * BAR
        seconds = end - start
        layers = section["layers"]
        if "pad" in layers:
            place(tracks["pad"], pad(section["chord"], seconds), start)
        if "sub" in layers:
            place(tracks["sub"], sub(section["chord"], seconds), start)
        if "pulse" in layers:
            for b in beats:
                if start <= b < end:
                    place(tracks["pulse"], pulse(), b)
        if "tick" in layers:
            for b in beats:
                for eighth in (b, b + BEAT / 2):
                    if start <= eighth < end:
                        place(tracks["tick"], tick(), eighth)

    duck = sidechain(total, [b for b in beats])
    mix = (
        tracks["pulse"] * LEVELS["pulse"]
        + tracks["tick"] * LEVELS["tick"]
        + tracks["pad"] * LEVELS["pad"] * duck
        + tracks["sub"] * LEVELS["sub"] * duck
    )
    if riser_at is not None:
        start, end = riser_at[0] * BAR, riser_at[1] * BAR
        place(mix, riser(end - start), start, gain=0.3)

    fx = np.zeros(total)
    for at, name in effects:
        if name in EFFECTS:
            sample, gain = EFFECTS[name]
            place(fx, sample(), at, gain)
    mix += fx

    for start, end in silence:
        mix[n_samples(start) : n_samples(end)] = 0
    return mix


STYLES = {
    "tour": {"pulse", "tick", "pad", "sub"},
    "desktop": {"tick", "pad", "sub"},
}


def sections_for(scenes, style):
    """One section per scene, chords cycling through the progression."""
    sections, cursor = [], 0.0
    for i, scene in enumerate(scenes):
        sections.append(
            {"start": cursor, "end": cursor + scene["bars"], "chord": PROGRESSION[i % 4], "layers": STYLES[style]}
        )
        cursor += scene["bars"]
    return sections, cursor


def effects_for(scenes):
    effects, cursor = [], 0.0
    for scene in scenes:
        for m in scene["marks"]:
            effects.append((cursor + m["beat"] * BEAT, m["name"]))
        cursor += scene["bars"] * BAR
    return effects


def trailer(bars=12.5):
    """Builds from the pulse alone to the full bed, rises, cuts to silence for
    a tenth of a second, and lands on the end card."""
    sections = [
        {"start": 0, "end": 1.5, "chord": "i", "layers": {"pulse"}},
        {"start": 1.5, "end": 3, "chord": "i", "layers": {"pulse", "pad"}},
        {"start": 3, "end": 4.5, "chord": "VI", "layers": {"pulse", "pad", "tick"}},
        {"start": 4.5, "end": 5.5, "chord": "III", "layers": {"pulse", "pad", "tick", "sub"}},
        {"start": 5.5, "end": 7, "chord": "VII", "layers": {"pulse", "pad", "tick", "sub"}},
        {"start": 7, "end": 8, "chord": "i", "layers": {"pulse", "pad", "tick", "sub"}},
        {"start": 8, "end": 9, "chord": "VI", "layers": {"pulse", "pad", "tick", "sub"}},
        {"start": 9, "end": 10, "chord": "III", "layers": {"pulse", "pad", "tick", "sub"}},
        {"start": 10, "end": 11.5, "chord": "VII", "layers": {"pulse", "pad", "tick"}},
        {"start": 11.5, "end": bars, "chord": "i", "layers": {"pad", "sub", "pulse"}},
    ]
    cut = 11.5 * BAR
    return render(sections, bars, silence=[(cut - 0.1, cut)], riser_at=(10, 11.5))


def write(path, track):
    peak = np.max(np.abs(track)) or 1.0
    normalized = track / peak * 0.89  # -1 dBFS; loudness is settled by the assembly
    with wave.open(path, "wb") as out:
        out.setnchannels(1)
        out.setsampwidth(2)
        out.setframerate(RATE)
        out.writeframes((normalized * 32767).astype("<i2").tobytes())
    print(f"wrote {path} ({len(track) / RATE:.1f}s)")


def main(argv):
    command = argv[0]
    style = argv[argv.index("--style") + 1] if "--style" in argv else "tour"
    if command == "scene":
        with open(argv[1]) as f:
            scenes = [json.load(f)]
        sections, total = sections_for(scenes, style)
        write(argv[2], render(sections, total, effects_for(scenes)))
    elif command == "sequence":
        with open(argv[1]) as f:
            scenes = json.load(f)
        sections, total = sections_for(scenes, style)
        write(argv[2], render(sections, total, effects_for(scenes)))
    elif command == "trailer":
        bars = float(argv[argv.index("--bars") + 1]) if "--bars" in argv else 12.5
        write(argv[1], trailer(bars))
    elif command == "preview":
        # Eight bars of each bed, then the trailer: something to listen to
        # before any video exists.
        fake = [{"bars": 2, "marks": [{"name": "click", "beat": 2}, {"name": "rows", "beat": 5}]}] * 4
        tour_sections, total = sections_for(fake, "tour")
        desk_sections, _ = sections_for(fake, "desktop")
        write(
            argv[1],
            np.concatenate(
                [
                    render(tour_sections, total, effects_for(fake)),
                    np.zeros(n_samples(1)),
                    render(desk_sections, total, effects_for(fake)),
                    np.zeros(n_samples(1)),
                    trailer(),
                ]
            ),
        )
    else:
        raise SystemExit(__doc__)


if __name__ == "__main__":
    main(sys.argv[1:])
