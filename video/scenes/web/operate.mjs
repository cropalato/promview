// Scene: operating on an alert. Acknowledge, hand it to a rota, leave a note,
// and see the three land in the timeline under the operator's name.

import { runScene } from '../lib/scene.mjs';
import { openDetail, standardConsole, typeText } from '../lib/console.mjs';

export const name = 'operate';
export const bars = 6;

export async function setup(context) {
  const page = await standardConsole(context);
  await openDetail(page, 'PostgresReplicationLag');
  return page;
}

export async function body({ page, pointer, at, mark }) {
  const drawer = page.getByRole('dialog');
  const click = async (locator, ms = 600, extra) => {
    await pointer.moveToElement(locator, { ms, ...extra });
    mark('click');
    await pointer.click();
  };

  await at(1, () => click(drawer.getByRole('button', { name: 'Acknowledge alert' }), 900));
  await at(4, () => click(drawer.locator('#alert-assignee'), 700));
  await at(5, () => typeText(drawer.locator('#alert-assignee'), 'platform-rota'));
  await at(7, () => click(drawer.getByRole('button', { name: 'Save' })));
  await at(9.5, () => click(drawer.locator('#alert-note'), 700));
  await at(10.5, () =>
    typeText(drawer.locator('#alert-note'), 'Paged the vendor, awaiting callback', { delay: 35 }),
  );
  await at(14, () => click(drawer.getByRole('button', { name: 'Add note' })));
  await at(16.5, () => click(page.getByRole('tab', { name: 'Timeline' }), 700));
  await at(19, () => pointer.moveTo(1500, 760, { ms: 900 }));
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
