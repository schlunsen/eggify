// The film's paper world (ported from promo/src/components/paper.tsx), with a quieter backdrop
// for the tutorial stage: the screen is the subject, so nothing behind it competes.
import { AbsoluteFill, Img, random, spring, staticFile, useCurrentFrame, useVideoConfig } from 'remotion';
import type { CSSProperties, ReactNode } from 'react';
import { c, mono, body } from '../theme';

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
const CLOUDS = [
  { y: 150, s: 0.9, v: 9, o: 0.55, x0: 100 },
  { y: 300, s: 0.7, v: 6, o: 0.45, x0: 900 },
  { y: 90, s: 1.2, v: 13, o: 0.7, x0: 1500 },
  { y: 520, s: 0.6, v: 5, o: 0.4, x0: 400 },
  { y: 640, s: 0.8, v: 16, o: 0.5, x0: 1300 },
];

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

/** Layered paper diorama: sun, drifting clouds, cut-out hills; bunting on the title and outro only. */
const Backdrop: React.FC<{ bunting: boolean; quiet: boolean }> = ({ bunting, quiet }) => {
  const frame = useCurrentFrame();
  const jFar = useJitter('bg-far', 0.8, 6);
  const jMid = useJitter('bg-mid', 0.9, 5);
  const jNear = useJitter('bg-near', 1, 4);
  const jit = { far: jFar, mid: jMid, near: jNear };
  const flagColors = [c.pink, c.mustard, c.teal];
  return (
    <AbsoluteFill style={{ pointerEvents: 'none' }}>
      <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`} style={{ position: 'absolute', inset: 0 }}>
        {quiet ? null : (
          <g>
            <circle cx={1646} cy={158} r={92} fill="rgba(43,29,16,0.12)" />
            <circle cx={1640} cy={150} r={90} fill="#ecc45a" opacity={0.55} />
            <circle cx={1640} cy={150} r={64} fill="#f3d57c" opacity={0.6} />
          </g>
        )}
        {HILLS.map((h) => {
          const j = jit[h.id as keyof typeof jit];
          const k = quiet ? 0.5 : 1;
          return (
            <g key={h.id} style={{ translate: `${j.x * h.speed * k}px ${j.y * h.speed * k}px` }}>
              <path d={h.d} fill="rgba(43,29,16,0.16)" transform="translate(7,-9)" />
              <path d={h.d} fill={h.fill} stroke="rgba(43,29,16,0.22)" strokeWidth={2} />
            </g>
          );
        })}
        {bunting ? (
          <g opacity={0.6}>
            <path d={`M0,10 Q480,${70 + Math.sin(frame / 40) * 4} 960,14 T1920,10`} stroke={c.inkSoft} strokeWidth={2.5} fill="none" />
            {Array.from({ length: 17 }, (_, i) => {
              const x = 40 + i * 112;
              const y = 14 + Math.sin((x / 960) * Math.PI) * 24;
              const sw = Math.sin(frame / 14 + i) * 3;
              return <path key={i} d={`M${x - 24},${y + 4} L${x + 24},${y + 4} L${x + sw},${y + 56} Z`} fill={flagColors[i % 3]} stroke="rgba(43,29,16,0.35)" strokeWidth={1.5} />;
            })}
          </g>
        ) : null}
        {CLOUDS.map((cl, i) => {
          const span = W + 420;
          const x = ((cl.x0 + frame * cl.v * (quiet ? 0.25 : 0.5)) % span) - 260;
          const bob = Math.round(Math.sin(frame / 30 + i) * 3);
          return (
            <g key={i} transform={`translate(${x.toFixed(1)},${cl.y + bob}) scale(${cl.s * 1.6})`} opacity={cl.o * (quiet ? 0.8 : 1)}>
              <path d={CLOUD_BLOBS} fill="rgba(43,29,16,0.14)" transform="translate(5,6)" />
              <path d={CLOUD_BLOBS} fill="#f8f1de" stroke="rgba(43,29,16,0.22)" strokeWidth={1.5} />
            </g>
          );
        })}
      </svg>
      <AbsoluteFill style={{ background: 'radial-gradient(ellipse 75% 70% at 50% 48%, transparent 55%, rgba(92,70,48,0.2) 100%)' }} />
    </AbsoluteFill>
  );
};

/** Aged paper with grain and engraving hatch, and the paper diorama behind the scene. */
export const Paper: React.FC<{ bunting?: boolean; quiet?: boolean; children?: ReactNode }> = ({ bunting = false, quiet = false, children }) => (
  <AbsoluteFill
    style={{
      backgroundColor: c.paper,
      backgroundImage: `${grain}, repeating-linear-gradient(135deg, rgba(43,29,16,0.035) 0 2px, transparent 2px 12px), repeating-linear-gradient(52deg, rgba(120,90,50,0.025) 0 1px, transparent 1px 7px)`,
    }}
  >
    <Backdrop bunting={bunting} quiet={quiet} />
    {children}
  </AbsoluteFill>
);

/** A paper cut-out image with jitter and a hard paper shadow. */
export const Cutout: React.FC<{ src: string; width: number; seed: string; style?: CSSProperties; jitter?: number }> = ({
  src, width, seed, style, jitter = 1,
}) => {
  const j = useJitter(seed, jitter);
  return (
    <Img
      src={staticFile(src)}
      style={{
        position: 'absolute', width, filter: 'drop-shadow(8px 10px 0 rgba(43,29,16,0.2))',
        translate: `${j.x}px ${j.y}px`, rotate: `${j.r}deg`, ...style,
      }}
    />
  );
};

/** Small paper label in typewriter capitals. */
export const Kicker: React.FC<{ text: string; delay?: number; style?: CSSProperties }> = ({ text, delay = 0, style }) => {
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

/** Caption strip, bottom centre, in the film's style. */
export const CaptionStrip: React.FC<{ text: string; progress: number }> = ({ text, progress }) => (
  <div
    style={{
      position: 'absolute', left: '50%', bottom: 26, translate: '-50% 0', maxWidth: 1560, width: 'max-content',
      background: 'rgba(28,22,16,0.9)', color: c.paperLight, padding: '11px 28px 12px',
      fontFamily: body, fontSize: 37, lineHeight: 1.22, textAlign: 'center',
      opacity: progress, borderTop: `4px solid ${c.pink}`,
    }}
  >
    {text}
  </div>
);
