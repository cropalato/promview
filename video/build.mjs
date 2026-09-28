#!/usr/bin/env node
// Assembles captures, cards and music into the delivery files.
//   node video/build.mjs filter        one scene, with its own bed (for review)
//   node video/build.mjs all           every captured scene, plus a rough cut
//   node video/build.mjs v2            the console tour
//   node video/build.mjs v1            the trailer
//   node video/build.mjs v3            the desktop client
//
// Cuts land on bars: every clip is a whole number of bars (or a half, in the
// trailer) and the music is generated for the same grid, so no transition
// shortens anything. Cards carry their own fades; console scenes hard-cut.

import { readFile, readdir, writeFile, mkdir } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { FPS, BEAT_MS, BEATS_PER_BAR } from './scenes/lib/beat.mjs';
import { renderClips } from './cards/render.mjs';

const OUT = new URL('./out/', import.meta.url).pathname;
const SYNTH = new URL('./audio/synth.py', import.meta.url).pathname;
const BAR = (BEAT_MS * BEATS_PER_BAR) / 1000;
const ORDER = [
  'ingest',
  'sources',
  'filter',
  'group',
  'detail',
  'operate',
  'live',
  'bulk',
  'silence',
  'themes',
];
const LOSSLESS = [
  '-c:v',
  'libx264',
  '-preset',
  'fast',
  '-qp',
  '0',
  '-pix_fmt',
  'yuv444p',
  '-r',
  String(FPS),
];
const DELIVERY = [
  '-c:v',
  'libx264',
  '-preset',
  'slow',
  '-crf',
  '18',
  '-pix_fmt',
  'yuv420p',
  '-r',
  String(FPS),
  '-c:a',
  'aac',
  '-b:a',
  '192k',
  '-movflags',
  '+faststart',
];
const LOUDNORM = ['-af', 'loudnorm=I=-14:TP=-1.5:LRA=11'];

// What each tour scene says, and how far into the scene it says it.
const CAPTIONS = {
  ingest: { at: 5.4, text: 'Alertmanager sends. Promview remembers.' },
  sources: { at: 2.0, text: 'Many Alertmanagers. One console.' },
  filter: { at: 0.6, text: 'Your labels are the filter.' },
  group: { at: 0.6, text: 'Fan-out, folded.' },
  detail: { at: 1.8, text: 'When did it start. Who has looked.' },
  operate: { at: 0.6, text: 'Acknowledge. Assign. Leave a note.' },
  live: { at: 0.6, text: 'Every open console, at once.' },
  bulk: { at: 0.6, text: 'One decision, many alerts.' },
  silence: { at: 0.6, text: 'Silenced in Alertmanager, from here.' },
  themes: { at: 0.6, text: 'Yours to look at all night.' },
};

const ffmpeg = (args) =>
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...args], {
    stdio: 'inherit',
  });
const synth = (args) => execFileSync('python3', [SYNTH, ...args], { stdio: 'inherit' });
const marksOf = (name) => readFile(`${OUT}scenes/${name}.marks.json`, 'utf8').then(JSON.parse);
const secondsOf = (marks) => marks.bars * BAR;

/**
 * Cuts `seconds` of a scene starting `from` seconds into it, joins a split
 * recording, and lays each caption over it from its `at`.
 */
function sceneClip(marks, from, seconds, out, captions = []) {
  const name = marks.name;
  const start = marks.offsetMs / 1000 + from;
  const inputs = marks.split
    ? [
        '-ss',
        start,
        '-i',
        `${OUT}scenes/${name}.left.mkv`,
        '-ss',
        start,
        '-i',
        `${OUT}scenes/${name}.right.mkv`,
      ]
    : ['-ss', start, '-i', `${OUT}scenes/${name}.mkv`];
  let graph = marks.split ? '[0:v][1:v]hstack=inputs=2[v]' : '[0:v]null[v]';
  const args = [...inputs];
  let index = marks.split ? 2 : 1;
  for (const caption of [captions].flat().filter(Boolean)) {
    args.push('-itsoffset', String(caption.at), '-i', caption.file);
    graph += `;[v][${index}:v]overlay=eof_action=pass:format=auto[v]`;
    index += 1;
  }
  ffmpeg([
    ...args.map(String),
    '-filter_complex',
    graph,
    '-map',
    '[v]',
    '-t',
    String(seconds),
    ...LOSSLESS,
    out,
  ]);
}

async function concatWithAudio(clips, audio, out) {
  await writeFile(`${out}.txt`, clips.map((file) => `file '${file}'`).join('\n'));
  ffmpeg([
    '-f',
    'concat',
    '-safe',
    '0',
    '-i',
    `${out}.txt`,
    '-i',
    audio,
    '-map',
    '0:v',
    '-map',
    '1:a',
    ...LOUDNORM,
    ...DELIVERY,
    '-shortest',
    out,
  ]);
  console.log(`wrote ${out}`);
}

async function buildScene(name) {
  const marks = await marksOf(name);
  synth(['scene', `${OUT}scenes/${name}.marks.json`, `${OUT}scenes/${name}.wav`]);
  sceneClip(marks, 0, secondsOf(marks), `${OUT}scenes/${name}.cut.mp4`);
  ffmpeg([
    '-i',
    `${OUT}scenes/${name}.cut.mp4`,
    '-i',
    `${OUT}scenes/${name}.wav`,
    ...DELIVERY,
    `${OUT}${name}.mp4`,
  ]);
  console.log(`wrote ${OUT}${name}.mp4 (${secondsOf(marks)}s)`);
}

async function captured() {
  const have = new Set(
    (await readdir(`${OUT}scenes/`))
      .filter((f) => f.endsWith('.marks.json'))
      .map((f) => f.replace('.marks.json', '')),
  );
  return ORDER.filter((name) => have.has(name));
}

async function buildAll() {
  const scenes = await captured();
  for (const name of scenes) await buildScene(name);
  const marks = await Promise.all(scenes.map(marksOf));
  await writeFile(`${OUT}rough.scenes.json`, JSON.stringify(marks));
  synth(['sequence', `${OUT}rough.scenes.json`, `${OUT}rough.wav`]);
  await concatWithAudio(
    scenes.map((name) => `${OUT}scenes/${name}.cut.mp4`),
    `${OUT}rough.wav`,
    `${OUT}rough.mp4`,
  );
}

/** The console tour: title, every scene with its caption, end card. */
async function buildV2() {
  await mkdir(`${OUT}cards/`, { recursive: true });
  await mkdir(`${OUT}v2/`, { recursive: true });
  const scenes = await captured();
  const marks = await Promise.all(scenes.map(marksOf));
  const TITLE_BARS = 2,
    END_BARS = 4;

  await renderClips([
    { out: `${OUT}cards/v2-title.mp4`, kind: 'title', seconds: TITLE_BARS * BAR },
    { out: `${OUT}cards/v2-end.mp4`, kind: 'end', seconds: END_BARS * BAR },
    ...marks.map((m) => ({
      out: `${OUT}cards/v2-cap-${m.name}.mov`,
      kind: 'caption',
      alpha: true,
      seconds: secondsOf(m) - CAPTIONS[m.name].at,
      params: { text: CAPTIONS[m.name].text },
    })),
  ]);
  for (const m of marks) {
    sceneClip(m, 0, secondsOf(m), `${OUT}v2/${m.name}.mp4`, [
      { at: CAPTIONS[m.name].at, file: `${OUT}cards/v2-cap-${m.name}.mov` },
    ]);
  }
  await writeFile(
    `${OUT}v2.scenes.json`,
    JSON.stringify([{ bars: TITLE_BARS, marks: [] }, ...marks, { bars: END_BARS, marks: [] }]),
  );
  synth(['sequence', `${OUT}v2.scenes.json`, `${OUT}v2.wav`]);
  await concatWithAudio(
    [
      `${OUT}cards/v2-title.mp4`,
      ...marks.map((m) => `${OUT}v2/${m.name}.mp4`),
      `${OUT}cards/v2-end.mp4`,
    ],
    `${OUT}v2.wav`,
    `${OUT}v2.mp4`,
  );
}

/** The trailer: cards and the sharpest moments of the captures, 12.5 bars. */
async function buildV1() {
  await mkdir(`${OUT}cards/`, { recursive: true });
  await mkdir(`${OUT}v1/`, { recursive: true });
  // `from` is seconds into the scene; every entry is a number of bars.
  const SHOTS = [
    { card: 'title', bars: 1.5 },
    {
      card: 'statement',
      bars: 1.5,
      params: { lines: ['Alertmanager decides', 'who to wake up.'] },
    },
    { scene: 'ingest', from: 4.3, bars: 1.5 },
    {
      card: 'statement',
      bars: 1,
      params: { lines: ['Promview is where', 'the shift happens.'], accentLine: 1 },
    },
    { scene: 'detail', from: 3.9, bars: 0.75 },
    { scene: 'operate', from: 8.4, bars: 0.75 },
    { scene: 'sources', from: 7.2, bars: 1 },
    { scene: 'filter', from: 1.8, bars: 1 },
    { scene: 'tour', from: 22.4, bars: 1 }, // the desktop client: compact window and a native notification
    {
      card: 'statement',
      bars: 1.5,
      params: { lines: ['One binary.', 'Your PostgreSQL. Your labels.'], size: 60 },
    },
    { card: 'end', bars: 1 },
  ];
  const total = SHOTS.reduce((sum, s) => sum + s.bars, 0);
  const cards = SHOTS.map((s, i) => ({ ...s, i })).filter((s) => s.card);
  await renderClips(
    cards.map((s) => ({
      out: `${OUT}cards/v1-${s.i}.mp4`,
      kind: s.card,
      seconds: s.bars * BAR,
      params: s.params,
    })),
  );
  const files = [];
  for (const [i, shot] of SHOTS.entries()) {
    if (shot.card) {
      files.push(`${OUT}cards/v1-${i}.mp4`);
    } else {
      const file = `${OUT}v1/${i}-${shot.scene}.mp4`;
      sceneClip(await marksOf(shot.scene), shot.from, shot.bars * BAR, file);
      files.push(file);
    }
  }
  synth(['trailer', `${OUT}v1.wav`, '--bars', String(total)]);
  await concatWithAudio(files, `${OUT}v1.wav`, `${OUT}v1.mp4`);
}

/** The desktop client: one continuous take, captions by beat, then the config file and the end card. */
async function buildV3() {
  await mkdir(`${OUT}cards/`, { recursive: true });
  await mkdir(`${OUT}v3/`, { recursive: true });
  const marks = await marksOf('tour');
  const seconds = secondsOf(marks);
  const CAPTIONS3 = [
    { at: 0.6, text: 'The console, in the tray.' },
    { at: 4.2, text: 'Same console. Same server.' },
    { at: 9.0, text: 'Your accounts, your directory, or your IdP.' },
    { at: 13.2, text: 'Always on top. Out of the way.' },
    { at: 19.2, text: 'Your machine tells you.' },
  ];
  // Each caption ends where the next begins, so the assembly overlays them one after another.
  const captionClips = CAPTIONS3.map((c, i) => ({
    ...c,
    file: `${OUT}cards/v3-cap-${i}.mov`,
    seconds: (CAPTIONS3[i + 1]?.at ?? seconds) - c.at,
  }));
  const CONFIG_BARS = 2,
    END_BARS = 2;
  await renderClips([
    ...captionClips.map((c) => ({
      out: c.file,
      kind: 'caption',
      alpha: true,
      seconds: c.seconds,
      params: { text: c.text },
    })),
    {
      out: `${OUT}cards/v3-config.mp4`,
      kind: 'code',
      seconds: CONFIG_BARS * BAR,
      params: {
        title: '~/.config/promview-desktop/config.toml',
        lines: [
          'server_url = "https://promview.internal"',
          '',
          '[env]',
          'SSL_CERT_FILE = "/etc/promview/internal-ca.pem"',
          '',
          "# This laptop only buzzes for its owner's team.",
          '[[notifications.rules]]',
          'severity = "^critical$"',
          'team = "^(payments|platform)$"',
        ],
      },
    },
    {
      out: `${OUT}cards/v3-end.mp4`,
      kind: 'end',
      seconds: END_BARS * BAR,
      params: {
        commands: ['sudo dpkg -i Promview_0.1.0-beta.3_amd64.deb'],
        footer: '.deb  ·  .rpm  ·  .msi  ·  .exe  —  attached to every release',
      },
    },
  ]);
  sceneClip(marks, 0, seconds, `${OUT}v3/tour.mp4`, captionClips);
  await writeFile(
    `${OUT}v3.scenes.json`,
    JSON.stringify([marks, { bars: CONFIG_BARS, marks: [] }, { bars: END_BARS, marks: [] }]),
  );
  synth(['sequence', `${OUT}v3.scenes.json`, `${OUT}v3.wav`, '--style', 'desktop']);
  await concatWithAudio(
    [`${OUT}v3/tour.mp4`, `${OUT}cards/v3-config.mp4`, `${OUT}cards/v3-end.mp4`],
    `${OUT}v3.wav`,
    `${OUT}v3.mp4`,
  );
}

const target = process.argv[2] ?? 'filter';
if (target === 'all') await buildAll();
else if (target === 'v2') await buildV2();
else if (target === 'v1') await buildV1();
else if (target === 'v3') await buildV3();
else await buildScene(target);
