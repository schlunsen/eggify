import { computed, onMounted, onUnmounted, ref } from 'vue'
import { desktopMachines, onDesktopMachines, type DesktopMachines } from '~/utils/desktop'

/**
 * The desktop app's machines (this Mac + paired Macs) and the one the window
 * shows. Loaded once on mount (the page reloads on every switch) and kept
 * current by the app's machines event. Empty on the web and in Tauri.
 */
export function useDesktopMachines() {
  const state = ref<DesktopMachines | null>(null)
  let off: (() => void) | null = null
  let disposed = false

  onMounted(async () => {
    const m = await desktopMachines()
    if (disposed) return
    if (m) state.value = m
    try {
      const unsub = await onDesktopMachines((next) => { state.value = next })
      if (disposed) unsub()
      else off = unsub
    } catch {
      // older app or no runtime: the list read on mount is enough
    }
  })
  onUnmounted(() => {
    disposed = true
    off?.()
  })

  const machines = computed(() => state.value?.machines ?? [])
  const current = computed(() => {
    const list = machines.value
    return list.find(m => m.id === (state.value?.current ?? '')) ?? list.find(m => m.here) ?? null
  })
  return { machines, current, state }
}
