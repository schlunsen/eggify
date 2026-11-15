#!/usr/bin/env node
// Prints an episode's timeline in frames (scenes, lines, segments, marks): handy for picking
// frames for `npx remotion still`.   node scripts/dump-timeline.mjs <slug>
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { clipsOf, loadEpisode, shotsOf } from './lib.mjs';
import { buildTimeline } from '../src/timeline.ts';

const { ep, publicDir } = loadEpisode(process.argv[2]);
const json = (f) => (existsSync(f) ? JSON.parse(readFileSync(f, 'utf8')) : null);
const boxes = {};
for (const [dir, names] of [['shots', shotsOf(ep)], ['clips', clipsOf(ep)]]) {
  for (const n of names) { const b = json(join(publicDir, dir, `${n}.json`))?.boxes; if (b) boxes[n] = b; }
}
const tl = buildTimeline(ep, json(join(publicDir, 'timing.json')), boxes);
console.log(`${ep.slug}: ${tl.total} frames (${(tl.total / 30).toFixed(1)} s)`);
for (const s of tl.scenes) {
  console.log(`${String(s.from).padStart(5)}  ${s.kind} ${s.id}${s.heading ? ` "${s.heading}"` : ''} (${s.dur})`);
  for (const l of s.lines) console.log(`${String(l.from).padStart(7)}  line ${l.id} (${l.dur})`);
}
for (const b of tl.blocks) {
  if (b.kind !== 'stage') continue;
  for (const g of b.segments) console.log(`${String(g.from).padStart(5)}  ${g.kind} ${g.name} ${g.enter} → ${g.to}; camera ${g.cam.map((k) => k.at).join(', ')}; cursor ${g.cursor.map((k) => k.at).join(', ')}`);
  for (const m of b.marks) console.log(`${String(m.from).padStart(5)}  mark ${m.ring ? 'ring ' : ''}${m.label ?? ''} → ${m.to}`);
}
