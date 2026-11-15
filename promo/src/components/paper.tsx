import { AbsoluteFill, Img, interpolate, random, spring, staticFile, useCurrentFrame, useVideoConfig } from 'remotion';
import type { CSSProperties, ReactNode } from 'react';
import { c, mono, display, body } from '../theme';

const grain =
  "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='260' height='260'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='.8' numOctaves='3' stitchTiles='stitch'/%3E%3CfeColorMatrix values='0 0 0 0 .17 0 0 0 0 .11 0 0 0 0 .06 0 0 0 .09 0'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E\")";

const W = 1920;
const H = 1080;

/** Hand-cut hill silhouette: straight scissor cuts along a few summed waves. */
const hillPath = (seed: string, base: number, amp: number, step: number, wave: number) => {
  const pts: string[] = [];
  const ph = random(`${seed}ph`) * 6;
  for (let x = -40; x <= W + 40; x += step) {
    const n = Math.sin(x / wave + ph) * amp + Math.sin(x / (wave * 0.37) + ph * 2) * amp * 0.35;
    const cut = (random(`${seed}${x}`) - 0.5) * 7;
    pts.push(`${x},${(base - n + cut).toFixed(1)}`);
  }
  return `M-40,${H + 40} L${pts.join(' L')} L${W + 40},${H + 40} Z`;
};

const HILLS = [
  { id: 'far', fill: '#cddcc6', base: 800, amp: 46, step: 30, wave: 190, speed: 0.4 },
  { id: 'mid', fill: '#dccd9f', base: 900, amp: 40, step: 26, wave: 150, speed: 0.7 },
  { id: 'near', fill: '#cdb98a', base: 985, amp: 30, step: 22, wave: 120, speed: 1 },
].map((h) => ({ ...h, d: hillPath(h.id, h.base, h.amp, h.step, h.wave) }));

const CLOUD_BLOBS = 'M20,70 C0,70 0,44 24,42 C26,20 58,14 70,34 C84,12 124,16 128,42 C156,38 166,70 140,72 Z';
const CLOUDS_FAR = [
  { y: 150, s: 0.9, v: 9, o: 0.55, x0: 100 },
  { y: 300, s: 0.7, v: 6, o: 0.45, x0: 900 },
  { y: 90, s: 1.2, v: 13, o: 0.7, x0: 1500 },
  { y: 520, s: 0.6, v: 5, o: 0.4, x0: 400 },
  { y: 640, s: 0.8, v: 16, o: 0.5, x0: 1300 },
];

const STARS = Array.from({ length: 16 }, (_, i) => ({
  x: random(`sx${i}`) * W, y: random(`sy${i}`) * 520 + 30, r: 8 + random(`sr${i}`) * 12, ph: Math.floor(random(`sp${i}`) * 8),
}));

const star = (x: number, y: number, r: number) =>
  `M${x},${y - r} L${x + r * 0.22},${y - r * 0.22} L${x + r},${y} L${x + r * 0.22},${y + r * 0.22} L${x},${y + r} L${x - r * 0.22},${y + r * 0.22} L${x - r},${y} L${x - r * 0.22},${y - r * 0.22} Z`;

/** Layered paper diorama behind every scene: sun, drifting clouds, bunting, cut-out hills, vignette. */
const Backdrop: React.FC = () => {
  const frame = useCurrentFrame();
  const jFar = useJitter('bg-far', 0.8, 6);
  const jMid = useJitter('bg-mid', 0.9, 5);
  const jNear = useJitter('bg-near', 1, 4);
  const jSun = useJitter('bg-sun', 0.6, 8);
  const jit = { far: jFar, mid: jMid, near: jNear };
  const tick = Math.floor(frame / 6) * 6; // rays turn on sixes
  const rays = Array.from({ length: 14 }, (_, i) => {
    const a = ((i * 360) / 14 + tick * 0.35) * (Math.PI / 180);
    const a2 = a + 0.12;
    const R = 1500;
    return `M1640,150 L${1640 + Math.cos(a) * R},${150 + Math.sin(a) * R} L${1640 + Math.cos(a2) * R},${150 + Math.sin(a2) * R} Z`;
  });
  const flags = Array.from({ length: 17 }, (_, i) => i);
  const flagColors = [c.pink, c.mustard, c.teal];
  return (
    <AbsoluteFill style={{ pointerEvents: 'none' }}>
      <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`} style={{ position: 'absolute', inset: 0 }}>
        {/* sun with hand-cut rays */}
        <g style={{ translate: `${jSun.x}px ${jSun.y}px` }}>
          {rays.map((d, i) => <path key={i} d={d} fill={c.mustard} opacity={0.07} />)}
          <circle cx={1646} cy={158} r={92} fill="rgba(43,29,16,0.12)" />
          <circle cx={1640} cy={150} r={90} fill="#ecc45a" opacity={0.55} />
          <circle cx={1640} cy={150} r={64} fill="#f3d57c" opacity={0.6} />
        </g>
        {/* stars */}
        {STARS.map((st, i) => {
          const on = (Math.floor(frame / 8) + st.ph) % 5 !== 0;
          return <path key={i} d={star(st.x, st.y, st.r)} fill={c.mustard} opacity={on ? 0.28 : 0.1} />;
        })}
        {/* distant castle on the far hill */}
        <g style={{ translate: `${jFar.x}px ${jFar.y}px` }} fill="#b9c9b4">
          <rect x={1270} y={690} width={120} height={120} />
          <rect x={1250} y={650} width={34} height={160} />
          <rect x={1376} y={660} width={34} height={150} />
          <path d="M1246,650 L1267,610 L1288,650 Z M1372,660 L1393,622 L1414,660 Z" fill="#a9bba5" />
          {[1276, 1296, 1316, 1336, 1356].map((x) => <rect key={x} x={x} y={676} width={10} height={14} fill="#b9c9b4" />)}
        </g>
        {/* hills, each on its own stop-motion step with a paper shadow */}
        {HILLS.map((h) => {
          const j = jit[h.id as keyof typeof jit];
          return (
            <g key={h.id} style={{ translate: `${j.x * h.speed}px ${j.y * h.speed}px` }}>
              <path d={h.d} fill="rgba(43,29,16,0.16)" transform="translate(7,-9)" />
              <path d={h.d} fill={h.fill} stroke="rgba(43,29,16,0.22)" strokeWidth={2} />
            </g>
          );
        })}
        {/* bunting along the top */}
        <g opacity={0.55}>
          <path d={`M0,10 Q480,${70 + Math.sin(frame / 40) * 4} 960,14 T1920,10`} stroke={c.inkSoft} strokeWidth={2.5} fill="none" />
          {flags.map((i) => {
            const x = 40 + i * 112;
            const y = 14 + Math.sin(((x / 960) * Math.PI)) * 24;
            const sw = Math.sin(frame / 14 + i) * 3;
            return <path key={i} d={`M${x - 24},${y + 4} L${x + 24},${y + 4} L${x + sw},${y + 56} Z`} fill={flagColors[i % 3]} stroke="rgba(43,29,16,0.35)" strokeWidth={1.5} />;
          })}
        </g>
        {/* clouds at several depths and speeds */}
        {CLOUDS_FAR.map((cl, i) => {
          const span = W + 420;
          const x = ((cl.x0 + frame * cl.v * 0.5) % span) - 260;
          const bob = Math.round(Math.sin(frame / 30 + i) * 3);
          return (
            <g key={i} transform={`translate(${x.toFixed(1)},${cl.y + bob}) scale(${cl.s * 1.6})`} opacity={cl.o}>
              <path d={CLOUD_BLOBS} fill="rgba(43,29,16,0.14)" transform="translate(5,6)" />
              <path d={CLOUD_BLOBS} fill="#f8f1de" stroke="rgba(43,29,16,0.22)" strokeWidth={1.5} />
            </g>
          );
        })}
      </svg>
      {/* soft paper vignette */}
      <AbsoluteFill style={{ background: 'radial-gradient(ellipse 75% 70% at 50% 48%, transparent 55%, rgba(92,70,48,0.2) 100%)' }} />
    </AbsoluteFill>
  );
};

/** Aged paper with grain and engraving hatch, and a layered paper diorama behind the scene. */
export const Paper: React.FC<{ color?: string; children?: ReactNode }> = ({ color = c.paper, children }) => (
  <AbsoluteFill
    style={{
      backgroundColor: color,
      backgroundImage: `${grain}, repeating-linear-gradient(135deg, rgba(43,29,16,0.035) 0 2px, transparent 2px 12px), repeating-linear-gradient(52deg, rgba(120,90,50,0.025) 0 1px, transparent 1px 7px)`,
    }}
  >
    <Backdrop />
    {children}
  </AbsoluteFill>
);

/**
 * Stop-motion jitter: a tiny random offset/rotation that changes every `every`
 * frames, like cut-out animation shot on fours.
 */
export const useJitter = (seed: string, amp = 1, every = 4) => {
  const frame = useCurrentFrame();
  const step = Math.floor(frame / every);
  return {
    x: (random(`${seed}x${step}`) - 0.5) * 4 * amp,
    y: (random(`${seed}y${step}`) - 0.5) * 4 * amp,
    r: (random(`${seed}r${step}`) - 0.5) * 1.2 * amp,
  };
};

/** Spring that starts at `delay` frames. */
export const useEnter = (delay = 0, config = { damping: 13, stiffness: 120, mass: 0.9 }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  return spring({ frame: frame - delay, fps, config });
};

/** A paper cut-out image with jitter and a hard paper shadow. */
export const Cutout: React.FC<{
  src: string; width: number; seed: string; style?: CSSProperties; jitter?: number; shadow?: boolean;
}> = ({ src, width, seed, style, jitter = 1, shadow = true }) => {
  const j = useJitter(seed, jitter);
  return (
    <Img
      src={staticFile(src)}
      style={{
        position: 'absolute',
        width,
        filter: shadow ? 'drop-shadow(8px 10px 0 rgba(43,29,16,0.2))' : undefined,
        translate: `${j.x}px ${j.y}px`,
        rotate: `${j.r}deg`,
        ...style,
      }}
    />
  );
};

/** A real app screenshot glued onto card stock. */
export const Shot: React.FC<{
  src: string; width: number; seed: string; style?: CSSProperties; label?: string; tilt?: number;
}> = ({ src, width, seed, style, label, tilt = 0 }) => {
  const j = useJitter(seed, 0.6);
  return (
    <div
      style={{
        position: 'absolute',
        width,
        padding: 14,
        background: c.card,
        border: `3px solid ${c.ink}`,
        boxShadow: `14px 16px 0 rgba(43,29,16,0.22)`,
        translate: `${j.x}px ${j.y}px`,
        rotate: `${tilt + j.r * 0.5}deg`,
        ...style,
      }}
    >
      <Img src={staticFile(src)} style={{ width: '100%', display: 'block', border: `1.5px solid ${c.edge}` }} />
      {label ? (
        <div
          style={{
            position: 'absolute', top: -26, left: 40, rotate: '-3deg',
            background: 'rgba(212,160,23,0.92)', padding: '6px 18px',
            fontFamily: mono, fontWeight: 700, fontSize: 24, letterSpacing: '0.08em', textTransform: 'uppercase', color: c.ink,
          }}
        >
          {label}
        </div>
      ) : null}
    </div>
  );
};

/** A rubber stamp that slams onto the page. */
export const Stamp: React.FC<{ text: string; delay: number; color?: string; rotate?: number; style?: CSSProperties; size?: number }> = ({
  text, delay, color = c.pinkInk, rotate = -8, style, size = 64,
}) => {
  const frame = useCurrentFrame();
  const t = frame - delay;
  if (t < 0) return null;
  const scale = interpolate(t, [0, 4, 7], [2.4, 0.92, 1], { extrapolateRight: 'clamp' });
  const opacity = interpolate(t, [0, 2], [0, 1], { extrapolateRight: 'clamp' });
  return (
    <div
      style={{
        position: 'absolute',
        fontFamily: display, fontWeight: 900, fontSize: size, lineHeight: 1,
        color, border: `6px solid ${color}`, borderRadius: 10, padding: '10px 26px',
        textTransform: 'uppercase', letterSpacing: '0.02em',
        rotate: `${rotate}deg`, scale: `${scale}`, opacity,
        mixBlendMode: 'multiply', background: 'rgba(255,255,255,0.15)',
        ...style,
      }}
    >
      {text}
    </div>
  );
};

/** Row of scalloped paper clouds hanging from the top edge. */
export const Clouds: React.FC<{ y?: number; color?: string }> = ({ y = 0, color = c.paperLight }) => (
  <div style={{ position: 'absolute', left: 0, right: 0, top: y, height: 220, pointerEvents: 'none' }}>
    <div style={{ position: 'absolute', inset: 0, height: 150, background: c.sky }} />
    <div
      style={{
        position: 'absolute', inset: 0,
        background: `radial-gradient(ellipse 110px 70px at 110px 150px, ${color} 98%, transparent 100%) 0 0 / 220px 220px repeat-x,
                     radial-gradient(ellipse 95px 62px at 0 175px, ${color} 98%, transparent 100%) 0 0 / 220px 220px repeat-x`,
        filter: `drop-shadow(0 5px 0 ${c.edge})`,
      }}
    />
  </div>
);

/** Little caption strip, bottom centre (for silent autoplay). */
export const CaptionStrip: React.FC<{ text: string; progress: number }> = ({ text, progress }) => (
  <div
    style={{
      position: 'absolute', left: '50%', bottom: 46, translate: '-50% 0', maxWidth: 1500,
      background: 'rgba(28,22,16,0.86)', color: c.paperLight, padding: '12px 26px',
      fontFamily: body, fontSize: 36, lineHeight: 1.25, textAlign: 'center',
      opacity: progress, borderTop: `4px solid ${c.pink}`,
    }}
  >
    {text}
  </div>
);

/** One hand-cut paper cloud: a flat-bottomed cluster of circles with an ink-brown edge and a paper shadow. */
const CloudShape: React.FC<{ seed: string; w: number; fill: string; opacity?: number }> = ({ seed, w, fill, opacity = 1 }) => {
  const h = w * 0.5;
  const bumps = 4 + Math.floor(random(`${seed}n`) * 3);
  const circles = Array.from({ length: bumps }, (_, i) => {
    const t = (i + 0.5) / bumps;
    const r = h * (0.2 + 0.14 * Math.sin(t * Math.PI) + random(`${seed}r${i}`) * 0.04);
    return { cx: w * (0.12 + 0.76 * t), cy: h * 0.72 - r * 0.9 + (random(`${seed}y${i}`) - 0.5) * h * 0.06, r };
  });
  const id = `cl-${seed}`;
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} style={{ overflow: 'visible', opacity }}>
      <defs>
        <clipPath id={id}><rect x={-10} y={-h} width={w + 20} height={h * 1.72} /></clipPath>
      </defs>
      <g clipPath={`url(#${id})`}>
        <g transform="translate(6 8)" fill="rgba(43,29,16,0.16)">
          {circles.map((k, i) => <circle key={i} cx={k.cx} cy={k.cy} r={k.r} />)}
        </g>
        <g fill={fill} stroke={c.ink} strokeWidth={2.5} strokeOpacity={0.55}>
          {circles.map((k, i) => <circle key={i} cx={k.cx} cy={k.cy} r={k.r} />)}
        </g>
        <g fill={fill}>
          {circles.map((k, i) => <circle key={i} cx={k.cx} cy={k.cy} r={k.r - 1.5} />)}
        </g>
      </g>
      <line x1={w * 0.06} x2={w * 0.94} y1={h * 0.72} y2={h * 0.72} stroke={c.ink} strokeWidth={2.5} strokeOpacity={0.55} />
      <path d={`M${w * 0.3} ${h * 0.6} q${w * 0.07} ${h * 0.07} ${w * 0.14} 0 M${w * 0.55} ${h * 0.62} q${w * 0.07} ${h * 0.07} ${w * 0.14} 0`}
        fill="none" stroke={c.ink} strokeOpacity={0.28} strokeWidth={2} strokeLinecap="round" />
    </svg>
  );
};

/** Little flapping bird, a hand-drawn V. */
const Bird: React.FC<{ size: number; flap: number }> = ({ size, flap }) => (
  <svg width={size} height={size * 0.5} viewBox="0 0 40 20" style={{ overflow: 'visible' }}>
    <path d={`M2 ${10 - flap * 7} Q11 ${2 + flap * 4} 20 10 Q29 ${2 + flap * 4} 38 ${10 - flap * 7}`}
      fill="none" stroke={c.ink} strokeOpacity={0.7} strokeWidth={2.6} strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);

/**
 * A richer sky for the cold open: a graded dawn (deep teal fading to warm cream at the horizon),
 * paper clouds in three depths that drift at different speeds, a few twinkling stars and a flock of
 * birds. All deterministic per frame (no render-time randomness) and cheap (no blurs).
 */
export const Sky: React.FC<{ horizon?: number }> = ({ horizon = 760 }) => {
  const frame = useCurrentFrame();
  const layers = [
    { key: 'far', n: 6, w: [150, 230], y: [150, 330], speed: 0.18, fill: '#e3efe6', op: 0.8 },
    { key: 'mid', n: 5, w: [230, 340], y: [90, 400], speed: 0.4, fill: '#f1ece0', op: 0.95 },
    { key: 'near', n: 3, w: [380, 520], y: [40, 220], speed: 0.8, fill: c.paperLight, op: 1 },
  ];
  const stars = Array.from({ length: 14 }, (_, i) => ({
    x: random(`sx${i}`) * W, y: 30 + random(`sy${i}`) * 300, s: 10 + random(`ss${i}`) * 14, ph: random(`sp${i}`) * 6,
  }));
  return (
    <div style={{ position: 'absolute', left: 0, top: 0, width: W, height: horizon, pointerEvents: 'none', overflow: 'hidden' }}>
      <div style={{
        position: 'absolute', inset: 0,
        background: `linear-gradient(180deg, #4fb0a4 0%, #7cc7b8 22%, ${c.sky} 45%, #d3e3c9 70%, rgba(239,228,200,0) 100%)`,
      }} />
      {/* warm glow where the sun sits */}
      <div style={{
        position: 'absolute', right: -80, top: -120, width: 900, height: 900,
        background: 'radial-gradient(circle, rgba(255,236,170,0.75) 0%, rgba(255,236,170,0.28) 38%, rgba(255,236,170,0) 68%)',
      }} />
      {stars.map((st, i) => {
        const tw = 0.35 + 0.65 * Math.abs(Math.sin(frame / 18 + st.ph));
        return (
          <svg key={i} width={st.s} height={st.s} viewBox="-10 -10 20 20" style={{ position: 'absolute', left: st.x, top: st.y, opacity: tw * 0.8 }}>
            <path d="M0 -9 L2 -2 L9 0 L2 2 L0 9 L-2 2 L-9 0 L-2 -2 Z" fill="#fff6d0" />
          </svg>
        );
      })}
      {layers.map((L) =>
        Array.from({ length: L.n }, (_, i) => {
          const w = L.w[0] + random(`${L.key}w${i}`) * (L.w[1] - L.w[0]);
          const span = W + w + 200;
          const x = ((random(`${L.key}x${i}`) * span + frame * L.speed) % span) - w - 100;
          const y = L.y[0] + random(`${L.key}y${i}`) * (L.y[1] - L.y[0]) + Math.sin(frame / 60 + i) * 4;
          return (
            <div key={`${L.key}${i}`} style={{ position: 'absolute', left: x, top: y }}>
              <CloudShape seed={`${L.key}${i}`} w={w} fill={L.fill} opacity={L.op} />
            </div>
          );
        }),
      )}
      {Array.from({ length: 5 }, (_, i) => {
        const span = W + 300;
        const x = ((random(`bx${i}`) * span + frame * (1.6 + i * 0.15)) % span) - 150;
        const y = 120 + random(`by${i}`) * 220 + Math.sin(frame / 22 + i * 2) * 10;
        const flap = Math.sin(frame / 3.2 + i * 1.7);
        return <div key={i} style={{ position: 'absolute', left: x, top: y }}><Bird size={26 + i * 4} flap={flap} /></div>;
      })}
    </div>
  );
};
