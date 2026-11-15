import { Composition, staticFile, type CalculateMetadataFunction } from 'remotion';
import { EpisodeVideo } from './Episode';
import { episodeBySlug, episodes } from './episodes';
import { FPS, H, W, buildTimeline, estimateDur } from './timeline';
import type { Boxes, EpisodeProps, Timing } from './types';

/** Fetches a file from public/ as JSON, or null when it is not there (yet). */
const optionalJson = async <T,>(path: string, signal: AbortSignal): Promise<T | null> => {
  try {
    const res = await fetch(staticFile(path), { signal });
    if (!res.ok) return null;
    return (await res.json()) as T;
  } catch {
    return null;
  }
};
const exists = async (path: string, signal: AbortSignal) => {
  try {
    return (await fetch(staticFile(path), { method: 'HEAD', signal })).ok;
  } catch {
    return false;
  }
};

/**
 * Everything that depends on generated files is read here, before rendering: narration lengths
 * (which set the duration), the shots' boxes, and whether the shared music exists.
 */
const calculateMetadata: CalculateMetadataFunction<EpisodeProps> = async ({ props, abortSignal }) => {
  const ep = episodeBySlug(props.slug);
  const dir = `episodes/${ep.slug}`;
  const timing = await optionalJson<Timing>(`${dir}/timing.json`, abortSignal);
  const names = new Map<string, 'shots' | 'clips'>();
  for (const sc of ep.scenes) {
    if (sc.shot) names.set(sc.shot, 'shots');
    if (sc.clip) names.set(sc.clip, 'clips');
    for (const b of sc.beats ?? []) {
      if (b.shot) names.set(b.shot, 'shots');
      if (b.clip) names.set(b.clip, 'clips');
    }
  }
  const boxes: Boxes = {};
  await Promise.all([...names].map(async ([name, kind]) => {
    const file = await optionalJson<{ boxes?: Boxes[string] }>(`${dir}/${kind}/${name}.json`, abortSignal);
    if (file?.boxes) boxes[name] = file.boxes;
  }));
  const [music, sting] = await Promise.all([exists('audio/music-bed.mp3', abortSignal), exists('audio/sting.mp3', abortSignal)]);
  const tl = buildTimeline(ep, timing, boxes);
  return { durationInFrames: tl.total, props: { ...props, timing, boxes, music, sting }, defaultOutName: ep.slug };
};

export const RemotionRoot: React.FC = () => (
  <>
    {episodes.map((ep) => (
      <Composition
        key={ep.slug}
        id={`learn-${ep.slug}`}
        component={EpisodeVideo}
        // Replaced by calculateMetadata; this is the estimate used until timing.json exists.
        durationInFrames={Math.ceil(ep.scenes.flatMap((s) => s.lines ?? []).reduce((n, l) => n + estimateDur(l.text) + 1, 0) * FPS)}
        fps={FPS}
        width={W}
        height={H}
        defaultProps={{ slug: ep.slug, timing: null, boxes: {}, music: false, sting: false, captions: true }}
        calculateMetadata={calculateMetadata}
      />
    ))}
  </>
);
