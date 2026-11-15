/**
 * Reconnect pacing for the agent WebSocket.
 *
 * A fixed 5 s retry turns a server that closes the socket right after the
 * handshake (or a page that keeps failing to load its key) into a steady stream
 * of reconnects, each re-listing sessions and reloading messages. This backs
 * off exponentially with jitter, so many tabs do not reconnect in lockstep,
 * and only forgets the failures once a connection has stayed up for a while,
 * so a socket that flaps (open, auth, close) cannot reconnect faster than the
 * backoff allows.
 */

export interface ReconnectPolicy {
  /** First delay (ms). */
  baseMs: number
  /** Upper bound for any delay (ms). */
  maxMs: number
  /** A connection that stayed up this long resets the backoff (ms). */
  stableMs: number
  random: () => number
  now: () => number
}

export const DEFAULT_RECONNECT_POLICY: ReconnectPolicy = {
  baseMs: 1_000,
  maxMs: 30_000,
  stableMs: 30_000,
  random: Math.random,
  now: () => Date.now(),
}

/**
 * Delay before reconnect attempt `attempt` (0-based): exponential, capped at
 * maxMs, with "equal jitter" (half fixed, half random), so it is never below
 * half the exponential step.
 */
export function reconnectDelay(attempt: number, policy: Pick<ReconnectPolicy, 'baseMs' | 'maxMs' | 'random'> = DEFAULT_RECONNECT_POLICY): number {
  const exp = Math.min(policy.maxMs, policy.baseMs * 2 ** Math.max(0, Math.min(attempt, 20)))
  return Math.round(exp / 2 + policy.random() * (exp / 2))
}

export interface ReconnectBackoff {
  /** The connection authenticated: remember when, to judge its stability on close. */
  connected(): void
  /** The connection closed or failed to open: the delay to wait before the next attempt. */
  nextDelay(): number
  /** Number of consecutive failed/short-lived attempts so far. */
  attempts(): number
  reset(): void
}

export function createReconnectBackoff(overrides: Partial<ReconnectPolicy> = {}): ReconnectBackoff {
  const policy = { ...DEFAULT_RECONNECT_POLICY, ...overrides }
  let attempt = 0
  let connectedAt: number | null = null

  return {
    connected() {
      connectedAt = policy.now()
    },
    nextDelay() {
      if (connectedAt !== null && policy.now() - connectedAt >= policy.stableMs) attempt = 0
      connectedAt = null
      const delay = reconnectDelay(attempt, policy)
      attempt++
      return delay
    },
    attempts: () => attempt,
    reset() {
      attempt = 0
      connectedAt = null
    },
  }
}
