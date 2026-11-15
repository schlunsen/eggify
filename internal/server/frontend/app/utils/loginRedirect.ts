/**
 * Where to go after signing in, and whether a 401 should send the page to
 * the login screen.
 *
 * A request refused while the login page was open (the perf reporter posts
 * every 30 s, the desktop update toast polls) used to push
 * `/login?redirect=/login?redirect=/agents`. After a successful sign-in the
 * page then pushed `/login?redirect=/agents`, the same page component, whose
 * onMounted never ran again: the user stayed on the login screen until a
 * full reload (e.g. switching machines in the desktop app).
 */

const AUTH_PAGE_RE = /^\/(login|setup|oauth-callback|access-denied)(?=[/?#]|$)/

/** True for the pages a signed-out user may be on. */
export function isAuthPage(path: string): boolean {
  return AUTH_PAGE_RE.test(path || '')
}

/**
 * The in-app path to open after signing in: a same-origin path that is not
 * itself an auth page. A login page nested in `redirect` is unwrapped;
 * anything else (absolute or protocol-relative URLs, backslashes, control
 * characters) falls back to `/`.
 */
export function loginRedirectTarget(raw: unknown): string {
  let value: unknown = raw
  for (let depth = 0; depth < 5; depth++) {
    if (Array.isArray(value)) value = value[0]
    if (typeof value !== 'string' || !value.startsWith('/')) return '/'
    // `//host` and `/\host` leave the origin; control characters have no place in a path
    if (value.startsWith('//') || value.includes('\\') || /[\x00-\x1f\x7f]/.test(value)) return '/'
    if (!isAuthPage(value)) return value
    if (!value.startsWith('/login')) return '/'
    const query = value.split('#')[0]!.split('?')[1] ?? ''
    value = new URLSearchParams(query).get('redirect')
  }
  return '/'
}
