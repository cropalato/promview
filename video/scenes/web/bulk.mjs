// Scene: one decision applied to a selection. Three rows ticked, closed from
// the action bar, reported per alert.

import { runScene } from '../lib/scene.mjs';
import { standardConsole } from '../lib/console.mjs';

export const name = 'bulk';
export const bars = 4;

export const setup = (context) => standardConsole(context);

export async function body({ page, pointer, at, mark }) {
  const boxes = [
    page.getByRole('checkbox', { name: 'Select NodeDiskSpaceLow' }).nth(0),
    page.getByRole('checkbox', { name: 'Select NodeDiskSpaceLow' }).nth(1),
    page.getByRole('checkbox', { name: 'Select KubeNodeNotReady' }),
  ];
  let beat = 1;
  for (const box of boxes) {
    await at(beat, async () => {
      await pointer.moveToElement(box, { ms: 500 });
      mark('click');
      await pointer.click();
    });
    beat += 1.5;
  }
  const bar = page.getByRole('region', { name: 'Bulk actions' });
  await at(7, () =>
    pointer.moveToElement(bar.getByRole('button', { name: 'Close', exact: true }), { ms: 800 }),
  );
  await at(8.5, async () => {
    mark('apply');
    await pointer.click();
    await bar.getByRole('status').waitFor();
  });
  await at(12, () => pointer.moveTo(1100, 700, { ms: 900 }));
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
