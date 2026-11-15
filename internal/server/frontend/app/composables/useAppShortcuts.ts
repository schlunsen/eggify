import { useShortcut } from '~/composables/useKeyboardShortcuts'

/** The Projects page opens its new-project flow for this route. */
export const NEW_PROJECT_ROUTE = '/projects?new=1'

/** App-wide shortcuts that app.vue owns: switch project (⇧⌘P), new project (⇧⌘N). */
export function useAppShortcuts(opts: {
  openProjectSelector: () => void
  navigate: (path: string) => unknown
  enabled: () => boolean
}) {
  useShortcut('project.switch', () => opts.openProjectSelector(), { when: opts.enabled })
  useShortcut('project.new', () => { void opts.navigate(NEW_PROJECT_ROUTE) }, { when: opts.enabled })
}
