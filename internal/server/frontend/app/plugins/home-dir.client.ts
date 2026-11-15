import { loadHomeDir } from '~/utils/homeDir'

/**
 * Nuxt client plugin: asks the server for its home folder once a signed-in page
 * loads, so paths can be shortened to `~` only inside it (utils/homeDir.ts).
 */
export default defineNuxtPlugin(() => {
  const { fetchWithAuth } = useAuthenticatedFetch()
  const router = useRouter()
  const AUTH_PAGES = /^\/(login|setup|oauth-callback|access-denied|notch)(\/|$)/
  const run = () => {
    if (AUTH_PAGES.test(router.currentRoute.value.path)) return
    void loadHomeDir(url => fetchWithAuth(url))
  }
  void router.isReady().then(run)
  // After signing in, the first page past the login screen asks.
  router.afterEach(run)
})
