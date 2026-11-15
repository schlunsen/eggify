/**
 * Answers an approval or a question from the notch over a short-lived agent
 * WebSocket: open, authenticate like the main window (API key in the auth
 * message; the session cookie on the upgrade makes it a signed-in user), send
 * one answer, wait for the server's acknowledgement, close.
 *
 * It never sends list_sessions or subscribe_session: the server routes a
 * session's permission prompts to the connections registered for it, and the
 * notch must not take them away from the main window.
 */

export type NotchAnswer =
  | { type: 'permission'; sessionId: string; permissionId: string; approved: boolean }
  | { type: 'question'; sessionId: string; questionId: string; answers: string[] }

export interface AnswerResult { ok: boolean; error?: string; code?: string }

export const ANSWER_TIMEOUT_MS = 8000

export function answerMessage(a: NotchAnswer): Record<string, unknown> {
  return a.type === 'permission'
    ? { type: 'permission_response', session_id: a.sessionId, permission_id: a.permissionId, approved: a.approved }
    : { type: 'user_question_response', session_id: a.sessionId, question_id: a.questionId, answers: a.answers }
}

const ACK: Record<NotchAnswer['type'], string> = {
  permission: 'permission_acknowledged',
  question: 'user_question_acknowledged',
}

/** Plain words for the refusals a user can do something about. */
function errorText(msg: { code?: string; message?: string; error?: string }): string {
  if (msg.code === 'user_required') return 'Sign in to Eggify to answer from the notch.'
  if (msg.code === 'admin_required') return 'Only an admin can answer this one.'
  return msg.message || msg.error || 'The answer was not accepted.'
}

export function sendNotchAnswer(
  answer: NotchAnswer,
  apiKey: string,
  opts: { url?: string; WebSocketImpl?: typeof WebSocket; timeoutMs?: number } = {},
): Promise<AnswerResult> {
  const WS = opts.WebSocketImpl || WebSocket
  const url = opts.url || `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/agent/ws`
  return new Promise((resolve) => {
    let settled = false
    const socket = new WS(url)
    const finish = (result: AnswerResult) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      try { socket.close() } catch { /* already closed */ }
      resolve(result)
    }
    const timer = setTimeout(() => finish({ ok: false, error: 'Eggify did not answer in time.' }), opts.timeoutMs ?? ANSWER_TIMEOUT_MS)
    socket.onopen = () => socket.send(JSON.stringify({ type: 'auth', token: apiKey }))
    socket.onerror = () => finish({ ok: false, error: 'Could not reach Eggify.' })
    socket.onclose = () => finish({ ok: false, error: 'The connection closed before the answer was confirmed.' })
    socket.onmessage = (event: MessageEvent) => {
      let msg: any
      try { msg = JSON.parse(String(event.data)) } catch { return }
      if (msg.type === 'auth_success') socket.send(JSON.stringify(answerMessage(answer)))
      else if (msg.type === 'auth_error') finish({ ok: false, error: 'Eggify refused the connection.', code: 'auth_error' })
      else if (msg.type === ACK[answer.type]) finish({ ok: true })
      else if (msg.type === 'error') finish({ ok: false, error: errorText(msg), code: msg.code })
    }
  })
}
