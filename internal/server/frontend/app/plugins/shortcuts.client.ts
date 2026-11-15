import { installShortcutDispatcher, shortcutPlatform, shortcutShell } from '~/composables/useKeyboardShortcuts'
import { loadShortcutOverrides, startShortcutOverridesSync } from '~/utils/shortcuts/overridesSync'

/**
 * Nuxt client plugin: starts the one keyboard-shortcut listener (and, in the
 * desktop app, the native menu bridge). Handlers are registered by the
 * components that own each action; see composables/useKeyboardShortcuts.ts.
 * The user's own keys come from localStorage at once and from the server
 * once a signed-in page loads, and again when the window comes back
 * (utils/shortcuts/overridesSync.ts).
 */
export default defineNuxtPlugin(() => {
  installShortcutDispatcher(window)

  const { fetchWithAuth } = useAuthenticatedFetch()
  startShortcutOverridesSync({
    fetcher: (url, init) => fetchWithAuth(url, init as Parameters<typeof fetchWithAuth>[1]),
    platform: shortcutPlatform(),
    shell: shortcutShell()
  })

  const router = useRouter()
  const AUTH_PAGES = /^\/(login|setup|oauth-callback|access-denied)(\/|$)/
  let state: 'idle' | 'loading' | 'done' = 'idle'
  const run = () => {
    if (state !== 'idle' || AUTH_PAGES.test(router.currentRoute.value.path)) return
    state = 'loading'
    void loadShortcutOverrides().then((ok) => { state = ok ? 'done' : 'idle' })
  }
  void router.isReady().then(run)
  // After signing in, the first page past the login screen loads them.
  router.afterEach(() => run())
  // Changed in another tab or the desktop app meanwhile: read them again.
  let lastLoad = Date.now()
  const reload = () => {
    if (state !== 'done' || Date.now() - lastLoad < 5_000) return
    lastLoad = Date.now()
    state = 'idle'
    run()
  }
  window.addEventListener('focus', reload)
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') reload() })
})
