// Scene: grouping. Alert name and source fold the fan-out into one row per
// alert; expanding a group loads its members with the same sort and detail.

import { runScene } from '../lib/scene.mjs';
import { setGroupingPreset, standardConsole } from '../lib/console.mjs';

export const name = 'group';
export const bars = 3;

export async function setup(context) {
  const page = await standardConsole(context, { grouping: true });
  await setGroupingPreset(page, 'Alert name and source');
  await page.locator('tr.group-row').first().waitFor();
  await page.locator('body').click({ position: { x: 900, y: 70 } });
  return page;
}

export async function body({ page, pointer, at, mark }) {
  const group = page.locator('tr.group-row', { hasText: 'NodeDiskSpaceLow' }).first();
  await at(1, () => pointer.moveToElement(group, { ms: 900, dx: -500 }));
  await at(3, async () => {
    mark('click');
    await pointer.click();
    await page.locator('tr[aria-level="2"]').first().waitFor();
  });
  await at(6, () => pointer.moveToElement(page.locator('tr[aria-level="2"]').first(), { ms: 700 }));
  await at(8, () => pointer.moveToElement(page.locator('tr[aria-level="2"]').nth(1), { ms: 500 }));
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
