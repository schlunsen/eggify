import { ref } from 'vue'

/** A pairing code as POST /api/auth/pairing returns it. */
export interface Pairing {
  id: string
  code: string
  base_url: string
  pair_url: string
  expires_at: string
  ttl_seconds: number
  warning?: string
}

export type PairingStatus = 'pending' | 'redeemed' | 'expired' | 'revoked'

/** A signed-in session as GET /api/auth/sessions returns it. */
export interface SignedInSession {
  id: string
  username: string
  device_name: string
  device_model?: string
  created_via: 'password' | 'oauth' | 'setup' | 'pairing' | string
  created_at: string
  last_used_at?: string
  expires_at: string
  current: boolean
}

export interface ApiResult<T> {
  ok: boolean
  data?: T
  error?: string
  code?: string
}

// These endpoints need a signed-in user: the HttpOnly session cookie is what
// counts, so plain same-origin fetch is enough (no API key).
async function call<T>(method: string, path: string, body?: unknown): Promise<ApiResult<T>> {
  try {
    const res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
    let data: any = null
    try { data = await res.json() } catch { /* empty body */ }
    if (!res.ok) {
      return { ok: false, error: data?.error || res.statusText || 'Request failed', code: data?.code }
    }
    return { ok: true, data: data as T }
  } catch (err: any) {
    return { ok: false, error: err?.message || 'Request failed' }
  }
}

export const createPairing = (baseUrl?: string) =>
  call<Pairing>('POST', '/api/auth/pairing', baseUrl ? { base_url: baseUrl } : {})

export const pairingStatus = (id: string) =>
  call<{ id: string, status: PairingStatus, device_name?: string }>('GET', `/api/auth/pairing/${encodeURIComponent(id)}/status`)

export const cancelPairing = (id: string) =>
  call<{ cancelled: boolean }>('DELETE', `/api/auth/pairing/${encodeURIComponent(id)}`)

export const listSessions = () =>
  call<{ sessions: SignedInSession[] }>('GET', '/api/auth/sessions')

export const revokeSession = (id: string) =>
  call<{ revoked: boolean }>('DELETE', `/api/auth/sessions/${encodeURIComponent(id)}`)

// Module-level (the dashboard is a client-only SPA).
const redeemedCount = ref(0)

/** Bumped whenever a phone finishes pairing, so device lists refresh. */
export function usePairingEvents() {
  return redeemedCount
}

/** Seconds left until iso, never negative. */
export function secondsLeft(iso: string, now = Date.now()): number {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return 0
  return Math.max(0, Math.ceil((t - now) / 1000))
}

/** 4:59, 0:07 */
export function formatCountdown(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
