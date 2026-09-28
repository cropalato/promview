// Scene: a second Alertmanager. Staging fires into the same table, the source
// column shows both, and grouping by source folds the table in two.

import { runScene } from '../lib/scene.mjs';
import { seed, standardConsole, toggleViewMenu } from '../lib/console.mjs';

export const name = 'sources';
export const bars = 4;

export const setup = (context) => standardConsole(context);

export async function body({ page, pointer, at, mark }) {
  await at(1, () => pointer.moveTo(1590, 560, { ms: 900 }));
  await at(2, async () => {
    mark('wave');
    await seed('--wave', 'staging');
    await page.locator('tbody tr', { hasText: 'staging' }).first().waitFor();
    mark('rows');
  });
  await at(5, () =>
    pointer.moveToElement(page.locator('tbody tr', { hasText: 'staging' }).first(), { ms: 800 }),
  );
  await at(8, () => pointer.moveToElement(page.getByRole('button', { name: 'View' }), { ms: 800 }));
  await at(9.5, async () => {
    mark('click');
    await pointer.click();
  });
  await at(10.5, () =>
    pointer.moveToElement(page.getByRole('checkbox', { name: 'Group alerts' }), { ms: 500 }),
  );
  await at(11.5, async () => {
    mark('click');
    await pointer.click();
  });
  await at(12.5, () =>
    pointer.moveToElement(page.getByRole('radio', { name: 'Source', exact: true }), { ms: 500 }),
  );
  await at(13.5, async () => {
    mark('click');
    await pointer.click();
  });
  await at(14.5, async () => {
    await page.keyboard.press('Escape');
    await pointer.moveTo(1100, 640, { ms: 700 });
  });
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
