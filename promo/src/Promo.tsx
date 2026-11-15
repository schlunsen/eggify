import { AbsoluteFill, Audio, Sequence, interpolate, staticFile, useCurrentFrame } from 'remotion';
import { CaptionStrip } from './components/paper';
import { Binary, Cage, End, Intro, Models, Open, Sessions, Watch, ZenPhone } from './scenes/scenes';
import { FPS, TOTAL_FRAMES, scenes, sec, vo } from './timeline';

const components: Record<string, React.FC> = {
  open: Open, intro: Intro, sessions: Sessions, watch: Watch, models: Models, zen: ZenPhone, cage: Cage, binary: Binary, end: End,
};
const OVERLAP = sec(0.4); // the next scene slides in over the previous one, like a sheet of paper

/** Incoming scene slides up from below with a slight tilt. */
const SlideIn: React.FC<{ children: React.ReactNode; first: boolean }> = ({ children, first }) => {
  const frame = useCurrentFrame();
  if (first) return <AbsoluteFill>{children}</AbsoluteFill>;
  const p = interpolate(frame, [0, OVERLAP], [0, 1], { extrapolateRight: 'clamp' });
  const eased = 1 - Math.pow(1 - p, 3);
  return (
    <AbsoluteFill style={{ translate: `0 ${(1 - eased) * 1100}px`, rotate: `${(1 - eased) * 4}deg`, boxShadow: '0 -16px 0 rgba(43,29,16,0.18)' }}>
      {children}
    </AbsoluteFill>
  );
};

/** Music volume: quiet, ducked further while the narrator speaks. */
const musicVolume = (f: number) => {
  const t = f / FPS;
  const BASE = 0.2;
  const DUCK = 0.075;
  let v = BASE;
  for (const line of vo) {
    const inside = interpolate(t, [line.at - 0.3, line.at, line.at + line.dur, line.at + line.dur + 0.5], [0, 1, 1, 0], {
      extrapolateLeft: 'clamp', extrapolateRight: 'clamp',
    });
    v = Math.min(v, BASE - (BASE - DUCK) * inside);
  }
  const fadeIn = interpolate(t, [0, 1.2], [0, 1], { extrapolateRight: 'clamp' });
  const fadeOut = interpolate(t, [60, 64], [1, 0], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' });
  return v * fadeIn * fadeOut;
};

export const Promo: React.FC = () => (
  <AbsoluteFill style={{ background: '#1c1610' }}>
    {scenes.map((sc, i) => {
      const Scene = components[sc.id];
      const from = Math.max(0, sec(sc.start) - (i ? OVERLAP : 0));
      return (
        <Sequence key={sc.id} from={from} durationInFrames={sec(sc.end) - from} name={sc.id}>
          <SlideIn first={i === 0}><Scene /></SlideIn>
        </Sequence>
      );
    })}

    {vo.map((line, i) => (
      <Sequence key={line.file} from={sec(line.at)} durationInFrames={sec(line.dur + 0.6)} name={`vo-${i + 1}`}>
        <Audio src={staticFile(line.file)} />
        <Caption text={line.text} dur={sec(line.dur + 0.6)} />
      </Sequence>
    ))}

    <Audio src={staticFile('audio/music.mp3')} volume={musicVolume} />
    {sfx.map((x) => (
      <Sequence key={x.name} from={sec(x.at)} durationInFrames={sec(1.5)} name={x.name}>
        <Audio src={staticFile(x.file)} volume={x.volume} />
      </Sequence>
    ))}
  </AbsoluteFill>
);

const sfx = [
  // The foot-hit clip (a Voice Memo) has its transient 0.1 s after the start; the foot lands at 4.35 s.
  { name: 'foot-hit-open', file: 'audio/foot-hit.wav', at: 4.35 - 0.1, volume: 1 },
  { name: 'thud-binary', file: 'audio/thud.wav', at: 50.8 + 5.4 - 0.02, volume: 1 },
];

const Caption: React.FC<{ text: string; dur: number }> = ({ text, dur }) => {
  const f = useCurrentFrame();
  const p = interpolate(f, [0, 6, dur - 8, dur], [0, 1, 1, 0], { extrapolateRight: 'clamp' });
  return <CaptionStrip text={text} progress={p} />;
};

export const totalFrames = TOTAL_FRAMES;
