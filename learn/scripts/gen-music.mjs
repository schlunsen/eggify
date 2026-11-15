// Music for the learning videos via the Atlas Cloud API. Each run is one paid generation
// (suno/chirp-v6 ≈ $0.13 and returns two takes; minimax/music-2.6 or -3.0 ≈ $0.15, one take).
// The key is read at run time from ATLASCLOUD_API_KEY or an env file outside this repository
// (ATLAS_ENV_FILE); it is never printed or written anywhere.
//
// usage: node scripts/gen-music.mjs "<style / prompt>" <out-base> [--model suno/chirp-v6] [--duration 110] [--title "…"]
//   writes <out-base>-1.mp3 (and -2.mp3 … when the model returns several takes) plus <out-base>.json
//   (model, prompt, date, prediction id). Raw takes belong in learn/tts/out/music (not committed);
//   tts/music_edit.py cuts them into public/audio/music-bed.mp3 (loopable) and public/audio/sting.mp3.
import fs from 'node:fs';
import path from 'node:path';

const API = 'https://api.atlascloud.ai/api/v1';
const ENV = process.env.ATLAS_ENV_FILE || '';

const args = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = args.indexOf(`--${name}`);
  if (i < 0) return fallback;
  const v = args[i + 1];
  args.splice(i, 2);
  return v;
};
const model = flag('model', 'suno/chirp-v6');
const duration = Number(flag('duration', '110'));
const title = flag('title', 'Eggify tutorial underscore');
const [prompt, outBase] = args;
if (!prompt || !outBase) {
  throw new Error('usage: node scripts/gen-music.mjs "<style / prompt>" <out-base> [--model …] [--duration s] [--title …]');
}

function readKey() {
  if (process.env.ATLASCLOUD_API_KEY) return process.env.ATLASCLOUD_API_KEY;
  let text = '';
  try { if (ENV) text = fs.readFileSync(ENV, 'utf8'); } catch { /* reported below */ }
  return text.match(/^ATLASCLOUD_API_KEY=["']?([^"'\s]+)/m)?.[1];
}
const key = readKey();
if (!key) throw new Error('No ATLASCLOUD_API_KEY (set it, or point ATLAS_ENV_FILE at a file that has it)');

async function api(method, url, body) {
  const res = await fetch(url, {
    method,
    headers: { Authorization: `Bearer ${key}`, ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${url} → HTTP ${res.status}: ${text.slice(0, 300)}`);
  const json = JSON.parse(text);
  return json.data ?? json;
}

// Instrumental only, on every model.
const body = model.startsWith('suno/')
  ? { model, custom: true, instrumental: true, title, style: prompt, duration, negative_tags: 'vocals, singing, voice, choir, lead melody, distortion' }
  : { model, prompt, is_instrumental: true, format: 'mp3', sample_rate: 44100, bitrate: 256000 };

const gen = await api('POST', `${API}/model/generateAudio`, body);
const id = gen.id;
if (!id) throw new Error(`No prediction id: ${JSON.stringify(gen).slice(0, 300)}`);
console.log('prediction', id, `(${model})`);

const started = Date.now();
for (;;) {
  await new Promise((r) => setTimeout(r, 5000));
  const d = await api('GET', `${API}/model/prediction/${id}`);
  const status = String(d.status ?? '').toLowerCase();
  if (status === 'completed' || status === 'succeeded') {
    const outputs = (d.outputs ?? []).filter((u) => typeof u === 'string' && /^https?:/.test(u));
    if (!outputs.length) throw new Error('Completed without outputs');
    fs.mkdirSync(path.dirname(outBase), { recursive: true });
    const files = [];
    for (const [i, url] of outputs.entries()) {
      const ext = (new URL(url).pathname.match(/\.(mp3|wav|m4a|flac|ogg)$/i)?.[1] ?? 'mp3').toLowerCase();
      const file = `${outBase}-${i + 1}.${ext}`;
      const audio = Buffer.from(await (await fetch(url)).arrayBuffer());
      fs.writeFileSync(file, audio);
      files.push(path.basename(file));
      console.log(`saved ${file} (${audio.length} bytes)`);
    }
    fs.writeFileSync(`${outBase}.json`, `${JSON.stringify({ model, prompt, title, duration, prediction: id, date: new Date().toISOString().slice(0, 10), files }, null, 2)}\n`);
    break;
  }
  if (['failed', 'error', 'canceled', 'cancelled'].includes(status)) throw new Error(`Prediction ${status}: ${d.error ?? ''}`);
  if (Date.now() - started > 12 * 60_000) throw new Error(`Timed out waiting for prediction ${id}`);
  process.stdout.write('.');
}
