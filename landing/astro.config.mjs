// @ts-check
import { defineConfig } from 'astro/config';

export default defineConfig({
  site: 'https://eggify.xyz',
  // Fully static output; deploy.sh syncs dist/ to the web server.
  output: 'static',
  build: { inlineStylesheets: 'auto' },
});
