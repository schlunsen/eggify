# Zen Circus backdrop art

Engraved cut-outs for the ten Circus backdrops (`app/composables/agents/zen/circusBackdrops.ts`)
and the drifting clouds (`sceneCircus.ts`).

**Generated** with Atlas Cloud image generation, model `openai/gpt-image-2/text-to-image`
(about 1254 x 1254 PNG). Every prompt is the subject below followed by this style suffix:

> 19th century steel engraving, fine crosshatching and stipple, black ink on aged cream paper, single isolated object, plain flat white background, no text, no frame, Victorian illustration, slightly surreal

**Processed** locally with Pillow + SciPy:

1. White paper background removed: pixels with every channel >= 232 that touch the image edge
   (connected components), plus, where noted, enclosed paper gaps larger than 250 px (between
   branches, fronds, wings). Specks under 0.2% of the image dropped, 1 px erosion, 0.9 px
   feathered alpha.
2. Hand-tinted wash: the image blended towards `image x wash colour` (multiply) at the given
   strength; the lightest paper toned to warm cream (`#f4ebd3`).
3. Trimmed, downscaled (Lanczos) to the given long side.
4. A cream scissor-cut border (`#f4ecd6`, dilated alpha) and a soft umber drop shadow (38%,
   offset 4,5 px, blur 2.2 px) baked in, so the page needs no CSS filters.
5. Saved as WebP, quality 82, method 6.

Every generated image was inspected before use. `BACKDROP_ART` in `circusBackdrops.ts`
records each file's pixel size (a test checks it against the files).

| File | Size | Prompt subject | Processing |
| --- | --- | --- | --- |
| `bats.webp` | 37 KB | Four bats with outstretched wings flying, in various poses, arranged in a loose group | wash `#8a6f93` at 35%, long side 420 px, cream border 6 px, enclosed paper gaps removed |
| `camel.webp` | 60 KB | A grumpy dromedary camel standing in full side view, wearing a tiny bowler hat | wash `#c28a4c` at 40%, long side 560 px, cream border 6 px |
| `castle.webp` | 62 KB | A small fairytale castle with two crenellated towers and a flag on a little rocky mound, seen from the front | wash `#7fae9f` at 38%, long side 520 px, cream border 6 px |
| `church.webp` | 40 KB | A small snowy village church with a pointed steeple and a snowy roof, front view, a few snow drifts at the base | wash `#b98a7a` at 30%, long side 520 px, cream border 6 px |
| `clock.webp` | 29 KB | An ornate Victorian grandfather longcase clock with a pendulum window, front view | wash `#b07a45` at 35%, long side 560 px, cream border 6 px |
| `cloud.webp` | 83 KB | A single billowing cumulus cloud with curling rolled edges, heavily engraved with parallel hatching | wash `#efe3c4` at 35%, long side 600 px, cream border 0 px |
| `crescent.webp` | 50 KB | A crescent moon with a sleepy human face in profile, engraved with fine hatching | wash `#f3e3a8` at 55%, long side 520 px, cream border 6 px |
| `crescent-sage.webp` | 57 KB | A crescent moon with a sleepy human face in profile, engraved with fine hatching | wash `#b9cfae` at 45%, long side 560 px, cream border 6 px |
| `deadtree.webp` | 76 KB | A gnarled crooked bare dead tree with twisted branches and a few hanging leaves | wash `#8a7aa0` at 35%, long side 520 px, cream border 3 px, enclosed paper gaps removed |
| `fern.webp` | 68 KB | Giant overgrown tropical ferns and monstera leaves rising from the ground in a cluster | wash `#6f9456` at 55%, long side 560 px, cream border 6 px, enclosed paper gaps removed |
| `gulls.webp` | 49 KB | Three seagulls in flight with spread wings, different poses, arranged in a loose group | wash `#e8e0cc` at 20%, long side 460 px, cream border 6 px, enclosed paper gaps removed |
| `lighthouse.webp` | 37 KB | A tall tapering lighthouse on a rocky base with a lantern room, a beam of light drawn with fine radiating lines | wash `#c0674a` at 35%, long side 560 px, cream border 6 px |
| `lighthouse-slate.webp` | 32 KB | A tall tapering lighthouse on a rocky base with a lantern room, a beam of light drawn with fine radiating lines | wash `#7d8ea6` at 45%, long side 520 px, cream border 6 px |
| `moonface.webp` | 106 KB | A full moon with a large melancholy human face with craters, wearing a night cap, engraved with fine hatching | wash `#cfd2e0` at 35%, long side 520 px, cream border 6 px |
| `palm.webp` | 50 KB | A tall date palm tree with a curved trunk and drooping fronds | wash `#8c9a52` at 40%, long side 520 px, cream border 6 px, enclosed paper gaps removed |
| `pier.webp` | 81 KB | A long wooden seaside pier on stilts with a little pavilion and a flag, seen from the side, with a few waves at the base | wash `#a8845a` at 35%, long side 700 px, cream border 6 px, enclosed paper gaps removed |
| `pines.webp` | 58 KB | A cluster of three tall snow-laden pine trees of different heights | wash `#6f8f6a` at 45%, long side 560 px, cream border 6 px, enclosed paper gaps removed |
| `plant.webp` | 64 KB | A giant carnivorous plant with a big open toothy trumpet flower on a thick stem with tendrils | wash `#a85a78` at 40%, long side 520 px, cream border 6 px |
| `portrait.webp` | 34 KB | An ornate gilded oval picture frame containing the stern portrait of a walrus in a naval admiral uniform | wash `#c9a24a` at 30%, long side 420 px, cream border 6 px |
| `pyramids.webp` | 65 KB | Three Egyptian pyramids of different sizes standing on a strip of sand, hatched shading on the shadow sides | wash `#d49a55` at 45%, long side 700 px, cream border 6 px |
| `rocket.webp` | 32 KB | A Victorian-era fantastical bullet-shaped rocket ship with rivets, portholes and fins, flames beneath, in the manner of Jules Verne | wash `#c9694a` at 30%, long side 560 px, cream border 6 px |
| `saturn.webp` | 56 KB | A ringed planet like Saturn with a grumpy face and engraved banded shading | wash `#d49a5a` at 45%, long side 560 px, cream border 6 px |
| `ship.webp` | 108 KB | A tall three-masted sailing ship with full billowing sails seen from the side, slightly crooked, sailing on a few engraved waves | wash `#c48a56` at 30%, long side 640 px, cream border 6 px |
| `stormcloud.webp` | 82 KB | A big dramatic thunder cloud with a forked lightning bolt striking down from it, heavy crosshatched shadows, rain streaks | wash `#6f819c` at 55%, long side 620 px, cream border 0 px |
| `sun-ochre.webp` | 103 KB | A huge radiant sun with a stern moustached face and long alternating straight and wavy rays | wash `#e0a63c` at 55%, long side 640 px, cream border 6 px |
| `sun-pale.webp` | 96 KB | A huge radiant sun with a stern moustached face and long alternating straight and wavy rays | wash `#f0d9a0` at 45%, long side 560 px, cream border 6 px |
| `teapot.webp` | 55 KB | An ornate Victorian porcelain teapot with a cup and saucer and a slice of cake on a stand | wash `#7aa3a0` at 35%, long side 560 px, cream border 6 px |
| `tombstones.webp` | 63 KB | A cluster of crooked weathered tombstones, a stone cross and a small angel statue, with tufts of grass at the base | wash `#7d8f88` at 40%, long side 520 px, cream border 6 px |
