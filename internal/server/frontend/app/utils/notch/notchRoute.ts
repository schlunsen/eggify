/**
 * The Notch Theatre page (/notch) runs in its own small desktop window. It
 * must stay lean and, above all, must not open the agent WebSocket: the app's
 * socket sends list_sessions, which registers every session with it, so the
 * server would route their approvals to the notch instead of the main window.
 * app.vue skips the socket and the global overlays on this path.
 */
export const NOTCH_PATH = '/notch'

export function isNotchPath(path: string | undefined | null): boolean {
  return path === NOTCH_PATH || path === `${NOTCH_PATH}/`
}
