// Driving a real X desktop with xdotool: the pointer, clicks, keys, and window
// geometry. The desktop scenes use this where the web scenes use Playwright.

import { execFileSync, spawn } from 'node:child_process';
import { appendFileSync } from 'node:fs';
import { sleep } from './beat.mjs';

export const xdo = (...args) =>
  execFileSync('xdotool', args.map(String), { encoding: 'utf8' }).trim();

/** Geometry of the first window matching an xdotool search, or null. */
export function findWindow(...search) {
  let ids;
  try {
    ids = xdo('search', ...search)
      .split('\n')
      .filter(Boolean);
  } catch {
    return null;
  }
  for (const id of ids) {
    const info = Object.fromEntries(
      xdo('getwindowgeometry', '--shell', id)
        .split('\n')
        .map((line) => line.split('=')),
    );
    const geometry = {
      id,
      x: Number(info.X),
      y: Number(info.Y),
      width: Number(info.WIDTH),
      height: Number(info.HEIGHT),
    };
    if (geometry.width > 1 && geometry.height > 1) return geometry;
  }
  return null;
}

/**
 * Finds a window by name in the whole tree, including windows embedded in
 * another client's (a tray icon inside the panel), which xdotool's search
 * does not reach. Absolute position from xwininfo.
 */
export function findInTree(namePattern) {
  const tree = execFileSync('xwininfo', ['-root', '-tree'], { encoding: 'utf8' });
  const pattern = new RegExp(namePattern);
  for (const line of tree.split('\n')) {
    // Name or class may carry the label, so the whole line is matched.
    const m = /^\s*(0x[0-9a-f]+) "([^"]*)".*?(\d+)x(\d+)[+-]\d+[+-]\d+\s+\+(\d+)\+(\d+)\s*$/.exec(
      line,
    );
    if (m && pattern.test(line)) {
      return {
        id: m[1],
        name: m[2],
        width: Number(m[3]),
        height: Number(m[4]),
        x: Number(m[5]),
        y: Number(m[6]),
      };
    }
  }
  return null;
}

export async function waitForWindow(search, { timeoutMs = 15_000, tree = false } = {}) {
  const until = Date.now() + timeoutMs;
  while (Date.now() < until) {
    const found = tree ? findInTree(search[0]) : findWindow(...search);
    if (found) return found;
    await sleep(200);
  }
  throw new Error(`no window for ${search.join(' ')}`);
}

/**
 * The client's tray icon, where the panel put it. Looked up on every use:
 * the icon window exists before the panel positions it, so a geometry read
 * at startup can point at the corner of the screen.
 */
export async function trayIcon({ timeoutMs = 20_000 } = {}) {
  const until = Date.now() + timeoutMs;
  while (Date.now() < until) {
    const found = findInTree('tray-icon tray app promview');
    if (found && found.x > 100 && found.y > 100) return found;
    await sleep(200);
  }
  throw new Error('tray icon never reached the panel');
}

const easeOutCubic = (t) => 1 - (1 - t) ** 3;

/** The real X pointer, moved on an ease-out curve like the page cursor. */
export class XPointer {
  constructor() {
    const [x, y] = xdo('getmouselocation', '--shell')
      .split('\n')
      .slice(0, 2)
      .map((l) => Number(l.split('=')[1]));
    this.x = x;
    this.y = y;
  }

  async moveTo(x, y, { ms = 600 } = {}) {
    const x0 = this.x,
      y0 = this.y;
    const frames = Math.max(1, Math.round(ms / 16));
    const started = Date.now();
    for (let i = 1; i <= frames; i += 1) {
      const t = easeOutCubic(i / frames);
      this.x = Math.round(x0 + (x - x0) * t);
      this.y = Math.round(y0 + (y - y0) * t);
      xdo('mousemove', this.x, this.y);
      await sleep(started + (i * ms) / frames - Date.now());
    }
  }

  moveToWindow(geometry, { ms, dx = 0, dy = 0 } = {}) {
    return this.moveTo(
      geometry.x + geometry.width / 2 + dx,
      geometry.y + geometry.height / 2 + dy,
      { ms },
    );
  }

  async click(button = 1) {
    xdo('click', button);
    await sleep(80);
  }
}

/**
 * Clicks the window manager's close button on a window's title bar. Under the
 * openbox theme in use the bar is 26px tall and the button sits at the right.
 * This is a WM_DELETE_WINDOW the client may answer by hiding, unlike
 * `xdotool windowclose`, which destroys the window outright.
 */
export async function clickClose(pointer, geometry, { ms = 700 } = {}) {
  await pointer.moveTo(geometry.x + geometry.width - 13, geometry.y - 13, { ms });
  await pointer.click();
}

/** Types through xdotool at a steady pace. */
export async function typeKeys(text, { delay = 45 } = {}) {
  xdo('type', '--delay', delay, text);
}

export const key = (...keys) => xdo('key', '--delay', 120, ...keys);

/** Starts a program in the sandbox and returns a handle that kills it. */
export function launch(command, args = [], { log } = {}) {
  const child = spawn(command, args, {
    stdio: ['ignore', log ?? 'ignore', log ?? 'ignore'],
    detached: false,
  });
  return { child, stop: () => child.kill('SIGTERM') };
}

/**
 * With VIDEO_PROBE set, records where the keyboard focus and the pointer are
 * and what is on screen, under a label. For a take that works on one machine
 * and not on another: the video shows the symptom, this shows the state. Off
 * by default because a screenshot costs time the scene's clock does not
 * know about, and it never throws.
 */
export function probe(label) {
  if (!process.env.VIDEO_PROBE) return;
  const dir = new URL('../../out/scenes/', import.meta.url).pathname;
  const ask = (...args) => {
    try {
      return xdo(...args).replace(/\n/g, ' ');
    } catch (err) {
      return `(${err.message.split('\n')[0]})`;
    }
  };
  const focus = ask('getwindowfocus');
  const line = [
    label,
    `focus=${focus} "${ask('getwindowfocus', 'getwindowname')}"`,
    `active="${ask('getactivewindow', 'getwindowname')}"`,
    `mouse=${ask('getmouselocation', '--shell')}`,
  ].join(' | ');
  try {
    appendFileSync(`${dir}tour.probe.log`, `${line}\n`);
    screenshot(`${dir}probe-${label}.png`);
  } catch (err) {
    console.error(`probe ${label}: ${err.message.split('\n')[0]}`);
  }
}

export const screenshot = (path) =>
  execFileSync('ffmpeg', [
    '-v',
    'error',
    '-y',
    '-f',
    'x11grab',
    '-video_size',
    '1920x1080',
    '-draw_mouse',
    '1',
    '-i',
    process.env.DISPLAY,
    '-frames:v',
    '1',
    path,
  ]);
