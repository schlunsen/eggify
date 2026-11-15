/**
 * "Requires a signed-in user" refusals.
 *
 * Privileged actions (answering permission requests, YOLO mode, always-allow
 * rules, providers, MCP servers, the terminal, ...) need a signed-in user: the
 * API key alone is not enough, because agents can read it. The server answers
 *   REST:      403 {"error": "<action> requires a signed-in user", "code": "user_required"}
 *   WebSocket: {"type": "error", "code": "user_required", "message": "..."}
 * The browser proves the user with its HttpOnly session cookie, which fetch
 * (credentials: 'same-origin', the default) and the WebSocket upgrade send.
 *
 * With several accounts, privileged actions need an admin: a signed-in
 * non-admin gets the same shapes with code "admin_required".
 */

export const USER_REQUIRED_CODE = 'user_required'
export const ADMIN_REQUIRED_CODE = 'admin_required'
export const USER_REQUIRED_EVENT = 'eggify:user-required'

export function isUserRequiredBody(body: unknown): body is { code: string; error?: string; message?: string } {
  if (!body || typeof body !== 'object') return false
  const code = (body as any).code
  return code === USER_REQUIRED_CODE || code === ADMIN_REQUIRED_CODE
}

/** The toast text for a refusal message from the server. */
export function userRequiredText(serverMessage?: string, code?: string): string {
  if (code === ADMIN_REQUIRED_CODE) {
    const base = (serverMessage || '').trim().replace(/\.$/, '')
    return base ? `Only an admin can do this (${base}).` : 'Only an admin can do this.'
  }
  const base = (serverMessage || 'This action requires a signed-in user').trim().replace(/\.$/, '')
  return `${base}. Sign in to Eggify and try again.`
}

/** Tell the app to show the sign-in (or "admin only") toast. */
export function announceUserRequired(serverMessage?: string, code?: string): void {
  if (typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent(USER_REQUIRED_EVENT, {
    detail: { message: userRequiredText(serverMessage, code), admin: code === ADMIN_REQUIRED_CODE },
  }))
}

/**
 * Announce when a fetch Response is a user_required refusal. Reads a clone,
 * so the caller's response body stays unread. Resolves to whether it was one.
 */
export async function checkResponseForUserRequired(response: Response): Promise<boolean> {
  if (response.status !== 403) return false
  try {
    const body = await response.clone().json()
    if (isUserRequiredBody(body)) {
      announceUserRequired(body.error || body.message, body.code)
      return true
    }
  } catch {
    // not JSON: not ours
  }
  return false
}
