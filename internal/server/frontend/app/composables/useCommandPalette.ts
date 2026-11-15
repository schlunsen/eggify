import { ref, readonly } from 'vue'

/** Open state of the command palette (⌘K), shared app-wide. */
const open = ref(false)

export function useCommandPalette() {
  return {
    isOpen: readonly(open),
    openPalette: () => { open.value = true },
    closePalette: () => { open.value = false },
    togglePalette: () => { open.value = !open.value }
  }
}
