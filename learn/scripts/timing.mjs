#!/usr/bin/env node
// Measures an episode's narration: public/episodes/<slug>/vo/<lineId>.wav → timing.json
//   node scripts/timing.mjs <slug> [--estimate]
// {"lines": {"<lineId>": {"dur": seconds}}, "clips": {"<name>": {"dur": seconds}}}
// The engine lays the whole episode out from this file, so run it after any voice change.
// A line without a wav is an error. With --estimate (trial builds before the voice exists) it
// gets {"dur": words / 2.6, "placeholder": true} instead and stays silent in the video; no wav
// is written, because tts/gen.py skips lines that already have one.
import { execFileSync } from 'node:child_process';
import { existsSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { FFPROBE, clipsOf, linesOf, loadEpisode } from './lib.mjs';

const duration = (file) => {
  const out = execFileSync(FFPROBE, ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', file], { encoding: 'utf8' });
  const dur = Number.parseFloat(out);
  if (!Number.isFinite(dur) || dur <= 0) throw new Error(`ffprobe gave no duration for ${file}`);
  return Math.round(dur * 1000) / 1000;
};

try {
  const estimate = process.argv.includes('--estimate');
  const { ep, publicDir } = loadEpisode(process.argv[2]);
  const lines = {};
  const missing = [];
  const ids = new Set();
  for (const l of linesOf(ep)) {
    if (ids.has(l.id)) throw new Error(`Line id "${l.id}" is used twice in episode.json`);
    ids.add(l.id);
    const wav = join(publicDir, 'vo', `${l.id}.wav`);
    if (existsSync(wav)) lines[l.id] = { dur: duration(wav) };
    else {
      missing.push(l.id);
      lines[l.id] = { dur: Math.round(Math.max(1.2, l.text.trim().split(/\s+/).length / 2.6) * 100) / 100, placeholder: true };
    }
  }
  if (missing.length && !estimate) throw new Error(`No narration for ${missing.length} line(s): ${missing.join(', ')} (expected ${join(publicDir, 'vo')}/<id>.wav)`);
  const clips = {};
  for (const name of clipsOf(ep)) {
    const mp4 = join(publicDir, 'clips', `${name}.mp4`);
    if (!existsSync(mp4)) throw new Error(`Clip "${name}" is used in episode.json but ${mp4} does not exist`);
    clips[name] = { dur: duration(mp4) };
  }
  const timing = Object.keys(clips).length ? { lines, clips } : { lines };
  writeFileSync(join(publicDir, 'timing.json'), `${JSON.stringify(timing, null, 2)}\n`);
  const total = Object.values(lines).reduce((n, l) => n + l.dur, 0);
  console.log(`timing.json: ${Object.keys(lines).length} lines, ${total.toFixed(1)} s of narration${Object.keys(clips).length ? `, ${Object.keys(clips).length} clip(s)` : ''}`);
  if (missing.length) console.log(`  estimated (no wav yet, silent): ${missing.join(', ')}`);
} catch (err) {
  console.error(`timing: ${err.message}`);
  process.exit(1);
}
