// Runs one desktop scene inside the sandbox desktop (scenes/desktop/session.sh):
// same clock, marks and recorder as the web scenes, but the actors are real X
// windows driven with xdotool and the pointer is the X one.
//
// A scene module exports { name, bars, setup(ctx), body(ctx), teardown?(ctx) }.

import { writeFile, mkdir } from 'node:fs/promises';
import { openSync } from 'node:fs';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { Clock, bars as toBeats, BPM, sleep } from './beat.mjs';
import { startRecorder } from './recorder.mjs';
import { XPointer, xdo, findWindow, waitForWindow, trayIcon, key } from './xdo.mjs';

const OUT = new URL('../../out/scenes/', import.meta.url).pathname;
const DEMO = new URL('../../demo/', import.meta.url).pathname;

export async function runDesktopScene(scene) {
  await mkdir(OUT, { recursive: true });
  const marks = [];
  const children = [];
  const ctx = {
    xdo,
    key,
    findWindow,
    waitForWindow,
    trayIcon,
    seed: (...args) => promisify(execFile)('node', [`${DEMO}seed.mjs`, ...args]),
    /** Starts a program in the sandbox; it is stopped when the scene ends. */
    launch: (command, args = [], { log } = {}) => {
      const out = log ? openSync(log, 'w') : 'ignore';
      const child = spawn(command, args, { stdio: ['ignore', out, out] });
      children.push(child);
      return child;
    },
    /** Hides a window the way an operator does: the WM close, which the client answers by hiding. */
    close: async (geometry) => {
      xdo('windowactivate', '--sync', geometry.id);
      await sleep(150);
      key('alt+F4');
    },
  };
  let recorder;
  try {
    await scene.setup(ctx);
    ctx.pointer = new XPointer();
    recorder = startRecorder(`${OUT}${scene.name}.mkv`, { drawMouse: true });
    await sleep(1000);
    const clock = new Clock();
    ctx.clock = clock;
    ctx.mark = (name, extra = {}) => marks.push({ name, beat: clock.now(), ...extra });
    ctx.at = async (beat, action) => {
      await clock.until(beat);
      return action();
    };
    await scene.body(ctx);
    await clock.until(toBeats(scene.bars));
    const endedAt = Date.now();
    const recordedFrom = recorder.startedAt;
    await recorder.stop();
    recorder = undefined;
    if (scene.teardown) await scene.teardown(ctx);
    await writeFile(
      `${OUT}${scene.name}.marks.json`,
      JSON.stringify(
        {
          name: scene.name,
          bpm: BPM,
          bars: scene.bars,
          offsetMs: clock.t0 - recordedFrom,
          durationMs: endedAt - clock.t0,
          marks,
        },
        null,
        2,
      ),
    );
    console.log(`captured ${scene.name}: ${scene.bars} bars, ${marks.length} marks`);
  } finally {
    if (recorder) await recorder.stop().catch(() => {});
    for (const child of children) child.kill('SIGTERM');
  }
}
