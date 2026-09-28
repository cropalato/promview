// Renders card clips frame by frame from cards/index.html into video files.
// Frame-exact and deterministic: no screen recording, no timers.
//
//   import { renderClips } from './cards/render.mjs';
//   await renderClips([{ out, kind, seconds, params, alpha }]);
//
// Opaque clips are lossless H.264; clips with alpha (captions) are QuickTime
// Animation with an alpha channel, which ffmpeg's overlay reads directly.
// A held frame (one the card reports as unchanged) is piped again from the
// last encode rather than drawn, which is what keeps a ten-second caption
// from costing six hundred renders.

import { spawn } from 'node:child_process';
import { chromium } from 'playwright';
import { FPS } from '../scenes/lib/beat.mjs';

const PAGE = new URL('./index.html', import.meta.url).href;

function encoder(out, alpha) {
  const codec = alpha
    ? ['-c:v', 'qtrle', '-pix_fmt', 'argb']
    : ['-c:v', 'libx264', '-preset', 'fast', '-qp', '0', '-pix_fmt', 'yuv444p'];
  const child = spawn(
    'ffmpeg',
    [
      '-hide_banner',
      '-loglevel',
      'error',
      '-y',
      '-f',
      'image2pipe',
      '-framerate',
      String(FPS),
      '-c:v',
      'png',
      '-i',
      '-',
      ...codec,
      '-r',
      String(FPS),
      out,
    ],
    { stdio: ['pipe', 'inherit', 'inherit'] },
  );
  const done = new Promise((resolve, reject) =>
    child.on('exit', (code) =>
      code === 0 ? resolve() : reject(new Error(`ffmpeg exited ${code}`)),
    ),
  );
  const write = (buffer) =>
    new Promise((resolve) =>
      child.stdin.write(buffer) ? resolve() : child.stdin.once('drain', resolve),
    );
  return { write, finish: () => (child.stdin.end(), done) };
}

export async function renderClips(clips) {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } });
  await page.goto(PAGE);
  await page.evaluate(() => window.ready);
  try {
    for (const clip of clips) {
      const frames = Math.round(clip.seconds * FPS);
      const enc = encoder(clip.out, clip.alpha === true);
      let held = null;
      let drawn = 0;
      for (let i = 0; i < frames; i += 1) {
        const t = i / FPS;
        const hold = await page.evaluate(
          ([kind, t, dur, params, haveHeld]) => {
            const h = window.renderFrame(kind, t, dur, params);
            return h && haveHeld ? true : false;
          },
          [clip.kind, t, clip.seconds, clip.params ?? {}, held !== null],
        );
        if (!hold) {
          const data = await page.evaluate(() => window.frameData());
          held = Buffer.from(data.slice(data.indexOf(',') + 1), 'base64');
          drawn += 1;
        }
        await enc.write(held);
      }
      await enc.finish();
      console.log(`rendered ${clip.out}: ${frames} frames, ${drawn} drawn`);
    }
  } finally {
    await browser.close();
  }
}

if (process.argv[1] === new URL(import.meta.url).pathname) {
  // `--check`: half a second of every kind, which exercises the page, the
  // fonts, the frame pipe and both encoders without rendering a video.
  const { mkdir } = await import('node:fs/promises');
  const out = new URL('../out/check/', import.meta.url).pathname;
  await mkdir(out, { recursive: true });
  const seconds = process.argv.includes('--check') ? 0.5 : 3;
  await renderClips([
    { out: `${out}title.mp4`, kind: 'title', seconds },
    {
      out: `${out}statement.mp4`,
      kind: 'statement',
      seconds,
      params: { lines: ['Alertmanager decides', 'who to wake up.'] },
    },
    {
      out: `${out}code.mp4`,
      kind: 'code',
      seconds,
      params: { title: 'config.toml', lines: ['[env]', 'SSL_CERT_FILE = "/etc/ca.pem"'] },
    },
    { out: `${out}end.mp4`, kind: 'end', seconds },
    {
      out: `${out}caption.mov`,
      kind: 'caption',
      seconds,
      alpha: true,
      params: { text: 'Alertmanager sends. Promview remembers.' },
    },
  ]);
}
