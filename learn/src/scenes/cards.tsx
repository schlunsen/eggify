// The title and outro sheets.
import { AbsoluteFill, Easing, interpolate, useCurrentFrame, useVideoConfig } from 'remotion';
import { c, body, display, displayItalic, mono, stripShadow } from '../theme';
import { Cutout, Kicker, Paper, useEnter } from '../components/paper';
import type { Episode } from '../types';
import type { Timeline } from '../timeline';

const clamp = { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' } as const;

/** Big Fraunces headline that shrinks to fit long titles on one or two lines. */
const headline = (text: string, max: number, width: number) => Math.round(Math.min(max, (width / Math.max(6, text.length)) * 1.7));

const Chapter: React.FC<{ n: number; text: string; delay: number }> = ({ n, text, delay }) => {
  const p = useEnter(delay, { damping: 12, stiffness: 150, mass: 0.8 });
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12, opacity: p, translate: `0 ${(1 - p) * 24}px` }}>
      <span style={{
        width: 44, height: 44, boxSizing: 'border-box', border: `3px solid ${c.ink}`, borderRadius: '50%', background: c.mustard,
        fontFamily: display, fontWeight: 900, fontSize: 24, lineHeight: '38px', textAlign: 'center', color: c.ink,
      }}>{n}</span>
      <span style={{ fontFamily: mono, fontWeight: 700, fontSize: 25, letterSpacing: '0.04em', color: c.ink }}>{text}</span>
    </div>
  );
};

export const Title: React.FC<{ ep: Episode; tl: Timeline }> = ({ ep, tl }) => {
  const { fps } = useVideoConfig();
  const mark = useEnter(8, { damping: 12, stiffness: 130, mass: 1 });
  const egg = useEnter(0.5 * fps);
  const sub = useEnter(0.9 * fps);
  return (
    <Paper bunting>
      <Kicker text="Eggify · Learn" delay={2} style={{ left: 150, top: 150 }} />
      <div style={{ position: 'absolute', left: 156, top: 262, fontFamily: displayItalic, fontWeight: 700, fontSize: 60, color: c.pinkInk, opacity: mark }}>
        Episode {ep.number}
      </div>
      <div style={{ position: 'absolute', left: 150, top: 330, width: 1180, scale: 0.7 + mark * 0.3, opacity: mark, transformOrigin: 'left center' }}>
        <div style={{ fontFamily: display, fontWeight: 900, fontSize: headline(ep.title, 210, 1180), letterSpacing: '-0.04em', lineHeight: 0.98, color: c.ink, textShadow: stripShadow() }}>
          {ep.title}
        </div>
      </div>
      {ep.summary ? (
        <div style={{ position: 'absolute', left: 156, top: 610, width: 1080, fontFamily: body, fontSize: 42, lineHeight: 1.25, color: c.ink, opacity: sub, translate: `0 ${(1 - sub) * 30}px` }}>
          {ep.summary}
        </div>
      ) : null}
      <div style={{ position: 'absolute', left: 156, top: 836, width: 1170, display: 'flex', flexWrap: 'wrap', gap: '14px 34px' }}>
        {tl.chapters.map((ch, i) => <Chapter key={ch.n} n={ch.n} text={ch.heading} delay={1.5 * fps + i * 5} />)}
      </div>
      <Cutout src="art/egg.webp" seed="egg-title" width={400} style={{ right: 150, top: 200 + (1 - egg) * 900 }} />
    </Paper>
  );
};

export const Outro: React.FC<{ ep: Episode; dur: number }> = ({ ep, dur }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  const mark = useEnter(8, { damping: 12, stiffness: 130, mass: 1 });
  const egg = useEnter(0.6 * fps, { damping: 9, stiffness: 140, mass: 0.9 });
  const url = useEnter(1.3 * fps);
  const head = ep.next ?? 'That was the series';
  return (
    <Paper bunting>
      <Kicker text={ep.next ? 'Next episode' : 'The end'} delay={2} style={{ left: 150, top: 170 }} />
      <div style={{ position: 'absolute', left: 150, top: 270, width: 1160, scale: 0.7 + mark * 0.3, opacity: mark, transformOrigin: 'left center' }}>
        <div style={{ fontFamily: display, fontWeight: 900, fontSize: headline(head, 150, 1160 * 1.6), letterSpacing: '-0.035em', lineHeight: 1.02, color: c.ink, textShadow: stripShadow(0.03) }}>
          {head}
        </div>
      </div>
      <div style={{ position: 'absolute', left: 156, top: 690, display: 'flex', alignItems: 'center', gap: 30, opacity: url, translate: `0 ${(1 - url) * 40}px` }}>
        <span style={{ fontFamily: body, fontSize: 44, color: c.ink }}>All episodes at</span>
        <span style={{
          fontFamily: mono, fontWeight: 700, fontSize: 50, letterSpacing: '0.05em', color: c.ink, background: c.mustard,
          border: `4px solid ${c.ink}`, padding: '12px 34px', boxShadow: '8px 10px 0 rgba(43,29,16,0.22)', rotate: '-2deg',
        }}>eggify.xyz/learn</span>
      </div>
      <div style={{ position: 'absolute', left: 156, top: 850, fontFamily: mono, fontSize: 26, letterSpacing: '0.1em', textTransform: 'uppercase', color: c.inkSoft, opacity: url }}>
        That was № {ep.number} · {ep.title}
      </div>
      <Cutout src="art/cherub.webp" seed="cherub-outro" width={210} style={{ right: 420, top: 96 + Math.sin(frame / 10) * 10 }} />
      <Cutout src="art/egg.webp" seed="egg-outro" width={370} style={{ right: 140, top: 270 + (1 - egg) * 900 }} />
      <AbsoluteFill style={{ background: c.umber, opacity: interpolate(frame, [dur - 0.7 * fps, dur - 2], [0, 1], { ...clamp, easing: Easing.in(Easing.quad) }) }} />
    </Paper>
  );
};
