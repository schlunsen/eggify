# Eggify promo film

A 60-second, 1920×1080 motion-graphics film in the Flying Circus style, made
with [Remotion](https://www.remotion.dev) (React). Shown on eggify.xyz.

```bash
npm install
npm run studio     # preview and scrub in the browser
npm run render     # -> out/eggify-promo.mp4
npm run deploy     # poster + upload to https://eggify.xyz/media/
```

- `src/timeline.ts`: scene timings and the voice-over lines (with captions).
- `src/scenes/scenes.tsx`: the eight scenes. `src/components/paper.tsx` has the
  paper background, stop-motion jitter, cut-outs, screenshot cards, stamps, clouds.
- `public/shots/`: real screenshots of Eggify running on a seeded demo database
  (no real user data), Flying Circus theme.
- `public/art/`: engraved cut-outs, shared with the landing page.
- `public/audio/`: narration (`vo-*.wav`), music (`music.mp3`, ducked under the
  voice in `Promo.tsx`) and a thud for the foot.

Remotion is free for individuals and small teams; check its licence if that changes.
