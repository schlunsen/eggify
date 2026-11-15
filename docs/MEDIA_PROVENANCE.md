# Media provenance

The root `LICENSE` (MIT) applies to Eggify's source code. It is **not** asserted over
third-party media. This file records where the images and audio tracked in this
repository came from, so that anyone redistributing the repository can check the
terms that actually apply.

Last reviewed: 2026-10-03.

## Generated in this repository

| Asset group | Files | How it was produced |
| --- | --- | --- |
| Cat avatars | `internal/server/frontend/public/avatars/cats/*.jpg` (74) | Text-to-image, `black-forest-labs/FLUX.1-schnell` via the Hugging Face Inference API. The prompts and generator are checked in: `scripts/generate_cat_avatars.sh`, `scripts/generate_cat_avatars_dali.py`, `scripts/generate_cat_avatars_line*.py`. |
| Website feature icons | `website/public/images/icon-*.png` | Text-to-image, FLUX.1 (commit `d9b99212`). |
| Website illustrations | `website/public/images/hero-illustration.png`, `feature-*.png`, `logo-cat-*.png` | Text-to-image, FLUX.1 (commit `d9b99212`). |
| Mascot images | `website/public/images/logo-eggify.png`, `ios/.../EggifyMascot.imageset/eggify-mascot.png`, `src-tauri/ui/logo.png` | Resized from the app icon master (see "App icon" below). |
| Presentation slide graphics | `website/public/presentation-images/*.svg` | Authored SVG; generator for the raster variants is `website/generate-graphics.py` (FLUX.1). |
| Presentation narration | `website/public/presentation-audio/slide-01..10.mp3` | Text-to-speech, Kokoro TTS, `af_sky` voice (commit `bfec488e`). Synthetic speech; no third-party recording or transcript. |
| Diagrams | `docs/architecture.svg`, `docs/architecture.png`, `docs/loop-controller-flow.svg`, `docs/loop-controller-flow.png` | Hand-authored SVG (plus PNG renders) created for this project. |
| App icon (egg gentleman) | `ios/.../AppIcon.appiconset/AppIcon-1024.png`, `src-tauri/icons/**`, `internal/server/frontend/public/pwa-*.png`, `docs/eggify-icon.png` | Text-to-image, `google/nano-banana-pro` via the Atlas Cloud API (2026-10-03). Prompt and generator: `scripts/generate_brand_art.sh`. Desktop/PWA sizes are resized with ImageMagick (macOS version: rounded square with padding and shadow) and `npx @tauri-apps/cli icon`. |
| Logo and favicons | `internal/server/frontend/app/components/EggifyLogo.vue`, `internal/server/frontend/public/eggify-logo.svg`, `favicon.svg`, `favicon.ico`, `website/public/favicon.ico` | Hand-authored SVG (egg in a bowler hat, `>` eye, monocle cursor, handlebar moustache); the `.ico` files are rendered from `eggify-logo.svg` with `rsvg-convert` + ImageMagick. |
| Banner | `docs/banner.png` | Text-to-image, `google/nano-banana-pro` via the Atlas Cloud API (2026-10-03), reduced to 1600 px / 256 colours. Prompt and generator: `scripts/generate_brand_art.sh`. |
| Landing page illustrations | `landing/src/assets/*.webp` (foot, egg, cherub, sessions, metrics, providers, sandbox, voice, handover) | Text-to-image via the Atlas Cloud API (2026-10-03): `google/nano-banana-pro`, except `egg` and `sandbox` (`bytedance/seedream-v5.0-pro`). Backgrounds removed with ImageMagick. `landing/src/assets/icon.png` and `landing/public/assets/og.jpg` derive from the app icon and banner. |
| Promo film audio | `promo/public/audio/vo-*.wav`, `promo/public/audio/music.mp3`, `promo/public/audio/thud.wav` | Narration: local text-to-speech with OmniVoice (`k2-fsa/OmniVoice`), a designed voice ("male, middle-aged, low pitch, british accent") cloned across lines. Music: instrumental generated with `suno/chirp-v6` via the Atlas Cloud API (2026-10-03). Thud: synthesised with ffmpeg. |
| Zen reel music | `internal/server/frontend/public/zen/audio/reel.mp3` | The promo film's instrumental (above: `suno/chirp-v6` via the Atlas Cloud API, 2026-10-03), re-encoded with ffmpeg to 128 kbps with a 1 s fade-in and a 2.5 s fade-out, and shipped inside the app as the default Zen music. The Suno and Atlas Cloud terms for redistributing this output inside a product have not been confirmed; the repository owner chose to bundle it on 2026-10-03. Replace the file (the app falls back to generated ambient music if it is missing) if the terms do not allow it. |
| Learning videos audio | `learn/public/episodes/*/vo/*.wav` (generated, not committed), `learn/tts/ref/ref.wav`, `learn/public/audio/music-bed.mp3`, `learn/public/audio/sting.mp3`, `learn/public/audio/alt/*.mp3` | Narration: local text-to-speech with OmniVoice (`k2-fsa/OmniVoice`), cloning the promo film's designed narrator from two of its lines (`ref.wav` = `vo-2.wav` + `vo-3.wav`, transcript in `ref.txt`); takes are checked with a local Whisper transcript and chosen by `learn/tts/gen.py` (2026-11-15). Music: two instrumental generations with `suno/chirp-v6` via the Atlas Cloud API (2026-11-15, `learn/scripts/gen-music.mjs`; styles "light dry British comedy underscore, jazz trio, upright bass, brushed drums, muted vibraphone, pizzicato strings…" for the bed and "short playful comic fanfare flourish, British music hall…" for the sting), cut to a 91 s loop and a 3.9 s sting, faded and levelled to -16 LUFS by `learn/tts/music_edit.py` (librosa + ffmpeg). As with the film's music, the Suno and Atlas Cloud terms for redistribution have not been confirmed. |
| Promo film screenshots | `promo/public/shots/*.png` | Captures of Eggify's own UI running on a seeded, fictional demo database (no real sessions, paths or people). |
| Circus subagent eggs | `internal/server/frontend/public/zen/circus/eggs/egg-*.webp` (gent, knight, hanky, parrot, egg, lady) | Text-to-image via the Atlas Cloud API (2026-10-04): `bytedance/seedream-v5.0-pro` (the model of the main egg), 1536 x 2304, prompts in `scripts/generate_circus_eggs.sh`. Cut out, toned, bordered and shadowed with Pillow/SciPy by `scripts/process_circus_eggs.py`, 600 px tall WebP (q82). One pick per kind, inspected before use. |
| Circus zen scene art | `internal/server/frontend/public/zen/circus/*.webp` (egg, foot, cherub, metrics) | Reused landing page illustrations (row above), resized to ≤600 px and re-encoded as webp (q80) with ImageMagick. |

Generative-model output has no settled, universal copyright status. These entries
record how each asset was made; they are not a legal opinion, and they do not
substitute for reviewing the terms of the model and API used.

## Screenshots

`docs/*.png` and `website/public/images/*.png` screenshots are captures of Eggify's
own UI. Screenshots that showed real session content, absolute personal paths,
third-party character artwork or a named remote server have been removed. Any new
screenshot must be taken against demo data on a local host before it is committed.

## Vendored third-party code with embedded notices

| File | Origin | License |
| --- | --- | --- |
| `website/public/three.min.js` | three.js | MIT (notice retained in the file header) |

## Removed for lack of redistribution permission

| File | Reason | Removed in |
| --- | --- | --- |
| `website/public/presentation-audio/bg-music.mp3` | Metadata identifies "Born to Win" by Aylex (publisher: Free To Use Music). The provider's [license](https://freetouse.com/license) permits attributed use in end-user content but separately prohibits making the digital asset available to third parties; redistributing the raw MP3 in a public repository is not supported by the permissions available at review time. | this cleanup |
| `src-tauri/ui/roar.mp3` | Lion-roar sound effect with encoder metadata only. No source or license recorded in its commit (`bd3976ab`), and none was supplied. | this cleanup |

Neither track is required for the software to build or run. If licensed
replacements are wanted later, add them together with a row in this table naming
the source and the terms.

## Bundled binaries

See [`../licenses/THIRD_PARTY_NOTICES.md`](../licenses/THIRD_PARTY_NOTICES.md)
for the ONNX Runtime and sherpa-onnx libraries shipped in macOS release archives.

## Still open

- Authority to publish contributions made on behalf of an employer or client is
  not established by scanning Git history and remains for the repository owner to
  confirm.
