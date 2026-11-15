import { configureSessionTagSync, syncSessionTags } from '~/utils/sessionTags'

/**
 * Nuxt client plugin: session tags are stored on the server too (so the iOS
 * app shows them). Writes in utils/sessionTags.ts go out through the app's
 * authenticated fetch; the server's tags are merged in once a signed-in page
 * loads, and again when the tab comes back after a minute or more.
 */
export default defineNuxtPlugin(() => {
  const { fetchWithAuth } = useAuthenticatedFetch()
  configureSessionTagSync((url, init) => fetchWithAuth(url, init as Parameters<typeof fetchWithAuth>[1]))

  const router = useRouter()
  const AUTH_PAGES = /^\/(login|setup|oauth-callback|access-denied)(\/|$)/
  let lastSync = 0
  const run = () => {
    if (AUTH_PAGES.test(router.currentRoute.value.path)) return
    lastSync = Date.now()
    void syncSessionTags()
  }

  void router.isReady().then(run)
  // After signing in, the first page past the login screen syncs.
  router.afterEach(() => { if (!lastSync) run() })
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && lastSync && Date.now() - lastSync > 60_000) run()
  })
})
