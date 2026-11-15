import { onMounted, onUnmounted, ref, type Ref } from 'vue'
import { desktopMachineUsage } from '~/utils/desktop'
import { parseSystemUsage, type SystemUsage } from '~/utils/systemUsage'

/** How often the meter refreshes while the page is visible. */
export const MACHINE_USAGE_POLL_MS = 5000

/**
 * CPU and memory per machine id for the meter next to the machine chip. In the
 * desktop app every Mac comes from the app (it asks the paired Macs through the
 * relay); elsewhere, or with an app too old to know them, only the machine
 * serving this page, keyed by currentId. Polls while the page is visible.
 */
export function useMachineUsage(currentId: Ref<string>) {
  const byId = ref<Record<string, SystemUsage>>({})
  let timer: ReturnType<typeof setInterval> | null = null
  let busy = false
  let disposed = false

  async function refresh() {
    if (busy || disposed) return
    busy = true
    try {
      const all = await desktopMachineUsage()
      if (all && Object.keys(all).length) {
        byId.value = all
        return
      }
      // Plain fetch (session cookie): a background meter must never send the
      // page to the login screen the way fetchWithAuth does on a 401.
      const resp = await fetch('/api/system/usage', { credentials: 'same-origin' })
      if (!resp.ok) return
      const u = parseSystemUsage(await resp.json())
      byId.value = u ? { [currentId.value]: u } : {}
    } catch {
      // keep the last reading; the next poll tries again
    } finally {
      busy = false
    }
  }

  function start() {
    stop()
    void refresh()
    timer = setInterval(() => void refresh(), MACHINE_USAGE_POLL_MS)
  }
  function stop() {
    if (timer) clearInterval(timer)
    timer = null
  }
  function onVisibility() {
    if (document.visibilityState === 'visible') start()
    else stop()
  }

  onMounted(() => {
    document.addEventListener('visibilitychange', onVisibility)
    onVisibility()
  })
  onUnmounted(() => {
    disposed = true
    stop()
    document.removeEventListener('visibilitychange', onVisibility)
  })

  return { byId, refresh }
}
