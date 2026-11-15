/**
 * useZenViz — Manages zen mode visualization scene selection
 *
 * Four scenes, each owning the whole screen:
 * - circus: a cut-out paper collage with the egg gentleman (DOM/CSS, zen/sceneCircus) — the default
 * - waves:  synthwave grid on a rolling surface (WebGL, useZenAnimation)
 * - galaxy: the session as a living spiral galaxy (WebGPU/TSL, zen/sceneGalaxy)
 * - ink:    the session paints itself in luminous ink (WebGPU/TSL fluid, zen/sceneInk)
 */

import { ref } from 'vue'

export type ZenVizMode = 'circus' | 'waves' | 'galaxy' | 'ink'

export interface ZenVizInfo {
  id: ZenVizMode
  label: string
  icon: string // SVG path data
  description: string
  color: string // Accent color for the mode
}

/** Scene shown to users who have not picked one (or stored an unknown id). */
export const DEFAULT_ZEN_VIZ: ZenVizMode = 'circus'

export const ZEN_VIZ_MODES: ZenVizInfo[] = [
  {
    id: 'circus',
    label: 'Circus',
    // An egg in a bowler hat
    icon: 'M12 21c-3.6 0-6-2.6-6-6.4C6 10.8 8.6 7 12 7s6 3.8 6 7.6c0 3.8-2.4 6.4-6 6.4zM8 6.6h8M9.6 6.6c0-2 1-3.3 2.4-3.3s2.4 1.3 2.4 3.3',
    description: 'And now for something completely different',
    color: '#e8458b'
  },
  {
    id: 'waves',
    label: 'Waves',
    icon: 'M2 12c1.5-3 3-4.5 5-4.5s3.5 3 5 4.5 3 1.5 5 1.5 3.5-3 5-4.5',
    description: 'Synthwave grid that rides the session',
    color: '#8B5CF6'
  },
  {
    id: 'galaxy',
    label: 'Galaxy',
    icon: 'M12 12m-1.6 0a1.6 1.6 0 1 0 3.2 0a1.6 1.6 0 1 0-3.2 0M12 7.2c2.9 0 4.8 2 4.8 4.8S14.4 17 11.6 17 6.2 14.3 6.2 11s2.6-8 6.8-8 8 3.3 8 8.2M12 21c-4.8 0-9-3.4-9-8.4',
    description: 'Your files as stars in a living galaxy',
    color: '#FBBF24'
  },
  {
    id: 'ink',
    label: 'Ink',
    icon: 'M12 3c-3 4.4-5.6 7.6-5.6 11.1a5.6 5.6 0 0 0 11.2 0C17.6 10.6 15 7.4 12 3zM9.4 15.2c.3 1.3 1.3 2.2 2.6 2.4',
    description: 'The session paints itself in living ink',
    color: '#F472B6'
  }
]

// Singleton state — persists across component mount/unmount
const activeViz = ref<ZenVizMode>(DEFAULT_ZEN_VIZ)
const previousViz = ref<ZenVizMode>(DEFAULT_ZEN_VIZ)
const isTransitioning = ref(false)

// Persist to localStorage
const STORAGE_KEY = 'zen-viz-mode'

export function loadZenVizFromStorage(): ZenVizMode {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored && ZEN_VIZ_MODES.some(m => m.id === stored)) {
      return stored as ZenVizMode
    }
  } catch {}
  return DEFAULT_ZEN_VIZ
}

// Initialize from storage
activeViz.value = loadZenVizFromStorage()

export function useZenViz() {
  /** `force` skips the transition guard (used for the GPU fallback, which
   *  must not be dropped just because a switch is still animating). */
  function setViz(mode: ZenVizMode, force = false) {
    if (mode === activeViz.value) return
    if (isTransitioning.value && !force) return

    previousViz.value = activeViz.value
    isTransitioning.value = true
    activeViz.value = mode

    try {
      localStorage.setItem(STORAGE_KEY, mode)
    } catch {}

    // Transition duration — allow scene swap
    setTimeout(() => {
      isTransitioning.value = false
    }, 600)
  }

  function nextViz() {
    const currentIdx = ZEN_VIZ_MODES.findIndex(m => m.id === activeViz.value)
    const nextIdx = (currentIdx + 1) % ZEN_VIZ_MODES.length
    setViz(ZEN_VIZ_MODES[nextIdx]!.id)
  }

  function prevViz() {
    const currentIdx = ZEN_VIZ_MODES.findIndex(m => m.id === activeViz.value)
    const prevIdx = (currentIdx - 1 + ZEN_VIZ_MODES.length) % ZEN_VIZ_MODES.length
    setViz(ZEN_VIZ_MODES[prevIdx]!.id)
  }

  function getVizInfo(mode?: ZenVizMode): ZenVizInfo {
    return ZEN_VIZ_MODES.find(m => m.id === (mode || activeViz.value)) || ZEN_VIZ_MODES[0]!
  }

  return {
    activeViz,
    previousViz,
    isTransitioning,
    setViz,
    nextViz,
    prevViz,
    getVizInfo,
    modes: ZEN_VIZ_MODES
  }
}
