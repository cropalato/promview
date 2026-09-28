// The scene clock. Every action in a scene and every note in the music is
// placed on the same grid, so a cut in the assembly lands on a beat and a
// sound effect lands on the frame of the click it belongs to.

export const BPM = 100;
export const BEATS_PER_BAR = 4;
export const BEAT_MS = 60_000 / BPM;
export const FPS = 60;

export const bars = (n) => n * BEATS_PER_BAR;
export const beatsToMs = (beats) => beats * BEAT_MS;

export const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, Math.max(0, ms)));

export class Clock {
  constructor(t0 = Date.now()) {
    this.t0 = t0;
  }

  /** Beats elapsed since the scene started, fractional. */
  now() {
    return (Date.now() - this.t0) / BEAT_MS;
  }

  /** Resolves when the given beat arrives; immediately if it has passed. */
  async until(beat) {
    await sleep(this.t0 + beat * BEAT_MS - Date.now());
  }
}
