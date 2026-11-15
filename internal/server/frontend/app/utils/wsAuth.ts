/**
 * Credentials for browser WebSocket upgrades.
 *
 * A browser WebSocket cannot set headers, so the old code put the API key in
 * `?token=`. URLs end up in proxy logs and, over remote access, in ngrok's
 * edge and traffic inspector, so the key must not travel there. The server
 * reads the HttpOnly `session_token` cookie on the upgrade (the terminal via
 * validateWebSocketToken, the /ws hub via its session validator), which the
 * browser sends by itself on a same-origin upgrade.
 *
 * The API key is only still sent when nobody is signed in (user auth off:
 * API-key-only mode) and the page itself is on a loopback address (the local
 * dashboard or the desktop app), where the URL never leaves the machine.
 */

const LOOPBACK_HOSTS = new Set(['localhost', '127.0.0.1', '::1', '[::1]'])

/** True for localhost / 127.0.0.0/8 / ::1 host names. */
export function isLoopbackHostname(hostname: string): boolean {
  const h = (hostname || '').toLowerCase()
  if (LOOPBACK_HOSTS.has(h)) return true
  if (h.endsWith('.localhost')) return true
  return /^127(?:\.\d{1,3}){3}$/.test(h)
}

/**
 * The `token` query value to put on a WebSocket URL, or null to send none.
 * `signedIn` is true when a user session (cookie) exists.
 */
export function wsQueryToken(
  apiKey: string | null | undefined,
  signedIn: boolean,
  hostname: string = typeof window !== 'undefined' ? window.location.hostname : '',
): string | null {
  if (!apiKey || signedIn) return null
  if (!isLoopbackHostname(hostname)) return null
  return apiKey
}

/** Build `?token=…` (or '') for a WebSocket URL; see wsQueryToken. */
export function wsTokenQuery(
  apiKey: string | null | undefined,
  signedIn: boolean,
  hostname?: string,
): string {
  const tok = wsQueryToken(apiKey, signedIn, hostname)
  return tok ? `token=${encodeURIComponent(tok)}` : ''
}
