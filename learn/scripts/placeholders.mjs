#!/usr/bin/env node
// Stand-ins so an episode can be laid out and rendered before its real assets exist.
//   node scripts/placeholders.mjs <slug>           write what is missing
//   node scripts/placeholders.mjs <slug> --list    show which files are still placeholders
//   node scripts/placeholders.mjs <slug> --clean   delete the ones that are still placeholders
// Missing shots get a grey mock screen (3840×2160) and a boxes json with three demo boxes
// (sidebar, card, button). Existing files are never overwritten. What was written is recorded
// with its hash in public/episodes/<slug>/placeholders.json, so a file that has since been
// replaced by the real thing is no longer counted (or cleaned).
// Narration lines without a wav get an estimated length in timing.json (words / 2.6 s, marked
// "placeholder": true) and stay silent. No wav is written for them: tts/gen.py skips lines that
// already have a file, so a silent stand-in would quietly end up in the finished video.
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import { FFMPEG, loadEpisode, root, shotsOf } from './lib.mjs';

const BOXES = { sidebar: { x: 0, y: 0, w: 300, h: 1080 }, card: { x: 560, y: 300, w: 800, h: 360 }, button: { x: 1180, y: 580, w: 150, h: 48 } };
const FONT = '/System/Library/Fonts/Helvetica.ttc';
const sha = (file) => createHash('sha1').update(readFileSync(file)).digest('hex');
const ffmpeg = (args) => execFileSync(FFMPEG, ['-v', 'error', '-y', ...args], { stdio: ['ignore', 'inherit', 'inherit'] });

try {
  const [slug, flag] = process.argv.slice(2);
  const { ep, publicDir } = loadEpisode(slug);
  const listFile = join(publicDir, 'placeholders.json');
  const known = existsSync(listFile) ? JSON.parse(readFileSync(listFile, 'utf8')).files : {};
  // Forget files that are gone or have been replaced by real ones.
  for (const [rel, hash] of Object.entries(known)) {
    const file = join(publicDir, rel);
    if (!existsSync(file) || sha(file) !== hash) delete known[rel];
  }
  const save = () => {
    if (Object.keys(known).length) writeFileSync(listFile, `${JSON.stringify({ files: known }, null, 2)}\n`);
    else rmSync(listFile, { force: true });
  };

  const timingFile = join(publicDir, 'timing.json');
  const silent = () => (existsSync(timingFile) ? Object.entries(JSON.parse(readFileSync(timingFile, 'utf8')).lines).filter(([, l]) => l.placeholder).map(([id]) => id) : []);
  if (flag === '--list' || flag === '--clean') {
    for (const rel of Object.keys(known).sort()) {
      console.log(`${flag === '--clean' ? 'removed ' : ''}public/episodes/${slug}/${rel}`);
      if (flag === '--clean') { rmSync(join(publicDir, rel)); delete known[rel]; }
    }
    const lines = silent();
    if (lines.length) {
      console.log(`${flag === '--clean' ? 'removed ' : ''}public/episodes/${slug}/timing.json (estimated, silent lines: ${lines.join(', ')})`);
      if (flag === '--clean') rmSync(timingFile);
    }
    save();
    process.exit(0);
  }
  if (flag) throw new Error(`Unknown option ${flag}`);

  const wrote = (file) => { known[relative(publicDir, file)] = sha(file); console.log(`placeholder ${relative(join(publicDir, '..', '..', '..'), file)}`); };
  mkdirSync(join(publicDir, 'shots'), { recursive: true });

  for (const name of shotsOf(ep)) {
    const png = join(publicDir, 'shots', `${name}.png`);
    const json = join(publicDir, 'shots', `${name}.json`);
    if (!existsSync(png)) {
      // A mock screen at device scale 2: sidebar, top bar, a card with a button, and the shot's name.
      const b = (r, color) => `drawbox=x=${r.x * 2}:y=${r.y * 2}:w=${r.w * 2}:h=${r.h * 2}:color=${color}:t=fill`;
      const text = (t, x, y, size, color) => `drawtext=fontfile=${FONT}:text='${t}':x=${x}:y=${y}:fontsize=${size}:fontcolor=${color}`;
      const rows = Array.from({ length: 7 }, (_, i) => `drawbox=x=60:y=${300 + i * 150}:w=480:h=90:color=0x2a2f3d:t=fill`);
      const vf = [
        b(BOXES.sidebar, '0x1b1e27'), 'drawbox=x=600:y=0:w=3240:h=140:color=0x20242f:t=fill', ...rows,
        b(BOXES.card, '0x262b38'), b(BOXES.button, '0xe8458b'),
        text(`PLACEHOLDER SHOT`, 700, 40, 56, '0x8a93a8'), text(name, 1180, 680, 150, 'white'),
        text('Replace with a real capture', 1180, 900, 64, '0x9aa3b8'), text('Button', 2440, 1178, 56, 'white'),
      ].join(',');
      ffmpeg(['-f', 'lavfi', '-i', 'color=c=0x14161c:s=3840x2160', '-vf', vf, '-frames:v', '1', png]);
      wrote(png);
      // Boxes only for a placeholder picture: a real shot's boxes come from its capture script.
      if (!existsSync(json)) {
        writeFileSync(json, `${JSON.stringify({ placeholder: true, boxes: BOXES }, null, 2)}\n`);
        wrote(json);
      }
    }
  }
  save();
  // Lay the episode out: real lengths for lines that have a wav, estimates for the rest.
  execFileSync(process.execPath, [join(root, 'scripts', 'timing.mjs'), slug, '--estimate'], { stdio: 'inherit' });
  const n = Object.keys(known).length + (silent().length ? 1 : 0);
  console.log(n ? `Placeholders in public/episodes/${slug}: node scripts/placeholders.mjs ${slug} --list` : 'Nothing is a placeholder.');
} catch (err) {
  console.error(`placeholders: ${err.message}`);
  process.exit(1);
}
