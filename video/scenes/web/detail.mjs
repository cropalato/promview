// Scene: the detail drawer. When it started, what it carries, what happened
// to it, and the payload exactly as Alertmanager sent it.

import { runScene } from '../lib/scene.mjs';
import { alertRow, standardConsole } from '../lib/console.mjs';

export const name = 'detail';
export const bars = 5;

export const setup = (context) => standardConsole(context);

export async function body({ page, pointer, at, mark }) {
  const row = alertRow(page, 'PostgresReplicationLag');
  await at(1, () => pointer.moveToElement(row, { ms: 900, dx: -400 }));
  await at(2.5, async () => {
    mark('click');
    await pointer.click();
    await page.getByRole('dialog').waitFor();
  });
  const tab = (label) => page.getByRole('tab', { name: label });
  await at(5.5, () => pointer.moveToElement(tab('Timeline'), { ms: 700 }));
  await at(6.5, async () => {
    mark('click');
    await pointer.click();
  });
  await at(10, () => pointer.moveToElement(tab('Raw'), { ms: 500 }));
  await at(11, async () => {
    mark('click');
    await pointer.click();
  });
  await at(14.5, () => pointer.moveToElement(tab('Overview'), { ms: 500 }));
  await at(15.5, async () => {
    mark('click');
    await pointer.click();
  });
  await at(17, () => pointer.moveTo(1500, 700, { ms: 900 }));
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
