// The tutorial stage: one paper-framed screen that stays put while chapters, shots, zooms,
// highlight rings, the cursor and labels play on it. The screen itself never tilts or jitters.
import { Easing, Freeze, Img, OffthreadVideo, Sequence, interpolate, random, staticFile, useCurrentFrame } from 'remotion';
import type { Rect } from '../types';
import { c, display, mono } from '../theme';
import { Cutout, Paper } from './paper';
import { FADE, PUSH, VIEW, W, cameraAt, cursorAt, type Block, type Mark, type Segment, type Timeline } from '../timeline';

type StageBlock = Extract<Block, { kind: 'stage' }>;
const clamp = { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' } as const;
const FRAME = 15; // card stock around the screen: 3 px ink edge + 12 px paper

/** Shot px → px inside the viewport, for the camera rectangle `cam`. */
const project = (cam: Rect) => {
  const s = VIEW.w / cam.w;
  return { s, x: (v: number) => (v - cam.x) * s, y: (v: number) => (v - cam.y) * s };
};

/** A closed loop drawn by hand around a box: a soft rounded rectangle that overshoots where it started. */
const ringPath = (seed: string, x: number, y: number, w: number, h: number) => {
  const cx = x + w / 2, cy = y + h / 2, a = w / 2, b = h / 2;
  const n = Math.max(22, Math.round((w + h) / 26));
  const turns = 1.06;
  const pts: [number, number][] = [];
  for (let i = 0; i <= n * turns; i++) {
    const th = -2.5 + (i / n) * Math.PI * 2;
    const co = Math.cos(th), si = Math.sin(th);
    const k = 0.34; // squircle exponent: close to a rounded rectangle
    const wob = (random(`${seed}${i}`) - 0.5) * 4 + (i > n ? 2.5 : 0); // the overshoot runs just outside the start
    pts.push([
      cx + Math.sign(co) * Math.pow(Math.abs(co), k) * (a + wob),
      cy + Math.sign(si) * Math.pow(Math.abs(si), k) * (b + wob),
    ]);
  }
  // Catmull-Rom through the points, as cubic Béziers.
  let d = `M${pts[0][0].toFixed(1)},${pts[0][1].toFixed(1)}`;
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[Math.max(0, i - 1)], p1 = pts[i], p2 = pts[i + 1], p3 = pts[Math.min(pts.length - 1, i + 2)];
    d += ` C${(p1[0] + (p2[0] - p0[0]) / 6).toFixed(1)},${(p1[1] + (p2[1] - p0[1]) / 6).toFixed(1)} ${(p2[0] - (p3[0] - p1[0]) / 6).toFixed(1)},${(p2[1] - (p3[1] - p1[1]) / 6).toFixed(1)} ${p2[0].toFixed(1)},${p2[1].toFixed(1)}`;
  }
  return d;
};

/** Highlight ring and paper label for one mark, in viewport px. */
const MarkView: React.FC<{ mark: Mark; cam: Rect; f: number }> = ({ mark, cam, f }) => {
  const t = f - mark.from;
  if (t < 0 || f >= mark.to) return null;
  const out = interpolate(f, [mark.to - 6, mark.to], [1, 0], clamp);
  const pr = project(cam);
  const pad = 9;
  const r = mark.rect
    ? { x: pr.x(mark.rect.x) - pad, y: pr.y(mark.rect.y) - pad, w: mark.rect.w * pr.s + pad * 2, h: mark.rect.h * pr.s + pad * 2 }
    : null;
  const draw = interpolate(t, [0, 13], [0, 1], { ...clamp, easing: Easing.out(Easing.cubic) });
  // The ink "boils": the same loop redrawn slightly differently, on eights.
  const d = r && mark.ring ? ringPath(`ring${mark.from}-${Math.floor(f / 8) % 3}`, r.x, r.y, r.w, r.h) : null;

  let label: React.ReactNode = null;
  if (mark.label) {
    const lw = mark.label.length * 16.3 + 44; // Courier Prime 27 px is 0.6 em wide
    const lh = 54;
    const below = r ? r.y + r.h + 26 : 34;
    const top = r && below + lh > VIEW.h - 14 ? r.y - lh - 26 : below;
    const left = Math.min(VIEW.w - lw - 18, Math.max(18, r ? r.x + Math.min(20, r.w / 2) : (VIEW.w - lw) / 2));
    const p = interpolate(t, [mark.ring ? 8 : 0, mark.ring ? 16 : 8], [0, 1], { ...clamp, easing: Easing.out(Easing.back(1.6)) });
    label = (
      <div
        style={{
          position: 'absolute', left, top, height: lh, boxSizing: 'border-box', whiteSpace: 'nowrap',
          fontFamily: mono, fontWeight: 700, fontSize: 27, lineHeight: '42px', color: c.ink,
          background: c.card, border: `3px solid ${c.ink}`, padding: '3px 19px', boxShadow: '6px 7px 0 rgba(43,29,16,0.28)',
          rotate: '-1.5deg', scale: 0.7 + p * 0.3, opacity: Math.min(1, p * 1.5), transformOrigin: 'left center',
        }}
      >
        {mark.label}
      </div>
    );
  }
  return (
    <div style={{ position: 'absolute', inset: 0, opacity: out }}>
      {d ? (
        <svg width={VIEW.w} height={VIEW.h} style={{ position: 'absolute', inset: 0, overflow: 'visible' }}>
          <path d={d} fill="none" stroke="rgba(43,29,16,0.22)" strokeWidth={6} strokeLinecap="round" transform="translate(3,4)" pathLength={1} strokeDasharray="1 1" strokeDashoffset={1 - draw} />
          <path d={d} fill="none" stroke={c.pink} strokeWidth={6} strokeLinecap="round" pathLength={1} strokeDasharray="1 1" strokeDashoffset={1 - draw} />
        </svg>
      ) : null}
      {label}
    </div>
  );
};

/** The paper cursor, with a ripple when it clicks. */
const CursorView: React.FC<{ seg: Segment; cam: Rect; f: number }> = ({ seg, cam, f }) => {
  const cur = cursorAt(seg.cursor, f);
  if (!cur) return null;
  const pr = project(cam);
  const x = pr.x(cur.x), y = pr.y(cur.y);
  const k = cur.click;
  const press = k >= 0 && k < 5 ? 0.84 : 1;
  const ripple = k >= 0 && k <= 18 ? k / 18 : -1;
  return (
    <div style={{ position: 'absolute', inset: 0, opacity: cur.opacity }}>
      {ripple >= 0 ? (
        <div
          style={{
            position: 'absolute', left: x, top: y, width: 0, height: 0,
            boxSizing: 'content-box', translate: '-50% -50%', borderRadius: '50%',
            padding: 12 + ripple * 34, border: `5px solid ${c.pink}`, opacity: 1 - ripple * ripple,
          }}
        />
      ) : null}
      <svg width={48} height={60} viewBox="-3 -3 32 40" style={{ position: 'absolute', left: x - 5, top: y - 4, scale: press, transformOrigin: '10% 8%', filter: 'drop-shadow(4px 5px 0 rgba(43,29,16,0.3))' }}>
        <path d="M1,1 L1,27 L8,21 L13,32 L18,30 L13,19 L22,19 Z" fill={c.ink} stroke={c.card} strokeWidth={2.4} strokeLinejoin="round" />
      </svg>
    </div>
  );
};

/** One shot or clip inside the viewport, with its camera and everything drawn on top of it. */
const SegmentView: React.FC<{ slug: string; seg: Segment; marks: Mark[]; exit: Segment['enter'] }> = ({ slug, seg, marks, exit }) => {
  const local = useCurrentFrame();
  const f = local + seg.from;
  const cam = cameraAt(seg.cam, f);
  const pr = project(cam);
  const media = { position: 'absolute', left: pr.x(0), top: pr.y(0), width: W * pr.s, height: (W * pr.s * VIEW.h) / VIEW.w, maxWidth: 'none' } as const;
  const leaving = f - seg.to;
  return (
    <div
      style={{
        position: 'absolute', inset: 0, overflow: 'hidden', background: c.umber,
        translate:
          seg.enter === 'push' && local < PUSH
            ? interpolate(local, [0, PUSH], [`${VIEW.w + 30}px 0px`, '0px 0px'], { ...clamp, easing: Easing.bezier(0.2, 0.8, 0.2, 1) })
            : exit === 'push' && leaving > 0
              ? interpolate(leaving, [0, PUSH], ['0px 0px', `${-VIEW.w * 0.22}px 0px`], { ...clamp, easing: Easing.bezier(0.2, 0.8, 0.2, 1) })
              : undefined,
        opacity: seg.enter === 'fade' ? interpolate(local, [0, FADE], [0, 1], clamp) : 1,
        boxShadow: seg.enter === 'push' ? '-18px 0 0 rgba(43,29,16,0.25)' : undefined,
      }}
    >
      {seg.kind === 'shot' ? (
        <Img src={staticFile(`episodes/${slug}/shots/${seg.name}.png`)} style={media} />
      ) : (
        <Freeze frame={seg.clipFrames ? Math.min(local, seg.clipFrames - 2) : local} active={!!seg.clipFrames && local > seg.clipFrames - 2}>
          <OffthreadVideo src={staticFile(`episodes/${slug}/clips/${seg.name}.mp4`)} muted style={media} />
        </Freeze>
      )}
      {marks.map((m) => <MarkView key={m.from} mark={m} cam={cam} f={f} />)}
      <CursorView seg={seg} cam={cam} f={f} />
    </div>
  );
};

/** The chapter name, stuck on the top edge of the card like a strip of mustard tape. */
const HeadingLabel: React.FC<{ n: number; text: string; delay: number }> = ({ n, text, delay }) => {
  const t = useCurrentFrame() - delay;
  if (t < 0) return null;
  return (
    <div
      style={{
        position: 'absolute', left: VIEW.x + 26, top: VIEW.y - FRAME - 31, rotate: '-1.6deg', transformOrigin: 'left bottom',
        display: 'flex', alignItems: 'baseline', gap: 16,
        background: 'rgba(212,160,23,0.96)', padding: '7px 22px 6px', boxShadow: '4px 5px 0 rgba(43,29,16,0.22)',
        fontFamily: mono, fontWeight: 700, fontSize: 30, letterSpacing: '0.08em', textTransform: 'uppercase', color: c.ink,
        scale: interpolate(t, [0, 4, 7], [1.5, 0.96, 1], clamp), opacity: interpolate(t, [0, 2], [0, 1], clamp),
      }}
    >
      <span style={{ fontFamily: display, fontWeight: 900, fontSize: 32, letterSpacing: 0 }}>{n}</span>
      <span>{text}</span>
    </div>
  );
};

/** Index tabs down the left edge of the card: one per chapter, the current one pulled out. */
const Tabs: React.FC<{ block: StageBlock; chapters: Timeline['chapters']; offset: number }> = ({ block, chapters, offset }) => {
  const f = useCurrentFrame() + offset;
  const current = [...block.scenes].reverse().find((s) => f >= s.from)?.chapter ?? block.scenes[0].chapter;
  return (
    <>
      {chapters.map((ch, i) => {
        const sc = block.scenes.find((s) => s.chapter === ch.n);
        const on = ch.n === current;
        const done = ch.n < current;
        const pull = on && sc ? interpolate(f - sc.from, [0, 8], [0, 1], { ...clamp, easing: Easing.out(Easing.back(2)) }) : 0;
        return (
          <div
            key={ch.n}
            style={{
              position: 'absolute', left: VIEW.x - FRAME - 46, top: VIEW.y + 26 + i * 66, width: 80, height: 54, boxSizing: 'border-box',
              background: on ? c.mustard : done ? c.card : c.paperLight, border: `3px solid ${c.ink}`, borderRadius: '9px 0 0 9px',
              boxShadow: '5px 6px 0 rgba(43,29,16,0.16)', translate: `${-pull * 16}px 0`,
              fontFamily: display, fontWeight: 900, fontSize: 28, lineHeight: '48px', paddingLeft: on ? 20 : 17,
              color: on || done ? c.ink : 'rgba(92,70,48,0.55)',
            }}
          >
            {done ? '✓' : ch.n}
          </div>
        );
      })}
    </>
  );
};

export const Stage: React.FC<{ block: StageBlock; tl: Timeline; slug: string; number: number; title: string; offset: number }> = ({
  block, tl, slug, number, title, offset,
}) => {
  const end = block.from + block.dur;
  return (
    <Paper quiet>
      {/* series mark, top right */}
      <div style={{ position: 'absolute', right: W - (VIEW.x + VIEW.w + FRAME), top: 30, fontFamily: mono, fontSize: 23, letterSpacing: '0.1em', textTransform: 'uppercase', color: c.inkSoft }}>
        <b style={{ color: c.ink }}>Eggify · Learn</b>&nbsp;&nbsp;№&nbsp;{number} · {title}
      </div>
      <Tabs block={block} chapters={tl.chapters} offset={offset} />
      <Cutout src="art/egg.webp" seed="stage-egg" width={148} jitter={0.5} style={{ left: VIEW.x + VIEW.w + FRAME + 6, top: 812 }} />
      {/* the card */}
      <div
        style={{
          position: 'absolute', left: VIEW.x - FRAME, top: VIEW.y - FRAME, width: VIEW.w + FRAME * 2, height: VIEW.h + FRAME * 2, boxSizing: 'border-box',
          background: c.card, border: `3px solid ${c.ink}`, boxShadow: '14px 16px 0 rgba(43,29,16,0.22)',
        }}
      />
      <div style={{ position: 'absolute', left: VIEW.x, top: VIEW.y, width: VIEW.w, height: VIEW.h, overflow: 'hidden', background: c.umber }}>
        {block.segments.map((seg, i) => {
          const next = block.segments[i + 1];
          const hold = next ? (next.enter === 'push' ? PUSH : FADE) : 0;
          return (
            <Sequence key={`${seg.name}-${seg.from}`} name={`${seg.kind} ${seg.name}`} layout="none" from={seg.from - offset} durationInFrames={Math.min(end, seg.to + hold) - seg.from}>
              <SegmentView slug={slug} seg={seg} marks={block.marks.filter((m) => m.seg === i)} exit={next?.enter ?? 'none'} />
            </Sequence>
          );
        })}
        <div style={{ position: 'absolute', inset: 0, boxShadow: `inset 0 0 0 1.5px ${c.edge}`, pointerEvents: 'none' }} />
      </div>
      {block.scenes.map((s, i) => (
        <Sequence key={s.id} name={`heading ${s.id}`} layout="none" from={i === 0 ? 0 : s.from - offset} durationInFrames={s.from + s.dur - offset - (i === 0 ? 0 : s.from - offset)}>
          <HeadingLabel n={s.chapter} text={s.heading ?? ''} delay={i === 0 ? 4 : 5} />
        </Sequence>
      ))}
    </Paper>
  );
};
