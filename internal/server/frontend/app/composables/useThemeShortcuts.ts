import { ref } from 'vue'
import { useShortcut } from '~/composables/useKeyboardShortcuts'
import { useTheme } from '~/composables/useTheme'
import type { Theme } from '~/composables/useTheme'

/** The theme the cycle shortcut just switched to; ThemeToast.vue shows it. */
export const themeToast = ref<{ theme: Theme; n: number } | null>(null)
let toastSeq = 0

/**
 * Next / previous theme (⌥⌘] / ⌥⌘[; Ctrl+Alt+Y / Ctrl+Alt+Shift+Y on
 * Windows/Linux). Registered once in app.vue so it works on every page.
 */
export function useThemeShortcuts() {
  const { cycleTheme } = useTheme()
  const run = (dir: 1 | -1) => {
    const theme = cycleTheme(dir)
    themeToast.value = { theme, n: ++toastSeq }
  }
  useShortcut('theme.next', () => run(1))
  useShortcut('theme.previous', () => run(-1))
  return { next: () => run(1), previous: () => run(-1) }
}
