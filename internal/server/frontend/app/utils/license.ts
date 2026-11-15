/**
 * Licensing (docs/licensing-protocol.md, docs/adr/013-licensing.md).
 *
 * A licensed build refuses every API request and WebSocket while it isn't
 * activated:
 *   REST:      402 {"code": "license_required", "error": "Activate Eggify with your serial number"}
 *   WebSocket: {"type": "error", "code": "license_required", "message": "..."} (or the upgrade gets 402)
 * The app then shows /activate. Serials are checked here before sending, with
 * the same normalisation and check character as the server.
 */

export const LICENSE_REQUIRED_CODE = 'license_required'
export const ACTIVATE_PATH = '/activate'
export const BUY_URL = 'https://eggify.xyz'

/** Crockford base32 (value = index). */
export const SERIAL_ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'
export const SERIAL_LENGTH = 20

export type LicenseState = 'unlicensed' | 'active' | 'expired' | 'invalid'

export interface LicenseStatus {
  required: boolean
  state: LicenseState
  serial_hint?: string
  expires_at?: string
  machine?: string
  plan?: string
  features?: string[]
  seats?: number
  seats_used?: number
  last_checked?: string
  error?: string
  renew_error?: string
}

export interface LicenseActivation {
  machine: string
  activated_at?: number
  last_seen_at?: number
}

/** An error answer of /api/license/activate|deactivate|check. */
export interface LicenseErrorBody {
  error?: string
  code?: string
  activations?: LicenseActivation[]
  status?: LicenseStatus
}

/** The check character of a 19-character body: ALPHABET[(Σ i·value) mod 31]. */
export function serialCheckChar(body: string): string {
  let sum = 0
  for (let i = 0; i < body.length; i++) {
    sum += (i + 1) * SERIAL_ALPHABET.indexOf(body[i]!)
  }
  return SERIAL_ALPHABET[sum % 31]!
}

/**
 * The characters of a serial as typed: upper-case; spaces, "-" and "_"
 * dropped; a leading "EGG" dropped unless exactly 20 characters are left; O→0, I/L→1.
 * Not validated.
 */
export function serialChars(input: string): string {
  let s = input.toUpperCase().replace(/[\s_-]/g, '')
  if (s.startsWith('EGG') && s.length !== SERIAL_LENGTH) s = s.slice(3)
  return s.replace(/O/g, '0').replace(/[IL]/g, '1')
}

/** The normalised 20 characters, or null when the serial isn't valid. */
export function normalizeSerial(input: string): string | null {
  const s = serialChars(input)
  if (s.length !== SERIAL_LENGTH) return null
  for (const c of s) {
    if (!SERIAL_ALPHABET.includes(c)) return null
  }
  return serialCheckChar(s.slice(0, SERIAL_LENGTH - 1)) === s[SERIAL_LENGTH - 1] ? s : null
}

/**
 * Formats input while typing: "egg7h3k9q2" → "EGG-7H3K9-Q2". Characters
 * outside the alphabet are kept (so the message can point at them), extra
 * ones beyond 20 too.
 */
export function formatSerialInput(input: string): string {
  // Typing the "EGG" prefix itself: keep it as typed until a serial character
  // follows ("EGG-" once the dash is typed, so backspace can remove it).
  const raw = input.toUpperCase().replace(/[\s_-]/g, '')
  if (raw === 'E' || raw === 'EG') return raw
  if (raw === 'EGG') return input.includes('-') ? 'EGG-' : 'EGG'
  // A body that itself starts with EGG, pasted or typed in one go without the
  // "EGG-" prefix (17-20 contiguous characters): the prefix drop would leave
  // too little to ever be a serial, so keep it as the body. A prefixed paste
  // is 23 characters, so it is not affected. serialChars/normalizeSerial keep
  // the protocol rule unchanged.
  const contiguous = !/[\s_-]/.test(input.trim())
  const s = contiguous && raw.startsWith('EGG') && raw.length >= SERIAL_LENGTH - 3 && raw.length <= SERIAL_LENGTH
    ? raw.replace(/O/g, '0').replace(/[IL]/g, '1')
    : serialChars(input)
  if (!s) return ''
  const groups: string[] = []
  for (let i = 0; i < s.length; i += 5) groups.push(s.slice(i, i + 5))
  return 'EGG-' + groups.join('-')
}

const nonSep = (v: string) => v.replace(/[\s_-]/g, '').length

/**
 * Where the caret goes after `raw` was reformatted to `formatted`: after the
 * same serial characters as before (the dashes and the added "EGG-" prefix
 * don't count).
 */
export function caretAfterFormat(raw: string, rawCaret: number, formatted: string): number {
  if (rawCaret >= raw.length) return formatted.length
  if (rawCaret <= 0) return 0
  const before = nonSep(raw.slice(0, rawCaret))
  if (!formatted.startsWith('EGG-')) return Math.min(before, formatted.length)
  const prefixDropped = nonSep(raw) - (nonSep(formatted) - 3) === 3
  const body = Math.max(0, before - (prefixDropped ? 3 : 0))
  if (body === 0) return prefixDropped && before < 3 ? before : 4
  return Math.min(formatted.length, 4 + body + Math.floor((body - 1) / 5))
}

/** Why a typed serial isn't valid yet, in plain words ('' when it is valid or empty). */
export function serialProblem(input: string): string {
  const s = serialChars(input)
  if (!s) return ''
  const bad = [...new Set([...s].filter(c => !SERIAL_ALPHABET.includes(c)))]
  if (bad.length) {
    return `Serial numbers don't use ${bad.map(c => `"${c}"`).join(', ')}. Check what you typed.`
  }
  if (s.length < SERIAL_LENGTH) {
    const left = SERIAL_LENGTH - s.length
    return `${left} more character${left === 1 ? '' : 's'} to go.`
  }
  if (s.length > SERIAL_LENGTH) {
    return 'That is longer than a serial number. Check for extra characters.'
  }
  if (!normalizeSerial(input)) {
    return "That serial number has a typo: one of its characters isn't right. Check it against your email."
  }
  return ''
}

export function isLicenseRequiredBody(body: unknown): boolean {
  return !!body && typeof body === 'object' && (body as any).code === LICENSE_REQUIRED_CODE
}

/** The message for a failed activation; seats_full names the machines. */
export function licenseErrorMessage(body: LicenseErrorBody | null | undefined, status?: number): string {
  if (body?.code === 'seats_full') {
    const names = (body.activations || []).map(a => a.machine).filter(Boolean)
    const list = names.length ? `: ${names.join(', ')}` : ''
    return `This serial number is already active on the most computers it allows${list}. ` +
      'Deactivate it on one of them (Settings → License on that Mac), then try again.'
  }
  if (body?.error) return body.error
  if (status === 403) return 'Only the signed-in admin of this Eggify can change its license.'
  return "Couldn't reach Eggify. Try again."
}

/** "12 Nov 2026" */
export function formatLicenseDate(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })
}

/** The parts of /api/auth/status that decide who may see the activation form. */
export interface ActivationAuthState {
  enabled?: boolean
  authenticated?: boolean
  needs_setup?: boolean
  mfa_required?: boolean
  mfa_verified?: boolean
}

/**
 * Who may see the activation form: anyone on a fresh install (no account yet,
 * activation comes before /setup and needs no session) or with auth off; else
 * a signed-in user whose MFA is complete. Everyone else signs in first.
 */
export function activationNeedsLogin(a: ActivationAuthState): boolean {
  if (a.needs_setup || !a.enabled) return false
  if (!a.authenticated) return true
  return !!a.mfa_required && !a.mfa_verified
}

/** Whether a status means the app can't be used until it is activated. */
export function licenseBlocks(status: LicenseStatus | null | undefined): boolean {
  return !!status && status.required && status.state !== 'active'
}

let lastRedirect = 0

/**
 * Open the activation page (at most once a second, never when already there).
 * Uses the router when Nuxt is up; falls back to a full navigation.
 */
export function goToActivation(): void {
  if (typeof window === 'undefined') return
  if (window.location.pathname === ACTIVATE_PATH) return
  const now = Date.now()
  if (now - lastRedirect < 1000) return
  lastRedirect = now
  const fallback = () => window.location.assign(ACTIVATE_PATH)
  try {
    Promise.resolve(navigateTo(ACTIVATE_PATH)).catch(fallback)
  } catch {
    fallback()
  }
}

/**
 * Open the activation page when a fetch Response is a license_required
 * refusal. Reads a clone, so the caller's body stays unread.
 */
export async function checkResponseForLicenseRequired(response: Response): Promise<boolean> {
  if (response.status !== 402) return false
  try {
    const body = await response.clone().json()
    if (isLicenseRequiredBody(body)) {
      goToActivation()
      return true
    }
  } catch {
    // not JSON: not ours
  }
  return false
}

let lastProbe = 0

/**
 * After a WebSocket closed: a refused upgrade (402) can't be read in the
 * browser, so ask /api/license (at most every 30 s) and open the activation
 * page when the app isn't activated.
 */
export async function probeLicenseAfterSocketClose(fetcher: typeof fetch = fetch): Promise<boolean> {
  const now = Date.now()
  if (now - lastProbe < 30_000) return false
  lastProbe = now
  try {
    const res = await fetcher('/api/license')
    if (res.status === 402) {
      goToActivation()
      return true
    }
    if (!res.ok) return false
    const status = await res.json() as LicenseStatus
    if (licenseBlocks(status)) {
      goToActivation()
      return true
    }
  } catch {
    // offline: the socket's own reconnect handles it
  }
  return false
}

/**
 * A license_required refusal was just seen (a frame or a 402): the next
 * post-close probe must not wait out the throttle.
 */
export function noteLicenseRefusal(): void {
  lastProbe = 0
}

/** Test hook: forget the throttles. */
export function _resetLicenseThrottles(): void {
  lastRedirect = 0
  lastProbe = 0
}
