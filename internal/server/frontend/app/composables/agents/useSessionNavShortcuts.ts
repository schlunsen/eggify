import { computed } from 'vue'
import type { Ref } from 'vue'
import { useShortcut } from '~/composables/useKeyboardShortcuts'

/**
 * ⌘1…⌘9, ⌘[ / ⌘] and ⌥⌘↑ / ⌥⌘↓ (Alt+↑/↓ on Windows/Linux) on the agents page:
 * jump to / step through sessions in the sidebar's order. The page passes the
 * sidebar's visible order (useSessionGroups: Needs you, Working, Today,
 * Earlier, collapsed groups left out); duplicate ids are dropped here too.
 */
export function useSessionNavShortcuts(
  sessions: Ref<Array<{ id: string }> | undefined | null>,
  activeId: Ref<string | null | undefined>,
  select: (id: string) => void
) {
  const order = computed(() => {
    const seen = new Set<string>()
    return (sessions.value || []).filter(s => s?.id && !seen.has(s.id) && !!seen.add(s.id))
  })

  function selectIndex(index: number) {
    const session = order.value[index]
    if (session && session.id !== activeId.value) select(session.id)
  }

  function step(delta: number) {
    const list = order.value
    if (!list.length) return
    const current = list.findIndex(s => s.id === activeId.value)
    const next = current < 0 ? (delta > 0 ? 0 : list.length - 1) : (current + delta + list.length) % list.length
    selectIndex(next)
  }

  useShortcut('session.jump', ({ digit }) => { if (digit) selectIndex(digit - 1) })
  useShortcut('session.prev', () => step(-1))
  useShortcut('session.next', () => step(1))
  useShortcut('session.up', () => step(-1))
  useShortcut('session.down', () => step(1))

  return { order, selectIndex, step }
}
