/** episode.json, see ../README.md. */
export interface Rect { x: number; y: number; w: number; h: number }
/** A box id from the current shot's json, or a rectangle in 1920×1080 CSS px. */
export type BoxRef = string | Rect;

export interface Line { id: string; text: string; say?: string }

export interface Beat {
  line?: string;            // narration line this beat hangs on (default: the scene's first line)
  at?: number;              // seconds after that line starts (default 0)
  shot?: string;            // cut to another shot
  clip?: string;            // cut to a clip
  zoom?: BoxRef | null;     // null zooms back out
  highlight?: BoxRef | null;// null removes the ring
  cursor?: BoxRef | null;   // null hides the cursor
  click?: boolean;
  label?: string;
}

export interface Scene {
  id: string;
  kind: 'title' | 'shot' | 'clip' | 'outro';
  heading?: string;
  shot?: string;
  clip?: string;
  lines?: Line[];
  beats?: Beat[];
}

export interface Episode {
  number: number;
  slug: string;
  title: string;
  summary?: string;
  next?: string;            // title of the next episode, shown on the outro
  dev?: boolean;            // engine test episode: only registered with REMOTION_LEARN_DEV=1
  scenes: Scene[];
}

/** public/episodes/<slug>/timing.json, written by scripts/timing.mjs. */
export interface Timing {
  lines: Record<string, { dur: number; placeholder?: boolean }>; // placeholder: estimated, no wav yet
  clips?: Record<string, { dur: number }>;
}

/** Boxes per shot (or clip) name. */
export type Boxes = Record<string, Record<string, Rect>>;

export type EpisodeProps = {
  slug: string;
  timing: Timing | null;    // null: no timing.json yet, durations are estimated and there is no voice
  boxes: Boxes;
  music: boolean;           // public/audio/music-bed.mp3 exists
  sting: boolean;           // public/audio/sting.mp3 exists
  captions: boolean;        // false for the poster still
};
