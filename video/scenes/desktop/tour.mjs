// Scene: the desktop client, in one take. The tray, the menu, a local sign-in
// inside the window, the compact always-on-top window over a terminal, a
// warning resolving in it, and a native notification for a new critical.
// The build cuts captions and cards around it by beat.

import { runDesktopScene } from '../lib/desktopScene.mjs';
import { typeKeys, probe, clientOrigin } from '../lib/xdo.mjs';
import { sleep } from '../lib/beat.mjs';
import { OPERATOR, enableNotifications } from '../lib/console.mjs';

export const name = 'tour';
export const bars = 12;

const VIDEO = new URL('../../', import.meta.url).pathname;
const MAIN = { x: 320, y: 120 }; // where the console window sits; the form coordinates below are relative to it
const FORM = { username: [640, 429], password: [640, 493], submit: [640, 537] };

export async function setup(ctx) {
  await enableNotifications();
  ctx.xdo('mousemove', 700, 500);
  // A terminal following the server's log, for the compact window to sit over.
  ctx.launch('alacritty', [
    '--config-file',
    process.env.ALACRITTY_CONFIG,
    '-e',
    'sh',
    '-c',
    `cd ${VIDEO} && docker compose -f demo/compose.yaml logs -f --tail 40 app alertmanager-production alertmanager-staging`,
  ]);
  const term = await ctx.waitForWindow(['--class', 'Alacritty']);
  ctx.xdo('windowmove', term.id, 40, 70);
  ctx.xdo('windowsize', term.id, 1400, 860);

  ctx.launch(process.env.PROMVIEW_DESKTOP_BIN, [], {
    log: `${VIDEO}out/scenes/tour.desktop.log`,
  });
  ctx.main = await ctx.waitForWindow(['--name', '^Promview$']);
  ctx.xdo('windowmove', ctx.main.id, MAIN.x, MAIN.y);
  await sleep(2500);
  ctx.main = ctx.findWindow('--name', '^Promview$');
  // The story starts from the tray: the window the client opened is hidden.
  await ctx.close(ctx.main);
  await ctx.trayIcon();
  await sleep(1500);
}

export async function body({
  pointer,
  at,
  mark,
  xdo,
  key,
  seed,
  trayIcon,
  findWindow,
  close,
  main,
}) {
  // Read when the click is made, not from the geometry taken earlier.
  const rel = ([x, y]) => {
    const origin = clientOrigin(main.id);
    return [origin.x + x, origin.y + y];
  };
  const menu = async (downs) => {
    const tray = await trayIcon();
    await pointer.moveToWindow(tray, { ms: 900 });
    await sleep(400);
    mark('click');
    await pointer.click(3);
    await sleep(700);
    for (let i = 0; i < downs; i += 1) {
      key('Down');
      await sleep(260);
    }
    await sleep(500);
    mark('click');
    key('Return');
  };

  // Tray and menu: open the console. The client reopens it where the window
  // manager likes; it is put back where the story wants it before it draws.
  await at(2, () => menu(2));
  await at(4.8, async () => {
    main = await visibleSoon(xdo, '^Promview$');
    xdo('windowmove', main.id, MAIN.x, MAIN.y);
    await sleep(300);
    main = findWindow('--name', '^Promview$');
    xdo('windowactivate', '--sync', main.id);
  });

  // Sign in, inside the window.
  await at(8, async () => {
    await pointer.moveTo(...rel(FORM.username), { ms: 800 });
    mark('click');
    await pointer.click();
    probe('1-username-clicked');
    await typeKeys(OPERATOR.username, { delay: 70 });
    probe('2-username-typed');
  });
  await at(10.5, async () => {
    await pointer.moveTo(...rel(FORM.password), { ms: 500 });
    mark('click');
    await pointer.click();
    probe('3-password-clicked');
    await typeKeys(OPERATOR.password, { delay: 22 });
    probe('4-password-typed');
  });
  await at(13, async () => {
    await pointer.moveTo(...rel(FORM.submit), { ms: 500 });
    mark('apply');
    await pointer.click();
    await sleep(1500);
    probe('5-submitted');
  });
  await at(15.5, () => pointer.moveTo(...rel([640, 620]), { ms: 900 }));

  // Compact window over the terminal.
  await at(19, () => menu(3));
  await at(21.6, async () => {
    const compact = await visibleSoon(xdo, '^Promview alerts$');
    xdo('windowmove', compact.id, 1460, 300);
    await sleep(600);
    await close(main);
    await pointer.moveTo(1000, 700, { ms: 900 });
  });
  await at(26, async () => {
    mark('updated');
    await seed('--resolve', 'HighMemoryUsage');
  });

  // A new critical, announced by the machine.
  await at(31, async () => {
    mark('critical');
    await seed('--wave', 'incident', ...(process.env.RETAKE ? ['--fresh'] : []));
  });
  await at(35, () => pointer.moveTo(1700, 90, { ms: 1200 }));
  await at(40, () => pointer.moveTo(1240, 640, { ms: 1200 }));
}

/** Polls fast for a window that is about to be mapped, so it can be placed before the eye catches it. */
async function visibleSoon(xdo, name) {
  for (let i = 0; i < 200; i += 1) {
    try {
      const id = xdo('search', '--onlyvisible', '--name', name).split('\n')[0];
      if (id) {
        const info = Object.fromEntries(
          xdo('getwindowgeometry', '--shell', id)
            .split('\n')
            .map((l) => l.split('=')),
        );
        return {
          id,
          x: Number(info.X),
          y: Number(info.Y),
          width: Number(info.WIDTH),
          height: Number(info.HEIGHT),
        };
      }
    } catch {
      /* not yet */
    }
    await sleep(50);
  }
  throw new Error(`no window ${name}`);
}

if (process.argv[1] === new URL(import.meta.url).pathname)
  await runDesktopScene({ name, bars, setup, body });
