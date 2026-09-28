// Scene: cold open. An empty console with a live stream, then the production
// Alertmanager delivers and the rows and counts arrive. Needs a fresh stack:
// this is the first scene of the story and seeds the base wave itself.

import { runScene } from '../lib/scene.mjs';
import { seed, standardConsole } from '../lib/console.mjs';

export const name = 'ingest';
export const bars = 6;

export const setup = (context) => standardConsole(context);

export async function body({ page, pointer, at, mark }) {
  await at(2, () => pointer.moveTo(1100, 620, { ms: 1200 }));
  await at(8, async () => {
    mark('wave');
    await seed('--wave', 'base');
    await page.locator('tbody tr').first().waitFor();
    mark('rows');
  });
  await at(14, () => pointer.moveToElement(page.locator('.sev-strip li').first(), { ms: 900 }));
  await at(18, () => pointer.moveToElement(page.locator('tbody tr').nth(3), { ms: 900 }));
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
