// Scene: a silence, written to the source's Alertmanager from the console,
// and reconciliation flagging the row a few seconds later.

import { runScene } from '../lib/scene.mjs';
import { openDetail, standardConsole, typeText } from '../lib/console.mjs';

export const name = 'silence';
export const bars = 5;

export async function setup(context) {
  const page = await standardConsole(context);
  await openDetail(page, 'TargetDown');
  return page;
}

export async function body({ page, pointer, at, mark }) {
  const drawer = page.getByRole('dialog').first();
  await at(1, () =>
    pointer.moveToElement(drawer.getByRole('button', { name: 'Silence alert…' }), { ms: 900 }),
  );
  await at(2.5, async () => {
    mark('click');
    await pointer.click();
    await page.getByRole('dialog', { name: /silence/i }).waitFor();
  });
  const dialog = page.getByRole('dialog', { name: /silence/i });
  await at(5, async () => {
    await pointer.moveToElement(dialog.locator('input[type="text"]').first(), { ms: 700 });
    mark('click');
    await pointer.click();
  });
  await at(6, () =>
    typeText(dialog.locator('input[type="text"]').first(), 'node-14 hardware swap, INFRA-482', {
      delay: 35,
    }),
  );
  await at(9.5, () =>
    pointer.moveToElement(dialog.getByRole('button', { name: 'Silence', exact: true }), {
      ms: 700,
    }),
  );
  await at(10.5, async () => {
    mark('apply');
    await pointer.click();
    await dialog.getByRole('button', { name: 'Close', exact: true }).waitFor();
  });
  await at(13.5, () =>
    pointer.moveToElement(dialog.getByRole('button', { name: 'Close', exact: true }), { ms: 600 }),
  );
  await at(14.5, async () => {
    mark('click');
    await pointer.click();
  });
  await at(16, () =>
    pointer.moveToElement(page.getByRole('button', { name: 'Close alert detail' }), { ms: 700 }),
  );
  await at(17, async () => {
    mark('click');
    await pointer.click();
  });
  await at(18, () =>
    pointer.moveToElement(page.locator('tbody tr', { hasText: 'TargetDown' }), {
      ms: 900,
      dx: -300,
    }),
  );
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
