import { ref } from 'vue'
import { useAuthenticatedFetch } from './useAuthenticatedFetch'

/**
 * Global sandbox settings: the default for new sessions and the egress
 * allowlist (GET/PUT /api/agent/sandbox). Saving needs a signed-in user;
 * the server answers 403 to API-key-only requests when user auth is on.
 */

export interface SandboxSettings {
  can_authorize_codex_network: boolean
  default_enabled: boolean
  allowed_domains: string[]
  default_allowed_domains: string[]
}

export function useSandboxSettings() {
  const { fetchWithAuth } = useAuthenticatedFetch()
  const settings = ref<SandboxSettings | null>(null)
  const loading = ref(false)
  const saving = ref(false)
  const error = ref<string | null>(null)

  const readError = async (res: Response, fallback: string) => {
    try {
      const body = await res.json()
      return body?.error || fallback
    } catch {
      return fallback
    }
  }

  const load = async (): Promise<SandboxSettings | null> => {
    loading.value = true
    error.value = null
    try {
      const res = await fetchWithAuth('/api/agent/sandbox')
      if (!res.ok) {
        error.value = await readError(res, `Failed to load sandbox settings (${res.status})`)
        return null
      }
      settings.value = await res.json()
      return settings.value
    } catch (e: any) {
      error.value = e?.message || 'Failed to load sandbox settings'
      return null
    } finally {
      loading.value = false
    }
  }

  const save = async (update: Partial<Pick<SandboxSettings, 'default_enabled' | 'allowed_domains'>>): Promise<boolean> => {
    saving.value = true
    error.value = null
    try {
      const res = await fetchWithAuth('/api/agent/sandbox', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(update),
      })
      if (!res.ok) {
        error.value = await readError(res, `Failed to save sandbox settings (${res.status})`)
        return false
      }
      settings.value = await res.json()
      return true
    } catch (e: any) {
      error.value = e?.message || 'Failed to save sandbox settings'
      return false
    } finally {
      saving.value = false
    }
  }

  return { settings, loading, saving, error, load, save }
}
