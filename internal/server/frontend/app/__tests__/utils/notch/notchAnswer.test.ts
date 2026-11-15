import { answerMessage, sendNotchAnswer } from '../../../utils/notch/notchAnswer'

/** A WebSocket double that answers like the server. */
function fakeSocket(reply: (msg: any, sock: any) => void) {
  const sent: any[] = []
  class FakeWS {
    onopen: any; onmessage: any; onclose: any; onerror: any
    closed = false
    constructor(public url: string) { setTimeout(() => this.onopen?.(), 0) }
    send(raw: string) { const msg = JSON.parse(raw); sent.push(msg); setTimeout(() => reply(msg, this), 0) }
    emit(msg: any) { this.onmessage?.({ data: JSON.stringify(msg) }) }
    close() { this.closed = true }
  }
  return { WS: FakeWS as unknown as typeof WebSocket, sent }
}

describe('notch answers', () => {
  it('authenticates, sends one answer and closes on the acknowledgement', async () => {
    const { WS, sent } = fakeSocket((msg, sock) => {
      if (msg.type === 'auth') sock.emit({ type: 'auth_success' })
      if (msg.type === 'permission_response') sock.emit({ type: 'permission_acknowledged' })
    })
    const result = await sendNotchAnswer({ type: 'permission', sessionId: 's', permissionId: 'p', approved: true }, 'key', { url: 'ws://x', WebSocketImpl: WS })
    expect(result).toEqual({ ok: true })
    expect(sent).toEqual([
      { type: 'auth', token: 'key' },
      { type: 'permission_response', session_id: 's', permission_id: 'p', approved: true },
    ])
    // Never registers sessions: that would move their approvals to the notch.
    expect(sent.some(m => ['list_sessions', 'subscribe_session', 'load_messages'].includes(m.type))).toBe(false)
  })

  it('reports a refusal in plain words', async () => {
    const { WS } = fakeSocket((msg, sock) => {
      if (msg.type === 'auth') sock.emit({ type: 'auth_success' })
      else sock.emit({ type: 'error', code: 'user_required', message: 'nope' })
    })
    const result = await sendNotchAnswer({ type: 'question', sessionId: 's', questionId: 'q', answers: ['A'] }, 'key', { url: 'ws://x', WebSocketImpl: WS })
    expect(result.ok).toBe(false)
    expect(result.error).toMatch(/sign in/i)
  })

  it('gives up when the server stays silent', async () => {
    const { WS } = fakeSocket(() => {})
    const result = await sendNotchAnswer({ type: 'permission', sessionId: 's', permissionId: 'p', approved: false }, 'key', { url: 'ws://x', WebSocketImpl: WS, timeoutMs: 20 })
    expect(result.ok).toBe(false)
  })

  it('builds the server\'s message shapes', () => {
    expect(answerMessage({ type: 'question', sessionId: 's', questionId: 'q', answers: ['A'] }))
      .toEqual({ type: 'user_question_response', session_id: 's', question_id: 'q', answers: ['A'] })
  })
})
