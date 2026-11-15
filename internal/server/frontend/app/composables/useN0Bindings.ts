import { ref } from 'vue'
import { useAuthenticatedFetch } from './useAuthenticatedFetch'

/**
 * Shared, cached map of session id → n0 binding (GET /api/connectors/n0/bindings).
 *
 * One fetch serves every SessionItem; callers trigger `scheduleRefresh()` (debounced)
 * when the session list changes, or `refresh()` after they changed a binding.
 */

export interface N0Binding {
  connection_id: number
  host: string
  app_subdomain: string
  app_name: string
  workspace_name: string
  status: string
}

const bindings = ref<Record<string, N0Binding>>({})
const loaded = ref(false)
let inFlight: Promise<void> | null = null
let debounceTimer: ReturnType<typeof setTimeout> | null = null

export const N0_BINDINGS_DEBOUNCE_MS = 500

export function useN0Bindings() {
  const { fetchWithAuth } = useAuthenticatedFetch()

  const refresh = (): Promise<void> => {
    if (inFlight) return inFlight
    inFlight = (async () => {
      try {
        const response = await fetchWithAuth('/api/connectors/n0/bindings', { method: 'GET' })
        if (response.ok) {
          const data = await response.json()
          bindings.value = data?.bindings && typeof data.bindings === 'object' ? data.bindings : {}
          loaded.value = true
        }
      } catch (err) {
        console.warn('[n0] Failed to load session bindings:', err)
      } finally {
        inFlight = null
      }
    })()
    return inFlight
  }

  const scheduleRefresh = (delay = N0_BINDINGS_DEBOUNCE_MS) => {
    if (debounceTimer) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(() => {
      debounceTimer = null
      void refresh()
    }, delay)
  }

  const getBinding = (sessionId: string): N0Binding | null => bindings.value[sessionId] || null

  return { bindings, loaded, refresh, scheduleRefresh, getBinding }
}
