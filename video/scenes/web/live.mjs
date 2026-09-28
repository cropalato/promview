// Scene: the stream. Two consoles side by side. The operator on the left closes
// an alert from the action bar; the console on the right loses the row
// without a refresh.

import { runScene } from '../lib/scene.mjs';
import { setColumns, standardConsole } from '../lib/console.mjs';

export const name = 'live';
export const bars = 3;
export const split = true;

// Half a screen each, so the columns that identify a row keep their room.
const NARROW = ['Instance', 'Last seen', 'Assignee', 'Notes'];

export async function setup(left, right) {
  const operator = await standardConsole(left);
  await setColumns(operator, { hide: NARROW });
  // The layout is stored on the user, but the second window may have loaded
  // its copy before the first one saved; setting it twice is harmless.
  const viewer = await standardConsole(right);
  await setColumns(viewer, { hide: NARROW });
  return [operator, viewer];
}

export async function teardown(operator) {
  await setColumns(operator, { show: NARROW });
}

export async function body({ pages: [operator, viewer], pointers: [pointer], at, mark }) {
  const box = operator.getByRole('checkbox', { name: 'Select KubeHPAMaxedOut' });
  await at(1, () => pointer.moveToElement(box, { ms: 900 }));
  await at(2.5, async () => {
    mark('click');
    await pointer.click();
  });
  const bar = operator.getByRole('region', { name: 'Bulk actions' });
  await at(4, () =>
    pointer.moveToElement(bar.getByRole('button', { name: 'Close', exact: true }), { ms: 700 }),
  );
  await at(5.5, async () => {
    mark('apply');
    await pointer.click();
    await viewer.locator('tbody tr', { hasText: 'KubeHPAMaxedOut' }).waitFor({ state: 'detached' });
    mark('updated');
  });
  await at(8.5, () => pointer.moveTo(480, 760, { ms: 900 }));
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, split, setup, body, teardown });
