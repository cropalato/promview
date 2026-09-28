// Scene: palettes, from the status bar. The console is looked at for hours;
// the choice follows the operator between machines.

import { runScene } from '../lib/scene.mjs';
import { setTheme, standardConsole } from '../lib/console.mjs';

export const name = 'themes';
export const bars = 3;

export const setup = (context) => standardConsole(context);

export async function body({ page, pointer, at, mark }) {
  await at(0.5, () => pointer.moveToElement(page.getByLabel('Theme'), { ms: 1000 }));
  let beat = 3;
  for (const theme of ['gruvbox', 'solarized-light', 'high-contrast', 'nord']) {
    await at(beat, async () => {
      mark('click', { theme });
      await setTheme(page, theme);
    });
    beat += 2;
  }
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runScene({ name, bars, setup, body });
