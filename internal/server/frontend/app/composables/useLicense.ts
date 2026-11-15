import { ref } from 'vue'
import { licenseErrorMessage, type LicenseErrorBody, type LicenseStatus } from '~/utils/license'

/** Result of an activate / deactivate / check call. */
export interface LicenseActionResult {
  ok: boolean
  status: LicenseStatus | null
  error?: string
  code?: string
  activations?: LicenseErrorBody['activations']
}

/**
 * The install's license: GET /api/license, activate, deactivate, check now.
 * Shared state, so the activation page and Settings → License agree.
 */
export const useLicense = () => {
  const status = useState<LicenseStatus | null>('license-status', () => null)
  const loading = ref(false)
  const busy = ref(false)
  const { fetchWithAuth } = useAuthenticatedFetch()

  const refresh = async (): Promise<LicenseStatus | null> => {
    loading.value = true
    try {
      // Plain fetch: fetchWithAuth sends a 401 to /login, which would race
      // the activation page's own sign-in handling.
      const res = await fetch('/api/license', { credentials: 'same-origin' })
      if (res.ok) status.value = await res.json() as LicenseStatus
      return status.value
    } catch {
      return status.value
    } finally {
      loading.value = false
    }
  }

  const call = async (path: string, body?: Record<string, unknown>): Promise<LicenseActionResult> => {
    busy.value = true
    try {
      const res = await fetchWithAuth(path, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body ?? {}),
      })
      let data: any = null
      try {
        data = await res.json()
      } catch {
        data = null
      }
      if (res.ok) {
        status.value = data as LicenseStatus
        return { ok: true, status: status.value }
      }
      if (data?.status) status.value = data.status as LicenseStatus
      return {
        ok: false,
        status: status.value,
        error: licenseErrorMessage(data, res.status),
        code: data?.code,
        activations: data?.activations,
      }
    } catch {
      return { ok: false, status: status.value, error: "Couldn't reach Eggify. Try again." }
    } finally {
      busy.value = false
    }
  }

  const activate = (serial: string, machine?: string) =>
    call('/api/license/activate', machine ? { serial, machine } : { serial })
  const deactivate = () => call('/api/license/deactivate')
  const checkNow = () => call('/api/license/check')

  return { status, loading, busy, refresh, activate, deactivate, checkNow }
}
