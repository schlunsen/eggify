import { startPerfReporter } from '~/utils/perfReporter'
import { useSessionStore } from '~/stores/session/sessionStore'
import { isAuthPage } from '~/utils/loginRedirect'

/**
 * Nuxt client plugin: reports the dashboard's own health (DOM size, running
 * animations, fps) to the server every 30 s for Settings → Performance.
 */
export default defineNuxtPlugin(() => {
  const sessionStore = useSessionStore()
  const route = useRoute()
  const { fetchWithAuth } = useAuthenticatedFetch()
  startPerfReporter({
    // Signed out on the login page: a 401 there is only noise.
    post: sample => isAuthPage(route.path) ? undefined : fetchWithAuth('/api/perf/client', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(sample),
    }),
    getContext: () => ({
      path: route.path,
      messages: sessionStore.totalMessageCount,
      sessions: sessionStore.sessions.length,
    }),
  })
})
