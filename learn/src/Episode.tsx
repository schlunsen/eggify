import { useMemo } from 'react';
import { AbsoluteFill, Audio, Sequence, interpolate, staticFile, useCurrentFrame, useVideoConfig } from 'remotion';
import { CaptionStrip } from './components/paper';
import { Stage } from './components/stage';
import { Outro, Title } from './scenes/cards';
import { episodeBySlug } from './episodes';
import { OVERLAP, buildTimeline, type Timeline } from './timeline';
import type { EpisodeProps } from './types';
import { c } from './theme';

const clamp = { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' } as const;

/** Incoming sheet slides up from below with a slight tilt, as in the film. */
const SlideIn: React.FC<{ children: React.ReactNode; first: boolean }> = ({ children, first }) => {
  const frame = useCurrentFrame();
  if (first || frame >= OVERLAP) return <AbsoluteFill>{children}</AbsoluteFill>;
  const eased = 1 - Math.pow(1 - frame / OVERLAP, 3);
  return (
    <AbsoluteFill style={{ translate: `0 ${(1 - eased) * 1100}px`, rotate: `${(1 - eased) * 3}deg`, boxShadow: '0 -16px 0 rgba(43,29,16,0.18)' }}>
      {children}
    </AbsoluteFill>
  );
};

const Caption: React.FC<{ text: string; dur: number }> = ({ text, dur }) => {
  const f = useCurrentFrame();
  return <CaptionStrip text={text} progress={interpolate(f, [0, 5, dur - 5, dur], [0, 1, 1, 0], clamp)} />;
};

/** Music bed: quiet, ducked further while the narrator speaks, faded at both ends. */
const musicVolume = (tl: Timeline, fps: number) => (f: number) => {
  const BASE = 0.15;
  const DUCK = 0.055;
  const pre = 0.3 * fps;
  const post = 0.5 * fps;
  let v = BASE;
  for (const sc of tl.scenes) {
    for (const l of sc.lines) {
      if (f < l.from - pre || f > l.from + l.dur + post) continue;
      const inside = interpolate(f, [l.from - pre, l.from, l.from + l.dur, l.from + l.dur + post], [0, 1, 1, 0], clamp);
      v = Math.min(v, BASE - (BASE - DUCK) * inside);
    }
  }
  return v * interpolate(f, [0, 1.2 * fps], [0, 1], clamp) * interpolate(f, [tl.total - 2.5 * fps, tl.total - 4], [1, 0], clamp);
};

/** The title sting plays out in the open, then drops under the narrator's first line. */
const STING_AT = 4;
const stingVolume = (tl: Timeline, fps: number) => (f: number) => {
  const first = tl.scenes[0]?.lines[0]?.from ?? Infinity;
  return interpolate(f + STING_AT, [first - 0.35 * fps, first], [0.5, 0.14], clamp);
};

export const EpisodeVideo: React.FC<EpisodeProps> = ({ slug, timing, boxes, music, sting, captions }) => {
  const { fps } = useVideoConfig();
  const ep = episodeBySlug(slug);
  const tl = useMemo(() => buildTimeline(ep, timing, boxes), [ep, timing, boxes]);
  return (
    <AbsoluteFill style={{ background: c.umber }}>
      {tl.blocks.map((b, i) => {
        const from = Math.max(0, b.from - (i ? OVERLAP : 0));
        return (
          <Sequence key={b.from} from={from} durationInFrames={b.from + b.dur - from} name={b.kind === 'stage' ? 'stage' : b.scene.id}>
            <SlideIn first={i === 0}>
              {b.kind === 'title' ? <Title ep={ep} tl={tl} /> : b.kind === 'outro' ? <Outro ep={ep} dur={b.from + b.dur - from} /> : (
                <Stage block={b} tl={tl} slug={slug} number={ep.number} title={ep.title} offset={from} />
              )}
            </SlideIn>
          </Sequence>
        );
      })}

      {captions ? tl.captions.map((cap) => (
        <Sequence key={cap.from} from={cap.from} durationInFrames={cap.dur} name="caption" layout="none">
          <Caption text={cap.text} dur={cap.dur} />
        </Sequence>
      )) : null}

      {tl.scenes.flatMap((sc) => sc.lines).filter((l) => !l.silent).map((l) => (
        <Sequence key={l.id} from={l.from} durationInFrames={l.dur + Math.round(0.3 * fps)} name={`vo ${l.id}`} layout="none">
          <Audio src={staticFile(`episodes/${slug}/vo/${l.id}.wav`)} />
        </Sequence>
      ))}

      {music ? <Audio src={staticFile('audio/music-bed.mp3')} loop volume={musicVolume(tl, fps)} /> : null}
      {sting ? (
        <Sequence from={STING_AT} durationInFrames={Math.min(tl.total - STING_AT, 6 * fps)} name="sting" layout="none">
          <Audio src={staticFile('audio/sting.mp3')} volume={stingVolume(tl, fps)} />
        </Sequence>
      ) : null}
    </AbsoluteFill>
  );
};
