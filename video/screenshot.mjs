#!/usr/bin/env node
// Reproduces docs/images/console.png from the demo stack. This is the
// acceptance test for the fixtures: if the seed and the stack are right, the
// result should be interchangeable with the committed screenshot.
//
//   node video/screenshot.mjs [out/console.png]

import {
  launch,
  openConsole,
  setGrouping,
  setTheme,
  sortBy,
  waitForLiveStream,
} from './scenes/lib/console.mjs';

const out = process.argv[2] ?? new URL('./out/console.png', import.meta.url).pathname;

const { browser, context } = await launch({ width: 1440, height: 900 });
try {
  const page = await openConsole(context);
  await setTheme(page, 'nord');
  await setGrouping(page, false);
  await sortBy(page, 'Severity', 'desc');
  await waitForLiveStream(page);
  await page.waitForTimeout(500);
  await page.screenshot({ path: out });
  console.log(`wrote ${out}`);
} finally {
  await browser.close();
}
