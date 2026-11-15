// Turns an episode + the narration durations into frames. Pure: no React, no I/O, so
// calculateMetadata and the composition get the same answer.
import type { Beat, BoxRef, Boxes, Episode, Rect, Scene, Timing } from './types';

export const FPS = 30;
export const W = 1920;
export const H = 1080;
/** Where the screenshot sits in the frame (16:9, 0.8 of the captured viewport). */
export const VIEW = { x: 192, y: 92, w: 1536, h: 864 };
export const FULL: Rect = { x: 0, y: 0, w: W, h: H };

const GAP = 0.35;          // between narration lines
const TAIL = 0.6;          // after a scene's last line
const LEAD = { title: 1.1, shot: 0.6, clip: 0.6, outro: 0.9 } as const; // before its first line
const MIN = { title: 4.5, shot: 2.5, clip: 2.5, outro: 6 } as const;
const OUTRO_HOLD = 1.6;    // the outro stays up after the narrator has finished
export const OVERLAP = 12; // frames: the next sheet slides in over the previous one
export const PUSH = 14;    // frames: a new chapter's screen pushes in
export const FADE = 8;     // frames: cut to another shot inside a chapter
export const ZOOM = 32;    // frames: one camera move
export const MOVE = 22;    // frames: one cursor move
const MAX_ZOOM = 2.4;      // shots are 2× the viewport and shown at 0.8, so 2.5 is native resolution

const fr = (s: number) => Math.round(s * FPS);

/** Spoken length when no audio exists yet (same rule as scripts/placeholders.mjs). */
export const estimateDur = (text: string) => Math.max(1.2, text.trim().split(/\s+/).length / 2.6);

export interface TlLine { id: string; text: string; from: number; dur: number; silent: boolean }
export interface TlScene {
  id: string; kind: Scene['kind']; heading?: string; chapter: number; // 1-based among shot/clip scenes, 0 otherwise
  from: number; dur: number; lines: TlLine[];
}
export interface CamKey { at: number; rect: Rect }
export interface Segment {
  kind: 'shot' | 'clip'; name: string; from: number; to: number;
  enter: 'none' | 'push' | 'fade';
  clipFrames?: number;           // known length of a clip, in frames
  cam: CamKey[];
  cursor: { at: number; to: { x: number; y: number } | null; click: boolean }[];
}
export interface Mark { from: number; to: number; seg: number; rect: Rect | null; ring: boolean; label?: string }
export type Block =
  | { kind: 'title'; from: number; dur: number; scene: TlScene }
  | { kind: 'outro'; from: number; dur: number; scene: TlScene }
  | { kind: 'stage'; from: number; dur: number; scenes: TlScene[]; segments: Segment[]; marks: Mark[] };
export interface Caption { from: number; dur: number; text: string }
export interface Timeline {
  total: number; scenes: TlScene[]; blocks: Block[]; captions: Caption[];
  chapters: { n: number; heading: string }[];
  posterFrame: number;
}

/** The 16:9 camera rectangle that shows `r` with some air around it. */
export const frameRect = (r: Rect, pad = 110): Rect => {
  const w = Math.min(W, Math.max(r.w + pad * 2, (r.h + pad * 1.4) * (W / H), W / MAX_ZOOM));
  const h = (w * H) / W;
  const x = Math.min(W - w, Math.max(0, r.x + r.w / 2 - w / 2));
  const y = Math.min(H - h, Math.max(0, r.y + r.h / 2 - h / 2));
  return { x, y, w, h };
};

/** Splits a narration line into captions of one comfortable line each. */
export const splitCaption = (text: string, max = 86): string[] => {
  const out: string[] = [];
  const sentences = text.match(/[^.?!:]+[.?!:]+["”’)]*\s*|[^.?!:]+$/g) ?? [text];
  let cur = '';
  for (const s of sentences) {
    if (cur && (cur + s).trim().length > max) { out.push(cur.trim()); cur = ''; }
    cur += s;
  }
  if (cur.trim()) out.push(cur.trim());
  return out.flatMap((part) => {
    if (part.length <= max) return [part];
    // Still too long: cut at the comma (or else the space) nearest the middle.
    const mid = part.length / 2;
    const cuts = [...part.matchAll(/, /g)].map((m) => m.index! + 1);
    const spaces = [...part.matchAll(/ /g)].map((m) => m.index!);
    const pick = (xs: number[]) => xs.reduce((best, x) => (Math.abs(x - mid) < Math.abs(best - mid) ? x : best), xs[0]);
    const at = cuts.length && Math.abs(pick(cuts) - mid) < part.length * 0.3 ? pick(cuts) : pick(spaces);
    return at ? [part.slice(0, at).trim(), part.slice(at).trim()] : [part];
  });
};

export const buildTimeline = (ep: Episode, timing: Timing | null, boxes: Boxes): Timeline => {
  const where = `episode ${ep.slug}`;
  const lineDur = (id: string, text: string) => {
    if (!timing) return estimateDur(text);
    const d = timing.lines[id]?.dur;
    if (!d) throw new Error(`${where}: line "${id}" is missing from timing.json. Run: node scripts/timing.mjs ${ep.slug}`);
    return d;
  };

  // 1. Scenes and lines, in seconds, then frames.
  const scenes: TlScene[] = [];
  const captions: Caption[] = [];
  const seen = new Set<string>();
  let t = 0;
  let chapter = 0;
  ep.scenes.forEach((sc, i) => {
    const isMedia = sc.kind === 'shot' || sc.kind === 'clip';
    if (isMedia) chapter++;
    let cursor = t + LEAD[sc.kind];
    const lines: TlLine[] = (sc.lines ?? []).map((l) => {
      if (seen.has(l.id)) throw new Error(`${where}: line id "${l.id}" is used twice`);
      seen.add(l.id);
      const dur = lineDur(l.id, l.text);
      const line = { id: l.id, text: l.text, from: fr(cursor), dur: fr(dur), silent: !timing || !!timing.lines[l.id]?.placeholder };
      cursor += dur + GAP;
      return line;
    });
    let end = lines.length ? cursor - GAP + TAIL : t;
    if (sc.kind === 'outro') end += OUTRO_HOLD;
    if (sc.kind === 'clip' && sc.clip && timing?.clips?.[sc.clip]) end = Math.max(end, t + LEAD.clip + timing.clips[sc.clip].dur);
    end = Math.max(end, t + MIN[sc.kind]);
    scenes.push({ id: sc.id, kind: sc.kind, heading: sc.heading, chapter: isMedia ? chapter : 0, from: fr(t), dur: fr(end) - fr(t), lines });
    for (const l of lines) {
      const parts = splitCaption(l.text);
      const chars = parts.reduce((n, p) => n + p.length, 0);
      let at = l.from;
      parts.forEach((p, k) => {
        const d = k === parts.length - 1 ? l.from + l.dur - at : Math.round((l.dur * p.length) / chars);
        captions.push({ from: at, dur: d + (k === parts.length - 1 ? fr(0.3) : 0), text: p });
        at += d;
      });
    }
    t = end;
    void i;
  });
  const total = fr(t);

  // 2. Blocks: title, outro, and runs of shot/clip scenes that share one stage.
  const blocks: Block[] = [];
  ep.scenes.forEach((sc, i) => {
    const s = scenes[i];
    const last = blocks[blocks.length - 1];
    if (sc.kind === 'title' || sc.kind === 'outro') {
      blocks.push({ kind: sc.kind, from: s.from, dur: s.dur, scene: s });
      return;
    }
    const stage = last?.kind === 'stage' ? last : (blocks.push({ kind: 'stage', from: s.from, dur: 0, scenes: [], segments: [], marks: [] }), blocks[blocks.length - 1] as Extract<Block, { kind: 'stage' }>);
    stage.scenes.push(s);
    stage.dur = s.from + s.dur - stage.from;
    const sceneEnd = s.from + s.dur;

    const open = (kind: 'shot' | 'clip', name: string, at: number, enter: Segment['enter'], rect: Rect) => {
      const prev = stage.segments[stage.segments.length - 1];
      if (prev) prev.to = at;
      const clip = kind === 'clip' ? timing?.clips?.[name]?.dur : undefined;
      stage.segments.push({ kind, name, from: at, to: sceneEnd, enter, clipFrames: clip ? fr(clip) : undefined, cam: [{ at, rect }], cursor: [] });
    };
    const first = sc.kind === 'clip' ? sc.clip : sc.shot;
    if (!first) throw new Error(`${where}: scene "${sc.id}" needs a "${sc.kind}"`);
    open(sc.kind as 'shot' | 'clip', first, s.from, stage.segments.length ? 'push' : 'none', FULL);

    const seg = () => stage.segments[stage.segments.length - 1];
    const resolve = (ref: BoxRef, what: string): Rect => {
      if (typeof ref !== 'string') return ref;
      const box = boxes[seg().name]?.[ref];
      if (!box) throw new Error(`${where}: scene "${sc.id}" ${what}: no box "${ref}" in ${seg().kind}s/${seg().name}.json`);
      return box;
    };
    let mark: Mark | null = null;
    const closeMark = (at: number) => { if (mark) { mark.to = at; mark = null; } };

    const timed = (sc.beats ?? []).map((b: Beat) => {
      const line = b.line ? s.lines.find((l) => l.id === b.line) : s.lines[0];
      if (b.line && !line) throw new Error(`${where}: scene "${sc.id}" has a beat on line "${b.line}", which is not one of its lines`);
      return { b, at: Math.min(sceneEnd - 1, (line?.from ?? s.from) + fr(b.at ?? 0)) };
    }).sort((a, b) => a.at - b.at);

    for (const { b, at } of timed) {
      if (b.shot || b.clip) {
        closeMark(at);
        open(b.clip ? 'clip' : 'shot', (b.clip ?? b.shot)!, at, 'fade', FULL);
        if (b.zoom) seg().cam[0].rect = frameRect(resolve(b.zoom, 'zoom'));
      } else if (b.zoom !== undefined) {
        seg().cam.push({ at, rect: b.zoom === null ? FULL : frameRect(resolve(b.zoom, 'zoom')) });
      }
      if (b.cursor !== undefined) {
        const r = b.cursor === null ? null : resolve(b.cursor, 'cursor');
        seg().cursor.push({ at, // The tip lands a little right of and below the centre, so it does not sit on the button's text.
        to: r && { x: r.x + r.w * 0.6, y: r.y + Math.min(r.h * 0.62, r.h / 2 + 14) }, click: !!b.click });
      }
      if (b.highlight !== undefined || b.label !== undefined) {
        closeMark(at);
        // A click lands first; the ring is drawn as the cursor arrives.
        const delay = b.cursor ? MOVE : 0;
        const anchor = b.highlight ?? (b.cursor || null) ?? (b.zoom || null);
        if (b.highlight || b.label) {
          mark = { from: at + delay, to: sceneEnd, seg: stage.segments.length - 1, rect: anchor ? resolve(anchor, 'highlight') : null, ring: !!b.highlight, label: b.label };
          stage.marks.push(mark);
        }
      }
    }
    closeMark(sceneEnd);
  });

  const title = scenes.find((s) => s.kind === 'title');
  return {
    total, scenes, blocks, captions,
    chapters: scenes.filter((s) => s.chapter).map((s) => ({ n: s.chapter, heading: s.heading ?? '' })),
    posterFrame: title ? title.from + fr(3.4) : 0,
  };
};

const ease = (p: number) => (p <= 0 ? 0 : p >= 1 ? 1 : p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2);
const mix = (a: Rect, b: Rect, p: number): Rect => ({
  x: a.x + (b.x - a.x) * p, y: a.y + (b.y - a.y) * p, w: a.w + (b.w - a.w) * p, h: a.h + (b.h - a.h) * p,
});

/** The camera rectangle at an absolute frame: each key eases in from wherever the camera was. */
export const cameraAt = (keys: CamKey[], f: number): Rect => {
  let from = keys[0].rect;
  let target = keys[0].rect;
  let at = keys[0].at;
  for (let i = 1; i < keys.length && keys[i].at <= f; i++) {
    from = mix(from, target, ease((keys[i].at - at) / ZOOM));
    target = keys[i].rect;
    at = keys[i].at;
  }
  return mix(from, target, ease((f - at) / ZOOM));
};

/** Cursor position (shot px) and click progress at an absolute frame; null while hidden. */
export const cursorAt = (moves: Segment['cursor'], f: number) => {
  let pos: { x: number; y: number } | null = null;
  let result: { x: number; y: number; opacity: number; click: number } | null = null;
  for (const m of moves) {
    if (m.at > f) break;
    const p = ease((f - m.at) / MOVE);
    if (!m.to) { result = pos && p < 1 ? { ...pos, opacity: 1 - p, click: -1 } : null; pos = null; continue; }
    const start = pos ?? { x: m.to.x + 260, y: m.to.y + 190 };
    const arc = Math.sin(p * Math.PI) * -18; // a slight arc, the way a hand moves
    result = {
      x: start.x + (m.to.x - start.x) * p, y: start.y + (m.to.y - start.y) * p + arc,
      opacity: pos ? 1 : Math.min(1, (f - m.at) / 6),
      click: m.click ? f - (m.at + MOVE) : -1,
    };
    pos = m.to;
  }
  return result;
};
