# eggify.xyz landing page

Static [Astro](https://astro.build) site in the Flying Circus style.

```bash
npm install
npm run dev       # http://localhost:4321
npm run build     # -> dist/
npm run deploy    # build + rsync to $DEPLOY_HOST:$LANDING_DEPLOY_DIR (see deploy.sh)
```

- `src/pages/index.astro` assembles the page from `src/components/`.
- Feature cards and ticker lines live in `src/data/sketches.ts`.
- Illustrations are in `src/assets/` (optimised by `astro:assets`); their
  provenance is in `../docs/MEDIA_PROVENANCE.md`.

## Hosting

Any static host works. `deploy.sh` rsyncs `dist/` to `$DEPLOY_HOST:$LANDING_DEPLOY_DIR`
(both from the environment or `~/.config/eggify/release.env`) and leaves `/download/` and
`/media/` alone. Set `PUBLIC_SITE_URL`, `PUBLIC_LICENSE_URL` and `PUBLIC_DOWNLOAD_BASE_URL`
at build time to point the page at your own site, license server and release folder.
