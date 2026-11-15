export type ThemeVariant = 'default-dark' | 'default-light' | 'neon-dark' | 'neon-light' | 'nord-dark' | 'nord-light' | 'dracula-dark' | 'dracula-light' | 'southpark-dark' | 'southpark-light' | 'eggify-dark' | 'eggify-light' | 'flyingcircus-light' | 'flyingcircus-dark' | 'theatre-light' | 'theatre-dark' | 'motleyard-light' | 'motleyard-dark'

export interface Theme {
  id: ThemeVariant
  name: string
  description: string
  isDark: boolean
  fontFamily: string
  fontDescription: string
  /** Kept out of the theme pickers and the theme cycle shortcut (experimental). */
  hidden?: boolean
}

export const availableThemes: Theme[] = [
  {
    id: 'theatre-light',
    name: 'Puppet Theatre Light',
    description: 'The Zen stage in daylight: curtain crimson, teal sky and honey floorboards on warm paper',
    isDark: false,
    fontFamily: 'Fraunces',
    fontDescription: 'Playbill serif headings, clean sans for dense UI, typewriter code'
  },
  {
    id: 'theatre-dark',
    name: 'Puppet Theatre Dark',
    description: 'A night at the theatre: umber house, teal-night sky, crimson velvet and footlight gold',
    isDark: true,
    fontFamily: 'Fraunces',
    fontDescription: 'Playbill serif headings, clean sans for dense UI, typewriter code'
  },
  {
    id: 'motleyard-light',
    name: 'Motleyard Light',
    description: 'An independent workshop: warm paper, sage surfaces, dark ink and acid-yellow details',
    isDark: false,
    fontFamily: 'Space Grotesk',
    fontDescription: 'Geometric sans-serif with precise DM Mono code'
  },
  {
    id: 'motleyard-dark',
    name: 'Motleyard Dark',
    description: 'The workshop after hours: charcoal green, warm chalk and electric-lime details',
    isDark: true,
    fontFamily: 'Space Grotesk',
    fontDescription: 'Geometric sans-serif with precise DM Mono code'
  },
  {
    id: 'eggify-light',
    name: 'Eggify Light',
    description: 'Warm paper theme',
    isDark: false,
    fontFamily: 'Inter',
    fontDescription: 'Clean sans-serif with serif headings'
  },
  {
    id: 'eggify-dark',
    name: 'Eggify Dark',
    description: 'Warm paper-inspired theme with terracotta accents',
    isDark: true,
    fontFamily: 'Inter',
    fontDescription: 'Clean sans-serif with serif headings'
  },
  {
    id: 'flyingcircus-light',
    name: 'Flying Circus Light',
    description: 'And now for something completely different: cut-out engravings on aged paper',
    isDark: false,
    fontFamily: 'EB Garamond',
    fontDescription: 'Engraved-book serif, circus-poster headings, typewriter code'
  },
  {
    id: 'flyingcircus-dark',
    name: 'Flying Circus Dark',
    description: 'The same collage after closing time: cream ink, pink, mustard and teal',
    isDark: true,
    fontFamily: 'EB Garamond',
    fontDescription: 'Engraved-book serif, circus-poster headings, typewriter code'
  },
  {
    id: 'default-dark',
    name: 'Default Dark',
    description: 'Classic dark theme with purple accents',
    isDark: true,
    fontFamily: 'Inter',
    fontDescription: 'Modern, clean sans-serif'
  },
  {
    id: 'default-light',
    name: 'Default Light',
    description: 'Clean light theme',
    isDark: false,
    fontFamily: 'Inter',
    fontDescription: 'Modern, clean sans-serif'
  },
  {
    id: 'neon-dark',
    name: 'Neon Dark',
    description: 'Cyberpunk-inspired neon dark theme',
    isDark: true,
    fontFamily: 'Orbitron',
    fontDescription: 'Futuristic, geometric display font'
  },
  {
    id: 'neon-light',
    name: 'Neon Light',
    description: 'Bright neon accents on light background',
    isDark: false,
    fontFamily: 'Orbitron',
    fontDescription: 'Futuristic, geometric display font'
  },
  {
    id: 'nord-dark',
    name: 'Nord Dark',
    description: 'Arctic-inspired cool blues and muted tones',
    isDark: true,
    fontFamily: 'Fira Code',
    fontDescription: 'Developer-focused monospaced font with ligatures'
  },
  {
    id: 'nord-light',
    name: 'Nord Light',
    description: 'Bright Nordic palette with subtle blues',
    isDark: false,
    fontFamily: 'Fira Code',
    fontDescription: 'Developer-focused monospaced font with ligatures'
  },
  {
    id: 'dracula-dark',
    name: 'Dracula Dark',
    description: 'Vibrant purple and pink tones on dark background',
    isDark: true,
    fontFamily: 'JetBrains Mono',
    fontDescription: 'Professional coding font with excellent clarity'
  },
  {
    id: 'dracula-light',
    name: 'Dracula Light',
    description: 'Soft pastels with Dracula accent colors',
    isDark: false,
    fontFamily: 'JetBrains Mono',
    fontDescription: 'Professional coding font with excellent clarity'
  },
  {
    id: 'southpark-dark',
    name: 'South Park Dark',
    description: 'Respect my authoritah! Bold colors and comic style',
    isDark: true,
    fontFamily: 'Comic Sans MS',
    fontDescription: 'Oh my God! They used Comic Sans!'
  },
  {
    id: 'southpark-light',
    name: 'South Park Light',
    description: 'Screw you guys, I\'m going light mode!',
    isDark: false,
    fontFamily: 'Comic Sans MS',
    fontDescription: 'Oh my God! They used Comic Sans!'
  }
]

/** The themes the pickers show, in their order (the cycle shortcut uses the same list). */
export const pickerThemes: Theme[] = availableThemes.filter(t => !t.hidden)

/**
 * The theme after (dir 1) or before (dir -1) `current` in `themes`, wrapping
 * around. A current theme that is not in the list (hidden, unknown, or an
 * automatic mode) starts from `applied`, the theme actually on the page; when
 * neither is in the list, next gives the first theme and previous the last.
 */
export function cycleThemeId(
  current: string | null | undefined,
  dir: 1 | -1,
  themes: readonly Theme[] = pickerThemes,
  applied?: string | null
): ThemeVariant {
  if (!themes.length) throw new Error('no themes to cycle')
  let i = themes.findIndex(t => t.id === current)
  if (i < 0 && applied) i = themes.findIndex(t => t.id === applied)
  if (i < 0) return (dir === 1 ? themes[0]! : themes[themes.length - 1]!).id
  return themes[(i + dir + themes.length) % themes.length]!.id
}

/**
 * Applies a theme change with a short cross-fade (View Transitions, where the
 * browser has them and motion is allowed) so the page does not flash.
 */
export function withThemeTransition(apply: () => void): void {
  const doc = typeof document !== 'undefined' ? document as Document & { startViewTransition?: (cb: () => void) => unknown } : null
  const reduced = typeof window !== 'undefined' && typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (doc?.startViewTransition && !reduced) {
    try {
      doc.startViewTransition(apply)
      return
    } catch { /* fall through */ }
  }
  apply()
}

/** Theme for new users (a stored choice always wins). */
export const DEFAULT_THEME: ThemeVariant = 'theatre-light'

/**
 * The other half of a theme's dark/light pair: `<family>-dark` <-> `<family>-light`.
 * Falls back to the default family's pair for anything unknown.
 */
export function pairedTheme(id: ThemeVariant): ThemeVariant {
  const theme = availableThemes.find(t => t.id === id)
  const match = /^(.*)-(dark|light)$/.exec(id)
  if (theme && match) {
    const counterpart = `${match[1]}-${theme.isDark ? 'light' : 'dark'}` as ThemeVariant
    if (availableThemes.some(t => t.id === counterpart)) return counterpart
  }
  return theme?.isDark ? 'default-light' : 'default-dark'
}

export const useTheme = () => {
  const currentTheme = useState<ThemeVariant>('theme', () => {
    // A stored choice wins; new users get DEFAULT_THEME
    if (typeof window !== 'undefined') {
      const stored = localStorage.getItem('eggify-theme') as ThemeVariant
      if (stored && availableThemes.some(t => t.id === stored)) {
        return stored
      }
    }
    return DEFAULT_THEME
  })

  const currentThemeData = computed(() => {
    return availableThemes.find(t => t.id === currentTheme.value) || availableThemes[0]
  })

  const isDark = computed(() => currentThemeData.value.isDark)

  const setTheme = (themeId: ThemeVariant) => {
    currentTheme.value = themeId
    updateTheme()
  }

  /**
   * Switches to the next (1) or previous (-1) theme of the pickers, wrapping
   * around, and saves it like choosing it in Settings. Returns the new theme.
   */
  const cycleTheme = (dir: 1 | -1): Theme => {
    const applied = typeof document !== 'undefined' ? document.documentElement.getAttribute('data-theme') : null
    const id = cycleThemeId(currentTheme.value, dir, pickerThemes, applied)
    withThemeTransition(() => setTheme(id))
    return availableThemes.find(t => t.id === id)!
  }

  const toggleDarkMode = () => {
    // Toggle between the dark and light variant of the current theme family
    currentTheme.value = pairedTheme(currentTheme.value)
    updateTheme()
  }

  const updateTheme = () => {
    if (typeof window !== 'undefined') {
      const html = document.documentElement
      html.setAttribute('data-theme', currentTheme.value)
      localStorage.setItem('eggify-theme', currentTheme.value)
    }
  }

  // Initialize theme on mount
  if (typeof window !== 'undefined') {
    onMounted(() => {
      updateTheme()
    })
  }

  return {
    currentTheme: readonly(currentTheme),
    currentThemeData: readonly(currentThemeData),
    isDark: readonly(isDark),
    // The pickers' list: hidden themes left out
    availableThemes: pickerThemes,
    setTheme,
    cycleTheme,
    toggleDarkMode
  }
}
