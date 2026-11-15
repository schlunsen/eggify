# Producing an episode

How one learning video is made, end to end, by one person or agent. Read `README.md` (layout,
`episode.json`, beats, commands) and `capture/README.md` (demo server, `shot()`, boxes, stubbing)
first. Episode 1 (`episodes/01-quick-start/`) is the model for style and format.

## What you own

Only `episodes/<nn>-<slug>/`, `public/episodes/<slug>/` and `out/<slug>*`. Do not edit the engine
(`src/`), `scripts/`, `tts/`, `capture/*.mjs|sh`, `capture/library/`, other episodes or app code;
work around a limit and report it. Do not commit, deploy or upload.

## 1. Script (`episode.json`)

- 75–100 s, 5–7 chapters (`shot` scenes with a short `heading`), a `title` scene and an `outro`;
  set `number`, `slug`, `title`, `summary` (one plain sentence) and `next` (the next episode's title).
- Voice: the film's narrator reading calm tutorial copy. Short sentences, second person, says what
  to press and what you then see. Exactly one dry Flying-Circus-style joke per episode, not a reused
  one (episode 1: "Do try to keep up."). No marketing words.
- Every sentence must be true of the screen it plays over: use the app's real labels (button and
  group names as shown), and name the desktop-app keys (a browser tab adds ⌥ to some; do not zoom
  on a key label that shows the browser variant). `CLAUDE.md` at the repository root documents
  every feature; the screens are the final word.
- Captions are each line's `text`. Use `say` only to fix pronunciation: keys ("⌘S" → "Command S"),
  abbreviations ("API" → "A P I"), "eggify.xyz" → "eggify dot x y z".
- Lines of 5–8 s at most; one idea per line. Line ids are unique in the episode.

## 2. Screens (`capture.mjs`)

- Look first at the library made for the episode: `public/library/shots/<area>/*.png`, described in
  `capture/library/index/<area>.json` (what each shows, boxes, seeded or stubbed), produced by
  `capture/library/areas/<area>.mjs` on the richer demo world in `capture/library/` (`server.sh`,
  `seed-extra.mjs`, `world.mjs`, `lib.mjs`). View the PNGs before writing the script.
- Write `episodes/<nn>-<slug>/capture.mjs` to shoot exactly the states the script needs into
  `public/episodes/<slug>/shots/` (3840×2160 PNG + `{"boxes": {...}}` JSON), reusing the library's
  world and helpers. Port: 3400 + episode number. A library PNG that is already exactly right may be
  copied with its boxes converted to the episode format; say which.
- Give a box to everything a beat points at. Fictional demo data only. Stubbing an API or WebSocket
  payload that the real UI then draws is fine and is noted in the script's header; never inject HTML
  or edit pixels. No live agent turns, no real logins.
- Paths on screen read like a real Mac's: `~/projects/<name>` wherever the app shortens them, or
  `/Users/Shared/praline/projects/<name>` where it shows them in full (the demo servers' home).
  Never a temporary folder (`/tmp`, `/private/tmp`, a port number in a path). In examples you type
  or say, use `~/projects/<name>`. Prefer pointing rings and zooms at the `~` form.
- Look at every shot and its proof sheet (boxes drawn). Re-shoot anything with real data
  (`/Users/…`, real names, e-mails, keys, URLs), spinners, toasts, error states or misplaced boxes.
  Stop every server you started.

## 3. Beats

- Zoom to a 16:9 rectangle `{x, y, w, h}` around what is being discussed; a tall dialog or page
  barely zooms when you zoom to its box. Text on screen must be readable: zoom in rather than show
  a whole 1920-wide page (useful maximum about 2.4×).
- A ring (`highlight`) on the thing being named; cursor + click on what the viewer should press,
  then cut (`shot`) to the screen that follows.
- Paper labels sit just below the ring and usually cover the next row: use one only over empty
  space, otherwise let the caption carry it.
- Keep each beat's `at` inside its line (check `timing.json` once the voice exists).

## 4. Voice

```bash
cd learn/tts && ../scripts/with-lock.sh tts uv run python gen.py ../episodes/<nn>-<slug>
```

One line takes 2–3 minutes and the GPU lock is shared, so other episodes may be ahead of you.
Read the per-line report (transcript check, pace); fix misreadings with `say` and redo only those
lines (`--only id,id`). Nobody producing an episode can hear it: say so in the report.

## 5. Build and check

```bash
cd learn && scripts/build-episode.sh <slug>          # timing → render (render lock) → web mp4 + poster
node scripts/dump-timeline.mjs <slug>                # frames per scene, line and beat
npx remotion still learn-<slug> out/check-<slug>/f<N>.jpg --frame=<N> --scale=0.5 --image-format=jpeg
```

Render a still mid-way through every beat, look at them (contact sheets are fine) and fix: text too
small, a ring on the wrong element, a label over content, a caption that contradicts the screen, a
cursor clicking nothing. Pull a few frames from the encoded mp4 too. Repeat until clean, then do the
final build.

## 6. Report

The final script, chapters with start times in seconds (scene start frame / 30), duration, shots
used (seeded / stubbed / copied), the stills reviewed, the voice table, where the narration was
changed to fit the UI, and what is unverified (the sound, motion in playback).

## Go easy on the machine

Other episodes are produced at the same time on the same computer. Run one demo server and one
browser at a time and stop them as soon as the shots are taken; never run two renders or two voice
jobs side by side (the locks see to that; do not bypass them); prefix long local jobs with
`nice -n 10`; render check stills one after another, not in parallel.
