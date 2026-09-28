// Runs one scene: prepares the console off camera, starts the recorder, runs
// the scripted actions against the beat clock, and writes the marks the
// assembly and the sound effects are placed by.
//
// A scene module exports { name, bars, setup(context), body(ctx) }. `setup`
// returns the page in the state the first frame should show. `body` receives
// { page, pointer, clock, at, mark } and is expected to finish before the last
// bar; the runner holds the final frame until then.

import { writeFile, mkdir } from 'node:fs/promises';
import { Clock, bars as toBeats, BPM, sleep } from './beat.mjs';
import { launch } from './console.mjs';
import { Pointer } from './pointer.mjs';
import { startRecorder } from './recorder.mjs';

const OUT = new URL('../../out/scenes/', import.meta.url).pathname;

export async function runScene(scene) {
  await mkdir(OUT, { recursive: true });
  // A split scene films two consoles side by side: two X screens of half
  // width (DISPLAY :n.0 and :n.1), two browsers, two recorders, joined by the
  // assembly. Everything else is one screen.
  const split = scene.split === true;
  const displays = split ? [`${process.env.DISPLAY}.0`, `${process.env.DISPLAY}.1`] : [undefined];
  const width = split ? 960 : 1920;
  const browsers = [];
  const recorders = [];
  const marks = [];
  try {
    const contexts = [];
    for (const display of displays) {
      const { browser, context } = await launch({ headless: false, kiosk: true, display, width });
      browsers.push(browser);
      contexts.push(context);
    }
    const pages = split ? await scene.setup(...contexts) : [await scene.setup(contexts[0])];
    const pointers = [];
    for (const page of pages) {
      const pointer = new Pointer(page, { x: width / 2, y: 540 });
      await pointer.install();
      pointers.push(pointer);
    }

    const files = split
      ? ['left', 'right'].map((side) => `${OUT}${scene.name}.${side}.mkv`)
      : [`${OUT}${scene.name}.mkv`];
    files.forEach((file, i) =>
      recorders.push(startRecorder(file, { display: displays[i], width })),
    );
    await sleep(1000);
    const clock = new Clock();
    const mark = (name, extra = {}) => marks.push({ name, beat: clock.now(), ...extra });
    const at = async (beat, action) => {
      await clock.until(beat);
      return action();
    };

    await scene.body({
      page: pages[0],
      pointer: pointers[0],
      pages,
      pointers,
      clock,
      at,
      mark,
    });
    await clock.until(toBeats(scene.bars));
    const endedAt = Date.now();
    const recordedFrom = recorders[0].startedAt;
    for (const recorder of recorders.splice(0)) await recorder.stop();
    // Off camera: undo anything the scene changed that later scenes should
    // not inherit, such as a column layout stored on the user.
    if (scene.teardown) await scene.teardown(...pages);

    await writeFile(
      `${OUT}${scene.name}.marks.json`,
      JSON.stringify(
        {
          name: scene.name,
          split,
          bpm: BPM,
          bars: scene.bars,
          // Where beat 0 sits in the recording, so the assembly can trim to it.
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
    // A scene that failed must not leave ffmpeg holding the display.
    for (const recorder of recorders) await recorder.stop().catch(() => {});
    for (const browser of browsers) await browser.close();
  }
}
