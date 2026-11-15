import { onBeforeUnmount, ref, toValue, watch, type MaybeRefOrGetter, type WatchSource } from 'vue'

/**
 * A rare, unsynchronised pulse for "working" effects (the session row sheen,
 * the composer edge). Activity (a change of `key` while `active`) lights it at
 * most every `interval` ms, each gap varied by ±`jitter`, and each pulse starts
 * after a random delay of up to `maxDelay` ms, so rows that see activity at the
 * same moment (one poll updates them all) don't flash in step.
 */
export interface PulseGateOptions {
  interval?: number
  jitter?: number
  maxDelay?: number
  duration?: number
  random?: () => number
  now?: () => number
}

export const PULSE_INTERVAL_MS = 10_000
export const PULSE_JITTER = 0.3
export const PULSE_MAX_DELAY_MS = 2_500
export const PULSE_DURATION_MS = 2_200

/** Pure timing: when a pulse may fire, and how long to wait before it starts. */
export function createPulseGate(opts: PulseGateOptions = {}) {
  const interval = opts.interval ?? PULSE_INTERVAL_MS
  const jitter = opts.jitter ?? PULSE_JITTER
  const maxDelay = opts.maxDelay ?? PULSE_MAX_DELAY_MS
  const random = opts.random ?? Math.random
  const now = opts.now ?? Date.now
  let nextAllowed = 0

  return {
    /** Delay (ms) before the pulse should start, or null when it's too soon. */
    request(): number | null {
      const t = now()
      if (t < nextAllowed) return null
      const delay = random() * maxDelay
      nextAllowed = t + delay + interval * (1 - jitter + 2 * jitter * random())
      return delay
    },
  }
}

/** `pulsing` is true for `duration` ms whenever the gate lets an activity through. */
export function useActivityPulse(key: WatchSource<unknown>, active: MaybeRefOrGetter<boolean>, opts: PulseGateOptions = {}) {
  const gate = createPulseGate(opts)
  const duration = opts.duration ?? PULSE_DURATION_MS
  const pulsing = ref(false)
  let startTimer: ReturnType<typeof setTimeout> | null = null
  let endTimer: ReturnType<typeof setTimeout> | null = null

  function clear() {
    if (startTimer) clearTimeout(startTimer)
    if (endTimer) clearTimeout(endTimer)
    startTimer = endTimer = null
    pulsing.value = false
  }

  watch(key, () => {
    if (!toValue(active)) return
    if (startTimer || pulsing.value) return
    const delay = gate.request()
    if (delay === null) return
    startTimer = setTimeout(() => {
      startTimer = null
      pulsing.value = true
      endTimer = setTimeout(() => {
        endTimer = null
        pulsing.value = false
      }, duration)
    }, delay)
  })

  watch(() => toValue(active), (on) => { if (!on) clear() })
  onBeforeUnmount(clear)

  return { pulsing }
}
