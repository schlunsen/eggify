# Motleyard frontend

An Astro site for the Motleyard brand and Mac rental concept. Eight static pages share a design system and small TypeScript controllers. No frontend framework runtime, Python generator, or backend is required.

## Development

Requires Node.js 22.12 or newer.

```sh
npm ci
npm run dev
```

Open http://127.0.0.1:4178. Astro provides live updates as files change.

```sh
npm run check         # Astro and TypeScript diagnostics
npm test              # Rental and sample-license state tests
npm run format:check  # Formatting
npm run build         # Check and generate dist/
npm run preview       # Serve the production build
```

## Source structure

- `src/pages/`: one Astro file per route. Existing `.html` URLs are preserved by the static `file` build format.
- `src/layouts/SiteLayout.astro`: document metadata, fonts, navigation, footer, and global styles.
- `src/components/`: reusable heroes, feature grids, FAQ sections, machine illustrations, and the workspace preview.
- `src/data/`: typed marketing content, navigation, and illustrative project data.
- `src/lib/`: pure rental configuration and demo-account state, plus a typed DOM helper.
- `src/scripts/`: page-specific TypeScript controllers. Astro bundles only scripts used by each page.
- `src/styles/`: palette tokens, base layout, page layouts, customer-area styles, and motion.
- `tests/`: state and interaction regression checks.

## Navigation and motion

This is a multi-page site, using native cross-document View Transitions. It does not intercept links or replace document navigation with a client router. Each page receives fresh script initialization; browser history, downloads, deep links, and form behavior stay native. Browsers without cross-document transitions use ordinary navigation and entrance animations. Reduced-motion preferences disable the motion.

## Demo boundaries

`/login.html` previews an email sign-in flow without sending email. `/account.html` uses sample data in session storage and accepts only `MOTLEY-DEMO`. It is publicly accessible and provides **no authentication or license enforcement**. Real serials and personal information must not be entered.

The configurator exports a text brief and creates shareable configuration URLs; it does not create orders. Rates, hardware specifications, subscriptions, and rental terms remain unannounced.

Before a production customer portal can launch, email authentication, account-to-license ownership verification, and activation changes must be implemented and authorized on the server. The demo account module must never be used as access control.

## Deployment

Run `npm run build`, then serve `dist/` with any static host. Neither dependencies nor generated output should be committed. The earlier HTML generator has been replaced by Astro source files.
