// Scene: the filter bar. A label selector typed the way an alerting rule is
// written, applied server-side, and the count and rows following it.

import { runScene } from '../lib/scene.mjs';
import { standardConsole, typeText } from '../lib/console.mjs';

export const name = 'filter';
export const bars = 4;

export const setup = (context) => standardConsole(context);

export async function body({ page, pointer, at, mark }) {
  const input = page.getByLabel('Filter alerts by label expression');

  await at(1, () => pointer.moveToElement(input, { ms: 700, dx: -300 }));
  await at(2.5, async () => {
    mark('click');
    await pointer.click();
  });
  await at(3, () => typeText(input, '{severity="critical", team!="infra"}'));
  await at(6, async () => {
    mark('apply');
    await page.keyboard.press('Enter');
  });
  await at(8, () => pointer.moveTo(960, 640, { ms: 900 }));
  await at(11, () => pointer.moveToElement(page.locator('tbody tr').first(), { ms: 700 }));
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
