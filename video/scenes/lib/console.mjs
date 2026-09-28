// Shared helpers for driving the Promview console under Playwright.
//
// Everything here goes through the real UI: the sign-in form, the theme select
// in the status bar, the filter input. A helper that reached into localStorage
// or the API would produce frames the console never shows a user.

import { chromium } from 'playwright';
import { execFile } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { promisify } from 'node:util';

const OUT = new URL('../../out/scenes/', import.meta.url).pathname;
const DEMO = new URL('../../demo/', import.meta.url).pathname;

export const SERVER = process.env.DEMO_SERVER_URL ?? 'http://localhost:8080';
export const OPERATOR = {
  username: process.env.DEMO_USERNAME ?? 'ada',
  password: process.env.DEMO_PASSWORD ?? 'correct-horse-battery-staple',
};

/**
 * Launches Chromium at the capture size. Headed when a display is available
 * (the recorder grabs the X screen), headless for a screenshot.
 */
export async function launch({
  width = 1920,
  height = 1080,
  headless = true,
  kiosk = false,
  display,
} = {}) {
  const browser = await chromium.launch({
    headless,
    env: display ? { ...process.env, DISPLAY: display } : process.env,
    args: [
      `--window-size=${width},${height}`,
      '--window-position=0,0',
      '--hide-scrollbars',
      ...(kiosk ? ['--kiosk'] : []),
      ...(process.env.CHROMIUM_FLAGS ? process.env.CHROMIUM_FLAGS.split(' ') : []),
    ],
  });
  const context = await browser.newContext({
    // Under the recorder the window is the screen; a fixed viewport would
    // letterbox the page inside it.
    ...(kiosk ? { viewport: null } : { viewport: { width, height }, deviceScaleFactor: 1 }),
    colorScheme: 'dark',
    locale: 'en-US',
    timezoneId: 'UTC',
  });
  return { browser, context };
}

/**
 * The instant every scene's clock is frozen at. Decided once per capture
 * session and shared, so the top-bar clock reads the same across scenes and
 * the trailer can cut between them without the time jumping. Timers keep
 * running; only Date stands still, which is what the clock and the Age
 * column read.
 */
export async function sessionTime() {
  const file = `${OUT}session.json`;
  try {
    return JSON.parse(await readFile(file, 'utf8')).time;
  } catch {
    await mkdir(OUT, { recursive: true });
    const time = new Date(Math.floor(Date.now() / 60_000) * 60_000).toISOString();
    await writeFile(file, JSON.stringify({ time }));
    return time;
  }
}

/**
 * Signs the demo operator in over the API and returns the session cookie.
 * The server refuses a sign-in without an Origin, as a browser would send.
 */
export async function apiSession() {
  const response = await fetch(`${SERVER}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Origin: SERVER },
    body: JSON.stringify(OPERATOR),
  });
  if (!response.ok) throw new Error(`sign-in failed: ${response.status}`);
  return response.headers.get('set-cookie').split(';')[0];
}

/**
 * Turns the operator's notification policy on. It is a per-user preference
 * the console keeps server-side, off by default, and the desktop client
 * notifies for nothing until it is on.
 */
export async function enableNotifications() {
  const cookie = await apiSession();
  const headers = { Cookie: cookie, 'Content-Type': 'application/json', Origin: SERVER };
  const preferences = await fetch(`${SERVER}/api/v1/preferences`, { headers }).then((r) =>
    r.json(),
  );
  preferences.notifications = { ...preferences.notifications, enabled: true };
  const saved = await fetch(`${SERVER}/api/v1/preferences`, {
    method: 'PUT',
    headers,
    body: JSON.stringify(preferences),
  });
  if (!saved.ok) throw new Error(`preferences save failed: ${saved.status}`);
}

/** Fires a seed wave from inside a scene, on cue. */
export async function seed(...args) {
  await promisify(execFile)('node', [`${DEMO}seed.mjs`, ...args]);
}

/**
 * Puts the window in real fullscreen through DevTools, so the recording shows
 * the page and none of the browser. `--kiosk` is not honoured under Playwright.
 */
export async function fullscreen(page) {
  const cdp = await page.context().newCDPSession(page);
  const { windowId } = await cdp.send('Browser.getWindowForTarget');
  await cdp.send('Browser.setWindowBounds', { windowId, bounds: { windowState: 'fullscreen' } });
  await cdp.detach();
}

/**
 * Types like a person, one character at a time, by setting the value to each
 * successive prefix. Two reasons this is not `keyboard.type`: a virtual X
 * server has no keyboard layout to spell "!" on, and Chromium reshapes only
 * the appended character when text is inserted, which leaves JetBrains Mono's
 * "!=" ligature half-applied (a blank where the "!" was) until the next full
 * re-layout. Replacing the whole value shapes the whole run every time.
 */
export async function typeText(input, text, { delay = 40 } = {}) {
  for (let i = 1; i <= text.length; i += 1) {
    await input.fill(text.slice(0, i));
    await input.page().waitForTimeout(delay);
  }
}

/** Opens the console and signs in through the form. */
export async function openConsole(context, { signIn = true } = {}) {
  const page = await context.newPage();
  if (page.viewportSize() === null) await fullscreen(page);
  await page.clock.setFixedTime(await sessionTime());
  await page.goto(SERVER, { waitUntil: 'networkidle' });
  if (signIn) {
    const form = page.getByRole('form', { name: 'Sign in' });
    await form.locator('input[name="username"]').fill(OPERATOR.username);
    await form.locator('input[name="password"]').fill(OPERATOR.password);
    await form.getByRole('button', { name: /sign in/i }).click();
    await page.getByRole('button', { name: 'Sign out' }).waitFor();
  }
  return page;
}

/**
 * The state most scenes open on: signed in, Nord, flat list sorted by
 * severity, stream live, nothing focused. Sorting needs a table, so an empty
 * console skips it.
 */
export async function standardConsole(context, { grouping = false } = {}) {
  const page = await openConsole(context);
  await setTheme(page, 'nord');
  await setGrouping(page, grouping);
  if (!grouping && (await page.locator('table.alert-table').count()) > 0) {
    await sortBy(page, 'Severity', 'desc');
  }
  await waitForLiveStream(page);
  await page.keyboard.press('Escape');
  await page.locator('body').click({ position: { x: 900, y: 70 } });
  return page;
}

/** The table row for an alert, by name. */
export function alertRow(page, alertName) {
  return page.locator('tbody tr', { hasText: alertName }).first();
}

/** Opens the detail drawer for an alert and waits for it. */
export async function openDetail(page, alertName) {
  await alertRow(page, alertName).click();
  await page.getByRole('dialog').waitFor();
  return page.getByRole('dialog');
}

/** Opens or closes the View menu. */
export async function toggleViewMenu(page) {
  await page.getByRole('button', { name: 'View' }).click();
}

/** Turns server-side grouping on or off from the View menu. */
export async function setGrouping(page, enabled) {
  await toggleViewMenu(page);
  const box = page.getByRole('checkbox', { name: 'Group alerts' });
  if ((await box.isChecked()) !== enabled) await box.click();
  await page.keyboard.press('Escape');
}

/**
 * Clicks a column header until it sorts the requested way. The console cycles
 * none → asc → desc on each click, which is what an operator would do too.
 */
export async function sortBy(page, column, order = 'desc') {
  const header = page.getByRole('button', { name: `Sort by ${column}` });
  for (let i = 0; i < 3; i += 1) {
    if ((await header.getAttribute('data-direction')) === order) return;
    await header.click();
  }
  throw new Error(`could not sort ${column} ${order}`);
}

/** Turns grouping on and picks one of its presets from the View menu. */
export async function setGroupingPreset(page, preset) {
  await toggleViewMenu(page);
  const box = page.getByRole('checkbox', { name: 'Group alerts' });
  if (!(await box.isChecked())) await box.click();
  await page.getByRole('radio', { name: preset, exact: true }).check();
  await page.keyboard.press('Escape');
}

/** Shows or hides table columns by label, from the View menu. */
export async function setColumns(page, { hide = [], show = [] } = {}) {
  await toggleViewMenu(page);
  for (const [labels, checked] of [
    [hide, false],
    [show, true],
  ]) {
    for (const label of labels) {
      const box = page.getByRole('checkbox', { name: label, exact: true });
      if ((await box.isChecked()) !== checked) await box.click();
    }
  }
  await page.keyboard.press('Escape');
}

/** Picks a palette from the status bar select, the way an operator does. */
export async function setTheme(page, theme) {
  await page.getByLabel('Theme').selectOption(theme);
}

/** Waits until the stream reports live, so a capture never shows "connecting". */
export async function waitForLiveStream(page) {
  await page.locator('.statusbar', { hasText: 'stream: live' }).waitFor();
}
