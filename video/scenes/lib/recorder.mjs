// Records the X screen with ffmpeg. Lossless intermediate; the assembly
// encodes for delivery. A web page draws its own cursor, so the X one is
// left out there; the desktop scenes film the real pointer.

import { spawn } from 'node:child_process';
import { FPS } from './beat.mjs';

export function startRecorder(
  outPath,
  { display, width = 1920, height = 1080, drawMouse = false } = {},
) {
  const input = display ?? `${process.env.DISPLAY ?? ':99'}.0`;
  const args = [
    '-hide_banner',
    '-loglevel',
    'error',
    '-nostdin',
    '-y',
    '-f',
    'x11grab',
    '-framerate',
    String(FPS),
    '-video_size',
    `${width}x${height}`,
    '-draw_mouse',
    drawMouse ? '1' : '0',
    '-i',
    input,
    '-c:v',
    'libx264',
    '-preset',
    'ultrafast',
    '-qp',
    '0',
    '-pix_fmt',
    'yuv444p',
    outPath,
  ];
  const startedAt = Date.now();
  const child = spawn('ffmpeg', args, { stdio: ['ignore', 'inherit', 'inherit'] });
  const exited = new Promise((resolve) => child.on('exit', resolve));
  return {
    startedAt,
    // SIGINT is how ffmpeg is told to finish the file; anything harsher
    // leaves an unreadable container behind.
    stop: async () => {
      child.kill('SIGINT');
      const code = await exited;
      if (code !== 0 && code !== 255) throw new Error(`ffmpeg exited ${code}`);
    },
  };
}
