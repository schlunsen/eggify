import { computed, unref, type Ref, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  readFirstProjectNudgeDismissed, shouldShowFirstProjectNudge, writeFirstProjectNudgeDismissed,
} from '~/utils/projects/firstProjectNudge'
import type { ProjectSetupMode } from '~/utils/projects/projectSetup'

/**
 * Whether the agents page should invite the user to add a first project.
 * Counts projects from /api/projects/overview and waits for the page's
 * session list (zero projects with sessions is an existing user). A failed
 * first request counts as "not loaded"; a failed re-check keeps the last good
 * answer. The count is read again when the page regains focus (a project may
 * have been created in another tab); focus and visibilitychange together make
 * one request.
 */
export function useFirstProjectNudge(sessions: { loaded: Ref<boolean>, count: Ref<number> }) {
  const { fetchWithAuth } = useAuthenticatedFetch()
  const router = useRouter()
  const projectsLoaded = ref(false)
  const projectCount = ref(0)
  const dismissed = ref(readFirstProjectNudgeDismissed())

  const show = computed(() => shouldShowFirstProjectNudge({
    projectsLoaded: projectsLoaded.value, projectCount: projectCount.value,
    sessionsLoaded: unref(sessions.loaded), sessionCount: unref(sessions.count), dismissed: dismissed.value,
  }))

  let seq = 0
  let inFlight = false
  async function refresh() {
    if (inFlight) return
    inFlight = true
    const mine = ++seq
    try {
      const res = await fetchWithAuth('/api/projects/overview?git=0')
      if (!res.ok) throw new Error(String(res.status))
      const body = await res.json()
      if (mine !== seq) return
      projectCount.value = Array.isArray(body?.projects) ? body.projects.length : 0
      projectsLoaded.value = true
    } catch {
      // keep the last good answer; projectsLoaded stays false if there was none
    } finally {
      inFlight = false
    }
  }

  function dismiss() {
    dismissed.value = true
    writeFirstProjectNudgeDismissed()
  }
  function choose(mode: ProjectSetupMode) {
    router.push({ path: '/projects', query: { new: mode } })
  }

  function onVisible() {
    if (document.visibilityState === 'visible' && !dismissed.value) refresh()
  }
  onMounted(() => {
    if (!dismissed.value) refresh()
    window.addEventListener('focus', onVisible)
    document.addEventListener('visibilitychange', onVisible)
  })
  onBeforeUnmount(() => {
    seq++
    window.removeEventListener('focus', onVisible)
    document.removeEventListener('visibilitychange', onVisible)
  })

  return { show, refresh, dismiss, choose }
}
