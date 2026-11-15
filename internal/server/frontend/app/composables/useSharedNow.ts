import { getCurrentScope, onScopeDispose, readonly, ref, type Ref } from 'vue'

interface Clock {
  now: Ref<number>
  users: number
  timer?: ReturnType<typeof setInterval>
}

// One timer per interval, shared by every subscriber and stopped when the
// last one goes away.
const clocks = new Map<number, Clock>()

/**
 * A `Date.now()` that updates every `intervalMs`. Read it only where the time
 * is displayed (e.g. in a tiny component), so the tick re-renders that text
 * instead of a whole view.
 */
export function useSharedNow(intervalMs = 1000): Readonly<Ref<number>> {
  let clock = clocks.get(intervalMs)
  if (!clock) {
    clock = { now: ref(Date.now()), users: 0 }
    clocks.set(intervalMs, clock)
  }
  const current = clock
  current.users++
  if (!current.timer) {
    current.now.value = Date.now()
    current.timer = setInterval(() => {
      // Nothing to show in a background tab; catch up on the next visible tick
      if (typeof document !== 'undefined' && document.hidden) return
      current.now.value = Date.now()
    }, intervalMs)
  }
  if (getCurrentScope()) {
    onScopeDispose(() => {
      current.users--
      if (current.users <= 0 && current.timer) {
        clearInterval(current.timer)
        current.timer = undefined
        current.users = 0
      }
    })
  }
  return readonly(current.now)
}
