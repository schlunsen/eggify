import { AbsoluteFill, Img, OffthreadVideo, interpolate, Easing, staticFile, useCurrentFrame, useVideoConfig, spring } from 'remotion';
import { c, display, displayItalic, mono, body, stripShadow } from '../theme';
import { Clouds, Cutout, Paper, Shot, Sky, Stamp, useEnter, useJitter } from '../components/paper';
import { FPS } from '../timeline';

const s = (x: number) => Math.round(x * FPS);
const clamp = { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' } as const;

/** Small paper label, e.g. a plain-words feature name. */
const Kicker: React.FC<{ text: string; delay?: number; style?: React.CSSProperties }> = ({ text, delay = 0, style }) => {
  const p = useEnter(delay);
  return (
    <div
      style={{
        position: 'absolute', fontFamily: mono, fontWeight: 700, fontSize: 30, letterSpacing: '0.1em',
        textTransform: 'uppercase', color: c.ink, background: c.card, border: `3px solid ${c.ink}`,
        padding: '10px 20px', boxShadow: '6px 7px 0 rgba(43,29,16,0.2)', rotate: '-2deg',
        opacity: p, translate: `0 ${(1 - p) * 30}px`, ...style,
      }}
    >
      {text}
    </div>
  );
};

/** A giant foot that drops from the sky at `at` frames and lifts off at `lift`. */
const Foot: React.FC<{ at: number; lift?: number; x: number; landY: number; width?: number }> = ({ at, lift = 9999, x, landY, width = 620 }) => {
  const frame = useCurrentFrame();
  const down = interpolate(frame, [at - 7, at], [0, 1], { ...clamp, easing: Easing.in(Easing.cubic) });
  const up = interpolate(frame, [lift, lift + 12], [0, 1], { ...clamp, easing: Easing.inOut(Easing.cubic) });
  const y = interpolate(down - up, [0, 1], [-1300, landY]);
  return <Cutout src="art/foot.webp" seed="foot" width={width} style={{ left: x, top: y, zIndex: 20 }} jitter={0.5} />;
};

/** Camera shake after an impact. */
const useShake = (at: number, strength = 18) => {
  const frame = useCurrentFrame();
  const t = frame - at;
  if (t < 0 || t > 12) return { x: 0, y: 0 };
  const k = (1 - t / 12) * strength;
  return { x: Math.sin(t * 2.7) * k, y: Math.cos(t * 3.3) * k * 0.7 };
};

// ── 1. Cold open ─────────────────────────────────────────────────────────
export const Open: React.FC = () => {
  const frame = useCurrentFrame();
  const words = ['And', 'now…', 'for', 'something', 'completely', 'different.'];
  const hit = s(4.35);
  // The angel floats in and hovers where the foot will land (the foot is 780 wide, so its x is the angel's centre minus 390).
  const CHERUB_X = 900;
  const CHERUB_W = 420;
  // Where the foot touches the ground, measured on the 780-wide art: the toe is ~730px right of the art's left edge, the sole ~950px down.
  const FOOT_TOE_X = 730;
  const FOOT_SOLE_Y = 950;
  const CHERUB_BOTTOM = 190 + (CHERUB_W * 883) / 900; // art is 900x883; the foot's sole stops here
  const cherubX = interpolate(frame, [0, s(2.4)], [-500, CHERUB_X], { ...clamp, easing: Easing.out(Easing.cubic) });
  // Flattened under the sole on impact: squashed to a pancake that bulges sideways, and it stops bobbing.
  const squash = interpolate(frame, [hit, hit + 4], [0, 1], clamp);
  const bob = Math.sin(Math.min(frame, hit) / 9) * 14 * (1 - squash);
  const shake = useShake(hit);
  return (
    <Paper>
      <AbsoluteFill style={{ translate: `${shake.x}px ${shake.y}px` }}>
        <Sky />
        <Cutout src="art/cherub.webp" seed="cherub" width={CHERUB_W} style={{
          left: cherubX, top: 190 + bob, transformOrigin: '50% 100%',
          scale: `${1 + 0.3 * squash} ${1 - 0.84 * squash}`,
        }} />
        <div style={{ position: 'absolute', left: 150, right: 150, top: 430, display: 'flex', flexWrap: 'wrap', gap: '0 28px', justifyContent: 'center' }}>
          {words.map((w, i) => {
            const d = s(0.9) + i * s(0.36);
            const p = spring({ frame: frame - d, fps: FPS, config: { damping: 12, stiffness: 160 } });
            return (
              <span key={i} style={{
                fontFamily: displayItalic, fontWeight: 900, fontSize: 116, color: c.ink, lineHeight: 1.15,
                opacity: p, translate: `0 ${(1 - p) * 40}px`, display: 'inline-block',
              }}>{w}</span>
            );
          })}
        </div>
        <Foot at={hit} x={CHERUB_X + CHERUB_W / 2 - FOOT_TOE_X} landY={CHERUB_BOTTOM - FOOT_SOLE_Y} width={780} />
      </AbsoluteFill>
    </Paper>
  );
};

// ── 2. Intro ─────────────────────────────────────────────────────────────
export const Intro: React.FC = () => {
  const frame = useCurrentFrame();
  const mark = useEnter(4, { damping: 11, stiffness: 140, mass: 1 });
  const egg = useEnter(s(0.6));
  const tagline = 'The AI workstation for writing code.';
  const chars = Math.floor(interpolate(frame, [s(1.3), s(3.0)], [0, tagline.length], clamp));
  const chips = ['Your agents', 'Your code', 'Your machine'];
  return (
    <Paper>
      <div style={{ position: 'absolute', left: 150, top: 230, scale: `${0.6 + mark * 0.4}`, opacity: mark, transformOrigin: 'left center' }}>
        <div style={{ fontFamily: display, fontWeight: 900, fontSize: 250, letterSpacing: '-0.045em', lineHeight: 0.9, color: c.ink, textShadow: stripShadow() }}>
          Eggify
        </div>
      </div>
      <div style={{ position: 'absolute', left: 160, top: 560, fontFamily: display, fontWeight: 700, fontSize: 64, color: c.ink }}>
        {tagline.slice(0, chars)}
        <span style={{ opacity: frame % 20 < 10 ? 1 : 0, color: c.pinkInk }}>▍</span>
      </div>
      <div style={{ position: 'absolute', left: 160, top: 690, display: 'flex', gap: 24 }}>
        {chips.map((t, i) => {
          const p = useEnter(s(3.6) + i * s(0.55));
          return (
            <div key={t} style={{
              fontFamily: mono, fontWeight: 700, fontSize: 32, textTransform: 'uppercase', letterSpacing: '0.06em',
              background: [c.pink, c.mustard, c.teal][i], color: i === 1 ? c.ink : '#fff', border: `3px solid ${c.ink}`,
              padding: '12px 22px', boxShadow: '6px 7px 0 rgba(43,29,16,0.2)', rotate: `${[-3, 2, -1][i]}deg`,
              opacity: p, scale: `${0.6 + p * 0.4}`,
            }}>{t}</div>
          );
        })}
      </div>
      <Cutout src="art/egg.webp" seed="egg-intro" width={430} style={{ right: 140, top: 180 + (1 - egg) * 900 }} />
    </Paper>
  );
};

// ── 3. Sessions side by side ─────────────────────────────────────────────
export const Sessions: React.FC = () => {
  const a = useEnter(2);
  const b = useEnter(s(2.4));
  const tags = ['own repository', 'own branch', 'own terminal'];
  return (
    <Paper>
      <Kicker text="Parallel agent sessions" style={{ left: 90, top: 60 }} delay={4} />
      <Shot src="shots/03-overview.png" seed="ov" width={1180} tilt={-3}
        style={{ left: 80, top: 160, translate: `0 ${(1 - a) * 1100}px` }} />
      <Shot src="shots/02-session.png" seed="sess" width={1080} tilt={2.5}
        style={{ left: 760, top: 300, translate: `${(1 - b) * 1400}px 0` }} />
      {tags.map((t, i) => (
        <Stamp key={t} text={t} delay={s(3.7) + i * s(0.5)} size={44} rotate={[-6, 4, -3][i]} color={[c.pinkInk, c.teal, c.ink][i]}
          style={{ left: 120 + i * 380, top: 800 }} />
      ))}
      <Cutout src="art/sessions.webp" seed="clerks" width={520} style={{ right: 30, top: 40, opacity: b }} />
    </Paper>
  );
};

// ── 4. Watch them work ───────────────────────────────────────────────────
export const Watch: React.FC = () => {
  const frame = useCurrentFrame();
  const zoom = interpolate(frame, [0, s(7.8)], [1, 1.12], clamp);
  const meta = useEnter(s(3.1));
  const count = (to: number, start: number, dur: number) =>
    interpolate(frame, [start, start + dur], [0, to], { ...clamp, easing: Easing.out(Easing.cubic) });
  return (
    <Paper>
      <Kicker text="Read · edit · test" style={{ left: 90, top: 60 }} delay={2} />
      <div style={{ position: 'absolute', left: 80, top: 140, width: 980, scale: `${zoom}`, transformOrigin: 'left top' }}>
        <Shot src="shots/02-session-toolcards.png" seed="tools" width={980} tilt={-1.5} style={{ position: 'relative' }} label="Edit" />
      </div>
      <Stamp text="Read" delay={s(0.7)} rotate={-10} style={{ left: 140, top: 760 }} color={c.teal} />
      <Stamp text="Edit" delay={s(1.1)} rotate={6} style={{ left: 420, top: 790 }} color={c.pinkInk} />
      <Stamp text="Tested ✓" delay={s(1.6)} rotate={-4} style={{ left: 680, top: 755 }} color="#2e7d32" />
      <div style={{ position: 'absolute', right: 70, top: 150, width: 720, opacity: meta, translate: `${(1 - meta) * 800}px 0` }}>
        <Shot src="shots/02-session-tests.png" seed="tests" width={720} tilt={2} style={{ position: 'relative' }} label="go test" />
      </div>
      <div style={{ position: 'absolute', right: 110, top: 690, display: 'grid', gap: 10, opacity: meta }}>
        {[
          ['Tokens', Math.round(count(18412, s(3.6), s(1.6))).toLocaleString('en-GB')],
          ['Tool calls', Math.round(count(37, s(4.1), s(1.2))).toString()],
          ['Cost', `$${count(0.21, s(4.6), s(1.0)).toFixed(2)}`],
        ].map(([k, v]) => (
          <div key={k} style={{ display: 'flex', justifyContent: 'space-between', gap: 40, width: 470, fontFamily: mono, fontSize: 34, color: c.ink, borderBottom: `3px solid ${c.ink}`, paddingBottom: 4 }}>
            <span style={{ textTransform: 'uppercase', letterSpacing: '0.08em', fontSize: 26, alignSelf: 'end' }}>{k}</span>
            <b>{v}</b>
          </div>
        ))}
      </div>
      <Cutout src="art/metrics.webp" seed="gauge" width={190} style={{ right: 610, top: 660, opacity: meta }} />
    </Paper>
  );
};

// ── 5. Switch models ─────────────────────────────────────────────────────
export const Models: React.FC = () => {
  const a = useEnter(2);
  const providers = ['Claude', 'Codex', 'DeepSeek', 'Kimi'];
  return (
    <Paper>
      <Kicker text="Any model, any provider" style={{ left: 90, top: 60 }} delay={2} />
      <Shot src="shots/04-models.png" seed="models" width={1000} tilt={-2.5} style={{ left: 80, top: 170, translate: `0 ${(1 - a) * 1000}px` }} />
      <Cutout src="art/providers.webp" seed="operator" width={640} style={{ right: 40, top: 300 }} />
      <div style={{ position: 'absolute', right: 120, top: 90, display: 'flex', gap: 18 }}>
        {providers.map((p, i) => {
          const e = useEnter(s(2.3) + i * s(0.5), { damping: 9, stiffness: 180, mass: 0.8 });
          return (
            <div key={p} style={{
              fontFamily: display, fontWeight: 900, fontSize: 52, color: c.ink, background: [c.pink, c.mustard, c.teal, c.card][i],
              border: `3px solid ${c.ink}`, padding: '8px 22px', boxShadow: '6px 7px 0 rgba(43,29,16,0.2)',
              rotate: `${[-4, 3, -2, 4][i]}deg`, scale: `${e}`, opacity: e,
            }}>{p}</div>
          );
        })}
      </div>
      <Stamp text="Nobody minds" delay={s(4.4)} rotate={-7} style={{ left: 560, top: 770 }} size={58} />
    </Paper>
  );
};

/** A little paper-egg subagent marching across the stage (deterministic stop-motion steps). */
const Walker: React.FC<{ seed: string; y: number; from: number; to: number; start: number; dur: number; color: string; hat: number; size?: number }> = ({
  seed, y, from, to, start, dur, color, hat, size = 96,
}) => {
  const frame = useCurrentFrame();
  const t = interpolate(frame, [start, start + dur], [0, 1], clamp);
  if (frame < start) return null;
  const step = Math.floor(frame / 4);
  const stride = step % 2 === 0 ? 1 : -1;
  const bob = step % 2 === 0 ? 0 : -4;
  const dir = to > from ? 1 : -1;
  const x = from + (to - from) * t;
  const hats = [
    <path key="h" d="M30,24 L70,24 L66,6 L34,6 Z" fill={c.ink} />,
    <path key="h" d="M26,26 Q50,-4 74,26 Z" fill={c.pinkInk} />,
    <g key="h"><rect x={32} y={2} width={36} height={22} fill={c.mustard} stroke={c.ink} strokeWidth={3} /><rect x={32} y={16} width={36} height={6} fill={c.ink} /></g>,
  ];
  return (
    <svg width={size} height={size * 1.5} viewBox="0 0 100 150" style={{ position: 'absolute', left: x, top: y + bob, scale: `${dir} 1`, zIndex: 3 }}>
      <ellipse cx={54} cy={146} rx={34} ry={5} fill="rgba(43,29,16,0.2)" />
      <path d={`M40,100 L${40 + stride * 12},140 L${34 + stride * 12},142`} stroke={c.ink} strokeWidth={6} fill="none" strokeLinecap="round" />
      <path d={`M60,100 L${60 - stride * 12},140 L${54 - stride * 12},142`} stroke={c.ink} strokeWidth={6} fill="none" strokeLinecap="round" />
      <ellipse cx={55} cy={62} rx={36} ry={46} fill="rgba(43,29,16,0.2)" />
      <ellipse cx={50} cy={58} rx={36} ry={46} fill={color} stroke={c.ink} strokeWidth={3.5} />
      <circle cx={40} cy={50} r={6} fill="#fff" stroke={c.ink} strokeWidth={2.5} /><circle cx={41} cy={51} r={2.5} fill={c.ink} />
      <circle cx={62} cy={50} r={6} fill="#fff" stroke={c.ink} strokeWidth={2.5} /><circle cx={63} cy={51} r={2.5} fill={c.ink} />
      <path d="M38,72 Q50,80 62,72" stroke={c.ink} strokeWidth={3} fill="none" strokeLinecap="round" />
      <path d={`M${stride > 0 ? 14 : 18},80 L${stride > 0 ? 2 : 6},${stride > 0 ? 64 : 98}`} stroke={c.ink} strokeWidth={5} fill="none" strokeLinecap="round" />
      {hats[hat % 3]}
    </svg>
  );
};

/** Paper-style mock of the app's Subagents view: one card per subagent with a live tool feed. */
const SubagentCard: React.FC<{ style?: React.CSSProperties; delay: number }> = ({ style, delay }) => {
  const frame = useCurrentFrame();
  const rows = [
    { name: 'code-reviewer', status: 'working', col: c.teal, lines: ['TOOL:Read: src/auth/token.go', 'TOOL:Grep: "hashToken"'] },
    { name: 'test-runner', status: 'working', col: c.teal, lines: ['TOOL:Bash: go test -race ./...'] },
    { name: 'explorer', status: 'done', col: c.mustard, lines: ['TOOL:Glob: **/*_test.go'] },
  ];
  const dots = '.'.repeat(1 + (Math.floor(frame / 8) % 3));
  return (
    <div style={{ position: 'absolute', width: 640, padding: 16, background: c.card, border: `3px solid ${c.ink}`, boxShadow: '14px 16px 0 rgba(43,29,16,0.22)', ...style }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', fontFamily: mono, fontWeight: 700, color: c.ink, borderBottom: `3px solid ${c.ink}`, paddingBottom: 8, marginBottom: 12 }}>
        <span style={{ fontSize: 28, textTransform: 'uppercase', letterSpacing: '0.08em' }}>Subagents</span>
        <span style={{ fontSize: 22, background: c.mustard, padding: '2px 10px', border: `2px solid ${c.ink}` }}>⇧⌘A</span>
      </div>
      {rows.map((r, i) => {
        const e = interpolate(frame, [delay + i * 6, delay + i * 6 + 8], [0, 1], clamp);
        return (
          <div key={r.name} style={{ border: `2px solid ${c.ink}`, background: '#fff9ea', padding: '8px 12px', marginBottom: 10, opacity: e, translate: `${(1 - e) * 60}px 0` }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontFamily: display, fontWeight: 900, fontSize: 28, color: c.ink }}>
              <span>{r.name}</span>
              <span style={{ fontFamily: mono, fontSize: 20, color: r.col === c.mustard ? c.ink : '#fff', background: r.col, padding: '2px 10px', alignSelf: 'center' }}>
                {r.status === 'working' ? `working${dots}` : 'done ✓'}
              </span>
            </div>
            {r.lines.map((l) => <div key={l} style={{ fontFamily: mono, fontSize: 21, color: c.inkSoft, marginTop: 4 }}>{l}</div>)}
          </div>
        );
      })}
    </div>
  );
};

// ── 5b. Zen mode on the Mac, and the iPhone app ──────────────────────────
const Phone: React.FC<{ src: string; seed: string; width: number; tilt: number; style?: React.CSSProperties }> = ({ src, seed, width, tilt, style }) => {
  const j = useJitter(seed, 0.6);
  return (
    <div style={{
      position: 'absolute', width, padding: 12, background: c.ink, borderRadius: 54,
      boxShadow: '14px 16px 0 rgba(43,29,16,0.25)', border: `3px solid ${c.ink}`,
      translate: `${j.x}px ${j.y}px`, rotate: `${tilt + j.r * 0.5}deg`, ...style,
    }}>
      <Img src={staticFile(src)} style={{ width: '100%', display: 'block', borderRadius: 42 }} />
    </div>
  );
};

export const ZenPhone: React.FC = () => {
  const a = useEnter(2);
  const p1 = useEnter(s(3.6), { damping: 11, stiffness: 120, mass: 1 });
  const j = useJitter('zenvid', 0.6);
  const frame = useCurrentFrame();
  const cardIn = useEnter(s(0.9));
  const cardOut = interpolate(frame, [s(3.3), s(3.9)], [0, 1], { ...clamp, easing: Easing.in(Easing.cubic) });
  const walkers = [
    { color: '#e9a9c2', hat: 0, from: -120, to: 1980, start: s(0.6), dur: s(8.2), y: 900 },
    { color: '#a8d8cc', hat: 1, from: -420, to: 1980, start: s(1.0), dur: s(8.0), y: 925 },
    { color: '#ecd27a', hat: 2, from: 2060, to: -140, start: s(1.8), dur: s(7.4), y: 905 },
    { color: '#f6eedb', hat: 0, from: -700, to: 1980, start: s(2.6), dur: s(6.4), y: 930 },
  ];
  return (
    <Paper>
      {walkers.map((w, i) => <Walker key={i} seed={`w${i}`} {...w} />)}
      <Kicker text="Zen mode" style={{ left: 90, top: 60 }} delay={2} />
      <Kicker text="Subagents on stage" style={{ left: 420, top: 60, background: c.mustard }} delay={s(0.8)} />
      <Kicker text="iPhone app" style={{ right: 130, top: 60 }} delay={s(3.4)} />
      <div style={{
        position: 'absolute', left: 80, top: 170, width: 1080, padding: 14, background: c.card, border: `3px solid ${c.ink}`,
        boxShadow: '14px 16px 0 rgba(43,29,16,0.22)', rotate: `${-2 + j.r * 0.5}deg`, translate: `${(1 - a) * -1400}px 0`,
      }}>
        <OffthreadVideo src={staticFile('video/zen.mp4')} muted style={{ width: '100%', display: 'block', border: `1.5px solid ${c.edge}` }} />
      </div>
      <SubagentCard delay={s(1.2)} style={{ left: 1230, top: 190, rotate: '3deg', opacity: cardIn * (1 - cardOut), translate: `${(1 - cardIn) * 800 + cardOut * 700}px 0`, zIndex: 4 }} />
      <Phone src="shots/11-phone-home.png" seed="ph1" width={360} tilt={-5} style={{ right: 240, top: 220, translate: `0 ${(1 - p1) * 1200}px` }} />
      <Stamp text="From the sofa" delay={s(6.2)} rotate={-6} style={{ left: 330, top: 830 }} color={c.pinkInk} size={66} />
    </Paper>
  );
};

/** Hand-cut "YOLO mode" badge with its toggle flipped on. */
const YoloBadge: React.FC<{ delay: number; style?: React.CSSProperties }> = ({ delay, style }) => {
  const frame = useCurrentFrame();
  const p = spring({ frame: frame - delay, fps: FPS, config: { damping: 9, stiffness: 150, mass: 0.9 } });
  const flip = interpolate(frame, [delay + 8, delay + 14], [0, 1], clamp);
  if (frame < delay) return null;
  return (
    <div style={{
      position: 'absolute', width: 640, padding: '12px 22px 14px', whiteSpace: 'nowrap', background: c.mustard, border: `5px solid ${c.ink}`,
      boxShadow: '12px 14px 0 rgba(43,29,16,0.25)', rotate: '-4deg', scale: `${0.5 + p * 0.5}`, opacity: Math.min(1, p * 2), zIndex: 10, ...style,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 20 }}>
        <span style={{ fontFamily: display, fontWeight: 900, fontSize: 76, lineHeight: 1, color: c.ink, textShadow: stripShadow(0.03) }}>YOLO mode</span>
        <svg width={110} height={60} viewBox="0 0 110 60">
          <rect x={3} y={3} width={104} height={54} rx={27} fill={flip > 0.5 ? c.pinkInk : '#cdbf9c'} stroke={c.ink} strokeWidth={4} />
          <circle cx={30 + flip * 50} cy={30} r={19} fill={c.card} stroke={c.ink} strokeWidth={4} />
        </svg>
      </div>
      <div style={{ fontFamily: mono, fontWeight: 700, fontSize: 30, letterSpacing: '0.02em', color: c.ink, marginTop: 6 }}>
        Approvals off. Your call.
      </div>
    </div>
  );
};

// ── 6. The cage ──────────────────────────────────────────────────────────
export const Cage: React.FC = () => {
  const frame = useCurrentFrame();
  const a = useEnter(2);
  const drop = spring({ frame: frame - s(2.3), fps: FPS, config: { damping: 8, stiffness: 120, mass: 1.2 } });
  return (
    <Paper>
      <Kicker text="Sandbox & approvals" style={{ left: 90, top: 60 }} delay={2} />
      <Shot src="shots/05-sandbox.png" seed="sandbox" width={1080} tilt={-2} style={{ left: 80, top: 170, translate: `${(1 - a) * -1400}px 0` }} />
      <Cutout src="art/sandbox.webp" seed="cage" width={400} style={{ right: 90, top: interpolate(drop, [0, 1], [-900, 120]) }} />
      <Stamp text="Sandboxed" delay={s(3.9)} rotate={8} style={{ left: 50, top: 890 }} color={c.teal} size={50} />
      <YoloBadge delay={s(4.8)} style={{ right: 60, top: 740 }} />
    </Paper>
  );
};

// ── 7. One Go binary + the foot ──────────────────────────────────────────
export const Binary: React.FC = () => {
  const frame = useCurrentFrame();
  const fan = useEnter(2);
  const stomp = s(5.4);
  const squash = interpolate(frame, [stomp - 1, stomp + 2], [1, 0.2], clamp);
  const shake = useShake(stomp, 24);
  const splat = interpolate(frame, [stomp + 2, stomp + 7], [0, 1], clamp);
  return (
    <Paper>
      <AbsoluteFill style={{ translate: `${shake.x}px ${shake.y}px` }}>
        {[
          ['shots/01-dashboard.png', -9, 60, 120],
          ['shots/06-stats.png', 4, 420, 90],
          ['shots/09-themes.png', -2, 780, 150],
        ].map(([src, tilt, left, top], i) => (
          <Shot key={src as string} src={src as string} seed={`fan${i}`} width={760} tilt={tilt as number}
            style={{ left: left as number, top: top as number, opacity: fan, translate: `0 ${(1 - fan) * (600 + i * 200)}px` }} />
        ))}
        <Stamp text="One Go binary" delay={s(1.0)} rotate={-6} style={{ left: 110, top: 560 }} size={76} color={c.ink} />
        <Stamp text="No dependencies" delay={s(1.9)} rotate={4} style={{ left: 420, top: 680 }} size={70} color={c.pinkInk} />
        <Stamp text="Considerable dignity" delay={s(2.8)} rotate={-3} style={{ left: 150, top: 800 }} size={70} color={c.teal} />
        <div style={{ position: 'absolute', right: 160, bottom: 40, width: 340, height: 600, transformOrigin: '50% 100%', scale: `${1 + (1 - squash) * 0.6} ${squash}` }}>
          <Img src={staticFile('art/egg.webp')} style={{ width: '100%', position: 'absolute', bottom: 0 }} />
        </div>
        <Foot at={stomp} lift={stomp + s(1.9)} x={1280} landY={-60} width={720} />
        <div style={{
          position: 'absolute', right: 200, bottom: 380, fontFamily: displayItalic, fontWeight: 900, fontSize: 120, color: c.ink,
          background: c.mustard, border: `5px solid ${c.ink}`, padding: '4px 36px', rotate: '-9deg', scale: `${splat}`, zIndex: 30,
          boxShadow: '10px 12px 0 rgba(43,29,16,0.25)',
        }}>Splat!</div>
      </AbsoluteFill>
    </Paper>
  );
};

// ── 8. End card ──────────────────────────────────────────────────────────
export const End: React.FC = () => {
  const frame = useCurrentFrame();
  const mark = useEnter(4, { damping: 10, stiffness: 130, mass: 1 });
  const egg = useEnter(s(0.8), { damping: 7, stiffness: 140, mass: 0.9 });
  const url = useEnter(s(1.8));
  const line = interpolate(frame, [s(2.2), s(2.8)], [0, 1], clamp);
  const fade = interpolate(frame, [s(4.4), s(5.1)], [0, 1], clamp);
  return (
    <Paper>
      <Clouds />
      <Cutout src="art/cherub.webp" seed="cherub-end" width={300} style={{ right: 120, top: 210 + Math.sin(frame / 10) * 12 }} />
      <Cutout src="art/egg.webp" seed="egg-end" width={330} style={{ left: 170, bottom: -10, translate: `0 ${(1 - egg) * 700}px` }} />
      <div style={{ position: 'absolute', left: 0, right: 0, top: 330, textAlign: 'center', scale: `${0.7 + mark * 0.3}`, opacity: mark }}>
        <div style={{ fontFamily: display, fontWeight: 900, fontSize: 230, letterSpacing: '-0.045em', lineHeight: 0.9, color: c.ink, textShadow: stripShadow() }}>Eggify</div>
      </div>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 600, textAlign: 'center', fontFamily: display, fontWeight: 700, fontSize: 54, color: c.ink, opacity: line }}>
        The AI workstation for writing code.
      </div>
      <div style={{
        position: 'absolute', left: '50%', top: 720, translate: `-50% ${(1 - url) * 40}px`, opacity: url,
        fontFamily: mono, fontWeight: 700, fontSize: 52, letterSpacing: '0.06em', color: c.ink, background: c.mustard,
        border: `4px solid ${c.ink}`, padding: '14px 40px', boxShadow: '8px 10px 0 rgba(43,29,16,0.22)', rotate: '-2deg',
      }}>eggify.xyz</div>
      <AbsoluteFill style={{ background: c.umber, opacity: fade }} />
    </Paper>
  );
};
