# Frontend Theme System

This directory contains all theme definitions for the Eggify analytics dashboard. The theme system uses CSS custom properties (variables) for a clean, maintainable approach.

## Structure

```
themes/
├── README.md              # This file
├── _variables.css         # Default and light themes
├── neon.css               # Neon dark/light themes (Cyberpunk-inspired)
├── nord.css               # Nord dark/light themes (Arctic-inspired)
├── dracula.css            # Dracula dark/light themes (Vibrant purple)
├── southpark.css          # South Park dark/light themes (Comic-style)
├── eggify.css             # Eggify dark/light themes (Professional monochromatic)
├── flyingcircus.css       # Flying Circus dark/light themes (Victorian cut-out collage)
├── motleyard.css          # Motleyard dark/light themes (independent workshop)
└── theatre.css            # Puppet Theatre dark/light themes (the Zen Circus stage) - the default
```

The default theme for new users is `DEFAULT_THEME` in `app/composables/useTheme.ts`
(currently `theatre-light`). A theme stored in `localStorage` (`eggify-theme`) always wins.

## Adding a New Theme

To add a new theme:

1. **Create a new CSS file** in this directory (e.g., `mytheme.css`)

2. **Define CSS custom properties** for both dark and light variants:

```css
/* My Theme Dark */
[data-theme="mytheme-dark"] {
  --bg-primary: #1a1a1a;
  --bg-secondary: #2d2d2d;
  --bg-tertiary: #3a3a3a;
  --text-primary: #e8e8e8;
  --text-secondary: #b8b8b8;
  --text-muted: #888888;
  --accent-purple: #a78bfa;    /* Primary accent (used for buttons, links) */
  --accent-cyan: #67e8f9;      /* Secondary accent */
  --accent-green: #4ade80;     /* Success state */
  --accent-yellow: #fbbf24;    /* Warning state */
  --accent-orange: #ff9800;    /* Alternative accent */
  --border-color: #3a3a3a;
  --code-bg: #242424;
  --card-bg: #3a3a3a;
  --card-hover: #444444;
  --status-success: #4ade80;
  --status-warning: #fbbf24;
  --status-error: #f87171;

  /* Context Progress Bar Colors */
  --progress-start: #67e8f9;      /* 0-25% */
  --progress-low: #a78bfa;        /* 25-50% */
  --progress-medium: #fbbf24;     /* 50-75% */
  --progress-high: #f87171;       /* 75-100% */

  /* Typography */
  --font-primary: 'FontName', 'Fallback', sans-serif;
  --font-mono: 'MonoFont', monospace;
}

/* My Theme Light */
[data-theme="mytheme-light"] {
  /* Similar structure for light variant */
}
```

3. **Import the file** in `main.css`:

```css
@import './themes/mytheme.css';
```

4. **Register the theme** in `composables/useTheme.ts`:

```typescript
export type ThemeVariant = '...' | 'mytheme-dark' | 'mytheme-light'

export const availableThemes: Theme[] = [
  // ... existing themes
  {
    id: 'mytheme-dark',
    name: 'My Theme Dark',
    description: 'Description of your theme',
    isDark: true,
    fontFamily: 'FontName',
    fontDescription: 'Font description'
  },
  {
    id: 'mytheme-light',
    name: 'My Theme Light',
    description: 'Light variant description',
    isDark: false,
    fontFamily: 'FontName',
    fontDescription: 'Font description'
  }
]
```

5. **Dark/light toggle**: nothing to add. `pairedTheme()` in `composables/useTheme.ts`
   pairs `<family>-dark` with `<family>-light` automatically, so name the two variants
   that way. `app/__tests__/composables/useTheme.test.ts` checks that every theme has
   a counterpart.

6. **Add Google Font** (if needed) in `nuxt.config.ts`:

```typescript
// In the link array within app.head.meta
{ rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=YourFont:wght@400;500;600;700&display=swap' }
```

## CSS Variable Reference

### Background Colors
- `--bg-primary`: Main background color
- `--bg-secondary`: Secondary background (slightly lighter/darker)
- `--bg-tertiary`: Tertiary background (cards, containers)
- `--card-bg`: Card background color
- `--card-hover`: Card hover state background
- `--code-bg`: Code block background

### Text Colors
- `--text-primary`: Main text color
- `--text-secondary`: Secondary text (less emphasis)
- `--text-muted`: Muted/disabled text

### Accent Colors
- `--accent-purple`: Primary accent (buttons, links, highlights)
- `--accent-cyan`: Secondary accent
- `--accent-green`: Success/positive state
- `--accent-yellow`: Warning state
- `--accent-orange`: Alternative accent

### Status Colors
- `--status-success`: Success indicator
- `--status-warning`: Warning indicator
- `--status-error`: Error indicator

### Progress Bar Colors
Used for context usage bars and similar indicators:
- `--progress-start`: 0-25% usage
- `--progress-low`: 25-50% usage
- `--progress-medium`: 50-75% usage
- `--progress-high`: 75-100% usage

### Border & Structure
- `--border-color`: Border and divider color

### Typography
- `--font-primary`: Main font family
- `--font-mono`: Monospace font for code

## Current Themes

### Default (Inter font)
- **Dark**: Classic dark theme with purple accents
- **Light**: Clean light theme

### Neon (Orbitron font)
- **Dark**: Cyberpunk-inspired with bright neon colors
- **Light**: Bright neon accents on light background

### Nord (Fira Code/Fira Sans)
- **Dark**: Arctic-inspired cool blues and muted tones
- **Light**: Bright Nordic palette with subtle blues

### Dracula (JetBrains Mono)
- **Dark**: Vibrant purple and pink on dark background
- **Light**: Soft pastels with Dracula accent colors

### South Park (Comic Sans MS)
- **Dark**: Bold colors and comic book style
- **Light**: Comic book palette on light background

### Eggify (Questrial)
- **Dark**: Professional dark theme with monochromatic gray palette
- **Light**: Clean light theme with monochromatic gray palette
- **Colors**: Professional palette using #DBDBDB, #4A4A4B, #848383

### Flying Circus (Rye / EB Garamond / Courier Prime)
*And now for something completely different.* Victorian engravings cut out
with scissors and glued onto aged paper, matching the Eggify logo and app icon.
- **Light**: Aged cream paper, sepia engraving ink, hand-tinted pink primary
- **Dark**: The same collage after closing time: burnt umber with cream ink
- **Extras**: Circus-poster `h1`/`h2`, a faint engraving hatch on the page, a
  pink/mustard/teal paper strip under the navbar, mustard scrollbars
- **Decorative tokens**: `--circus-pink`, `--circus-mustard`, `--circus-teal`, `--circus-hatch`

### Puppet Theatre (Fraunces / Inter / Courier Prime) - default
The paper puppet theatre from Zen mode (`app/composables/agents/zen/sceneCircus.ts`)
carried into the whole app, so the chrome and the Zen stage read as one piece.
- **Palette** (from the scene): curtain crimson `#9e2f4c`, teal sky `#2a9d8f`,
  mustard `#d4a017`, bunting pink `#e8458b`, paper-sun gold `#e7b73c`, honey
  floorboards `#ead9ad`, cream paper clouds `#fdf9ef`, umber ink `#2b1d10`
- **Light**: the stage in daylight: warm paper surfaces, umber ink, crimson primary
- **Dark**: a night at the theatre: umber house, teal-night navbar, rose-crimson
  text accents (`#e8728c`, readable on umber), crimson velvet and footlight gold
- **Extras** (all under `html[data-theme^="theatre"]`): a crimson pelmet with a gold
  trim and a strip of paper bunting under the navbar; the active nav item is a piece
  of curtain velvet; panels, modals and the settings menu are paper cards with a hard
  offset shadow (modals hang from a crimson rail); the bottom toolbar is honey
  floorboards with footlights; footlight-gold focus rings (a >= 3:1 brass outline
  plus a gold glow); a scalloped crimson valance over the Zen Circus stage; Fraunces
  playbill headings
- **Dark fills**: in the dark variant `--accent-purple` is a light rose so crimson
  text passes AA on umber, but white text on it would not. `theatre.css` therefore
  resets `--accent-purple` to the deep velvet (`--theatre-crimson`, white on it is
  6.6:1) on the controls that put white text on a primary fill. If you add such a
  control, add its selector to that list.
- **Decorative tokens**: `--theatre-crimson`, `--theatre-crimson-deep`, `--theatre-pink`,
  `--theatre-mustard`, `--theatre-teal`, `--theatre-gold`, `--theatre-honey`,
  `--theatre-nav-bg`, `--theatre-card-edge`, `--theatre-card-shadow`, `--theatre-focus`

### Motleyard (Space Grotesk / DM Mono)
The Motleyard website's workshop identity adapted for the dense app UI.
- **Light**: warm paper `#edece5`, sage panels, dark ink, olive text accents
- **Dark**: charcoal green, warm chalk text, lime text accents
- **Both**: acid-yellow `#e4fc54` new-session and send buttons with dark labels,
  crisp card edges, flat surfaces, geometric headings and visible focus rings
- **Readability**: lime is not used as light-mode text. Dark controls with white
  labels use an olive primary fill; the signature actions explicitly pair lime
  with dark ink. Add new white-label primary controls to the scoped list in
  `motleyard.css`, as with Puppet Theatre.
- **Scope**: selectable light/dark pair; existing saved choices and the default
  theme are unchanged. Zen retains its own scene and composer styling.
- **Decorative tokens**: `--yard-lime`, `--yard-ink`, `--yard-selected`, `--yard-focus`

## Usage in Components

Use CSS variables in your Vue components:

```vue
<style scoped>
.my-component {
  background: var(--bg-primary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
}

.button {
  background: var(--accent-purple);
  color: white;
}

.status-success {
  color: var(--status-success);
}
</style>
```

## Tips

1. **Always use CSS variables** instead of hardcoded colors for theme consistency
2. **Test both dark and light variants** when adding new theme elements
3. **Use semantic naming** (e.g., `--status-error` instead of `--red-color`)
4. **Document your theme** in this README when adding a new one
5. **Consider accessibility** - ensure sufficient color contrast ratios (WCAG AA:
   4.5:1 for text, 3:1 for large text, controls and focus rings). Don't hardcode
   text colours like `#ffc107` or `#10b981`: they fail on light themes. Use
   `var(--accent-yellow, #ffc107)` / `var(--status-success, #10b981)` instead
6. **Use the progress bar colors** for context/resource usage indicators
