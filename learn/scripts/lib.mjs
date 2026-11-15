// Shared by the episode scripts: find an episode by slug, list what it needs.
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const bin = (name) => (existsSync(`/opt/homebrew/bin/${name}`) ? `/opt/homebrew/bin/${name}` : name);
export const FFMPEG = bin('ffmpeg');
export const FFPROBE = bin('ffprobe');

export const loadEpisode = (slug) => {
  if (!slug) throw new Error('Usage: <script> <slug>');
  for (const dir of readdirSync(join(root, 'episodes'))) {
    const file = join(root, 'episodes', dir, 'episode.json');
    if (!existsSync(file)) continue;
    const ep = JSON.parse(readFileSync(file, 'utf8'));
    if (ep.slug === slug) return { ep, file, publicDir: join(root, 'public', 'episodes', slug) };
  }
  throw new Error(`No episode with slug "${slug}" in learn/episodes/*/episode.json`);
};

export const linesOf = (ep) => ep.scenes.flatMap((s) => s.lines ?? []);
const names = (ep, key) => [...new Set(ep.scenes.flatMap((s) => [s[key], ...(s.beats ?? []).map((b) => b[key])]).filter(Boolean))];
export const shotsOf = (ep) => names(ep, 'shot');
export const clipsOf = (ep) => names(ep, 'clip');
