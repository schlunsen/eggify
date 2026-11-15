# Eggify learning videos

Short narrated tutorials (1920×1080, 30 fps, 60–120 s) in the Flying Circus paper style of the
promo film (`../promo`), shown on https://eggify.xyz/learn. Made with Remotion, a local OmniVoice
clone of the film's narrator, and real Eggify screens captured from a throwaway demo server.

## Layout (the contract between the parts)

```
learn/
  episodes/<nn>-<slug>/episode.json     the single source of an episode: scenes, narration, beats
  episodes/<nn>-<slug>/capture.mjs      Playwright script that takes this episode's shots
  public/episodes/<slug>/shots/<name>.png   3840×2160 (1920×1080 viewport, device scale 2)
  public/episodes/<slug>/shots/<name>.json  {"boxes": {"<boxId>": {x, y, w, h}}} in 1920×1080 CSS px
  public/episodes/<slug>/clips/<name>.mp4   optional screen recordings
  public/episodes/<slug>/vo/<lineId>.wav    narration, one file per line
  public/episodes/<slug>/timing.json        {"lines": {"<lineId>": {"dur": seconds}}}, from the wavs (scripts/timing.mjs)
  public/audio/                             music bed, sting (shared by all episodes)
  public/art/                               cut-outs shared with the film
  src/                                      the Remotion engine: one composition per episode, id `learn-<slug>`
  tts/                                      OmniVoice voice clone (uv project): episode.json → vo/*.wav
  capture/                                  demo server (temporary HOME, seeded demo data) + Playwright helpers
  scripts/                                  with-lock.sh, timing, build and deploy
```

## episode.json

```jsonc
{
  "number": 1, "slug": "quick-start", "title": "Quick start", "summary": "…",
  "scenes": [{
    "id": "agents",
    "kind": "title | shot | clip | outro",
    "heading": "Connect your agents",          // chapter label shown in the corner
    "shot": "runtimes",                        // first shot of the scene (kind shot)
    "clip": "streaming",                       // kind clip
    "lines": [{ "id": "g1", "text": "caption text", "say": "optional spelling for the voice" }],
    "beats": [{                                 // what happens on screen, tied to a narration line
      "line": "g2", "at": 0.4,                  // seconds after that line starts (default 0)
      "shot": "runtimes-login",                 // cut to another shot
      "zoom": "claudeCard",                     // box id from the shot's json, or {x, y, w, h}; null zooms out
      "highlight": "loginButton",               // hand-drawn ring around a box
      "cursor": "loginButton", "click": true,   // move the cursor there (and click)
      "label": "Runs the tool's own login"      // paper label next to the highlight
    }]
  }]
}
```

Line ids are unique within an episode. A scene lasts as long as its lines (plus short gaps), so
timing follows the audio and nothing is timed by hand. Captions are the lines' `text`.

More that the engine reads (all optional):

- Episode: `"next": "Projects, sessions and tags"` is the next episode's title on the outro;
  `"dev": true` marks an engine test episode (`episodes/00-demo`), registered only with
  `REMOTION_LEARN_DEV=1`.
- Beats: `line` defaults to the scene's first line. `"clip": "name"` cuts to a clip the way `shot`
  cuts to a shot. `zoom`, `highlight` and `cursor` take a box id of the shot on screen or a
  rectangle `{x, y, w, h}` in 1920×1080 px; `null` zooms out, removes the ring, hides the cursor.
  A ring and its label stay until the next beat with `highlight`/`label`, a cut, or the end of
  the scene. `label` without `highlight` is placed by the beat's `cursor` or `zoom` box, else at
  the top of the screen. The camera never zooms past 2.4× (shots are 2× and shown at 0.8).
- A clip can have boxes too: `clips/<name>.json`, same format as a shot's.
- `timing.json` also holds `"clips": {"<name>": {"dur": seconds}}` (a clip scene lasts at least as
  long as its clip; a shorter clip holds its last frame), and a line may be
  `{"dur": …, "placeholder": true}`: estimated, no wav yet, silent in the video.
- `public/audio/music-bed.mp3` (looped, ducked under the voice) and `public/audio/sting.mp3` (on
  the title) are used when they exist.

## The engine (`src/`)

`timeline.ts` turns episode.json + timing.json + the shots' boxes into frames (scenes, captions,
camera moves, rings, cursor); `Root.tsx` registers `learn-<slug>` per episode and reads the
generated files in `calculateMetadata`; `components/stage.tsx` is the paper-framed screen,
`scenes/cards.tsx` the title and outro, `components/paper.tsx` the film's paper world. A scene is
its lines with 0.35 s between them, 0.6 s before the first and after the last (title 1.1 s before,
at least 4.5 s; outro at least 6 s and 1.6 s after its last line). One narration line becomes one
or more one-line captions (split at sentence ends, then at a comma near the middle).

```bash
npm install                                   # once
npx remotion studio                           # preview (REMOTION_LEARN_DEV=1 adds learn-demo)
node scripts/timing.mjs <slug>                # vo/*.wav → timing.json; a line without a wav is an error
node scripts/dump-timeline.mjs <slug>         # the episode in frames, to pick frames for stills
npx remotion still learn-<slug> out/f.png --frame=600
scripts/build-episode.sh <slug>               # timing → out/<slug>.mp4 → out/<slug>-web.mp4 → out/<slug>-poster.jpg
```

Before the real assets exist: `node scripts/placeholders.mjs <slug>` writes a grey mock screen
(and three demo boxes: `sidebar`, `card`, `button`) for every missing shot and gives lines without
a wav an estimated length in timing.json; `--list` shows what is still a placeholder, `--clean`
removes it. It never overwrites a file and never writes a wav (the voice script skips lines that
already have one). `PLACEHOLDERS=1 scripts/build-episode.sh <slug>` builds with them.
`episodes/00-demo/setup.sh` prepares the engine test episode (every kind of beat, on screens
borrowed from the film).

The Remotion agent skills (`npx skills add remotion-dev/skills`) are pinned in `skills-lock.json`;
`.claude/` is ignored by git, so restore them with `npx skills experimental_install`.

Heavy jobs take a lock so parallel work does not fight over the machine:
`scripts/with-lock.sh tts …` for voice generation, `scripts/with-lock.sh render …` for renders.

Screens are always captured from demo data on a local throwaway server, never from a real
install (see `../docs/MEDIA_PROVENANCE.md`).
