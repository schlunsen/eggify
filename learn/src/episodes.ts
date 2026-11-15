import type { Episode } from './types';

// Every learn/episodes/<nn>-<slug>/episode.json is bundled; adding a folder adds a composition.
declare const process: { env: Record<string, string | undefined> };
declare const require: { context: (dir: string, deep: boolean, re: RegExp) => { keys: () => string[]; (key: string): unknown } };
const ctx = require.context('../episodes', true, /\/episode\.json$/);

const showDev = process.env.REMOTION_LEARN_DEV === '1';

export const episodes: Episode[] = ctx
  .keys()
  .filter((k) => k.startsWith('./')) // webpack lists each file under several request forms
  .map((k) => {
    const mod = ctx(k) as Episode | { default: Episode };
    return 'default' in mod ? mod.default : mod;
  })
  .filter((e) => showDev || !e.dev)
  .sort((a, b) => a.number - b.number);

export const episodeBySlug = (slug: string) => {
  const e = episodes.find((x) => x.slug === slug);
  if (!e) throw new Error(`No episode "${slug}" in learn/episodes`);
  return e;
};
