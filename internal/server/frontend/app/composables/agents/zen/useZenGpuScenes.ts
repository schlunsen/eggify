/**
 * Vue glue for the zen scenes: switches between the GPU scenes (Galaxy, Ink),
 * the Waves scene (WebGL) and the Circus scene (plain DOM, no GPU), and
 * forwards session events to whichever is visible.
 */
import { watch, onUnmounted, ref, computed, type Ref } from 'vue'
import type { ZenVizMode } from '~/composables/agents/useZenViz'
import {
  registerZenGpuScene,
  showZenGpuScene,
  hideZenGpu,
  withActiveZenScene,
  setZenGpuStatus,
  setZenGpuProjectColor,
  resetZenGpuScenes,
  onZenGpuFailure,
} from './gpuHost'
import { createGalaxyScene } from './sceneGalaxy'
import { createInkScene } from './sceneInk'
import { toolCategory, type ZenToolEvent } from './types'
import { getCircusScene } from './sceneCircus'
import { resolveCircusBackdrop, useCircusBackdropPref } from './useCircusBackdrop'
import { backdropInfo, type CircusBackdropId } from './circusBackdrops'

let registered = false
function ensureRegistered() {
  if (registered) return
  registerZenGpuScene('galaxy', createGalaxyScene)
  registerZenGpuScene('ink', createInkScene)
  registered = true
}

export const GPU_SCENES: ZenVizMode[] = ['galaxy', 'ink']
/** Scenes drawn without WebGL/WebGPU */
export const DOM_SCENES: ZenVizMode[] = ['circus']

interface Options {
  container: Ref<HTMLElement | null>
  activeViz: Ref<ZenVizMode>
  sessionStatus: Ref<string>
  projectColor: Ref<string>
  /** The session on stage, and (for spreading backdrops) every session known right now */
  sessionId?: Ref<string>
  openSessionIds?: () => string[]
  /** Show the Waves scene (attach its canvas) */
  showWaves: () => Promise<void> | void
  /** Hide the Waves scene (detach its canvas, stop its loop) */
  hideWaves: () => void
  /** Called with the ids of subagents that are running when a GPU scene appears */
  runningAgentIds: () => string[]
  /** Fall back to a scene that needs no GPU if the GPU renderer cannot start */
  onUnavailable: () => void
}

export function useZenGpuScenes(opts: Options) {
  ensureRegistered()
  const circus = getCircusScene()
  const { pref } = useCircusBackdropPref()
  const backdropId = ref<CircusBackdropId>('meadow')
  const backdrop = computed(() => backdropInfo(backdropId.value))
  /** The container this instance mounted the (shared) circus scene into */
  let circusEl: HTMLElement | null = null
  let unmounted = false
  let switchToken = 0

  const isCurrent = (token: number) => () => token === switchToken && !unmounted

  async function apply(mode: ZenVizMode) {
    const token = ++switchToken
    const el = opts.container.value
    if (!el || unmounted) return
    if (DOM_SCENES.includes(mode)) {
      hideZenGpu()
      opts.hideWaves()
      circus.mount(el)
      circusEl = el
      circus.agentSync(opts.runningAgentIds())
      return
    }
    circus.unmount(circusEl)
    circusEl = null
    if (!GPU_SCENES.includes(mode)) {
      hideZenGpu()
      await opts.showWaves()
      // Switched again while Waves was loading: take it down again
      if (!isCurrent(token)()) opts.hideWaves()
      return
    }
    opts.hideWaves()
    try {
      const shown = await showZenGpuScene(mode, el, isCurrent(token))
      if (!shown || !isCurrent(token)()) return
      for (const id of opts.runningAgentIds()) withActiveZenScene(s => s.agentSpawn(id))
    } catch (err) {
      console.error(`[zen] ${mode} scene unavailable, falling back:`, err)
      if (token === switchToken && !unmounted) opts.onUnavailable()
    }
  }

  watch(opts.activeViz, (mode) => { apply(mode) })
  if (opts.sessionId) {
    watch(opts.sessionId, (id) => { circus.setSession(id) }, { immediate: true })
    watch([opts.sessionId, pref], () => {
      backdropId.value = resolveCircusBackdrop(opts.sessionId!.value, opts.openSessionIds?.() ?? [])
      circus.setBackdrop(backdropId.value)
    }, { immediate: true })
  }
  onZenGpuFailure((reason) => {
    console.error(`[zen] GPU scene stopped (${reason}), falling back`)
    if (!unmounted) opts.onUnavailable()
  })
  watch(opts.sessionStatus, (s) => { setZenGpuStatus(s); circus.setStatus(s) }, { immediate: true })
  watch(opts.projectColor, (c) => { setZenGpuProjectColor(c); circus.setProjectColor(c) }, { immediate: true })

  /** Run `fn` against the visible scene (circus or GPU). */
  function each(fn: (s: Pick<typeof circus, 'toolEvent' | 'sendPulse' | 'agentSpawn' | 'agentComplete'>) => void) {
    if (circus.mounted) fn(circus)
    else withActiveZenScene(fn)
  }

  onUnmounted(() => {
    unmounted = true
    switchToken++
    onZenGpuFailure(null)
    hideZenGpu()
    circus.unmount(circusEl)
    circusEl = null
  })

  return {
    /** The Circus backdrop in use (name, dark or light) */
    backdrop,
    /** Show whichever scene is selected (call once on mount). */
    start: () => apply(opts.activeViz.value),
    isGpuScene: (mode: ZenVizMode) => GPU_SCENES.includes(mode),
    toolEvent(tool: string, filePath: string, label: string, isError = false) {
      const e: ZenToolEvent = { tool, category: toolCategory(tool, isError), filePath, label, isError }
      each(s => s.toolEvent(e))
    },
    sendPulse: () => each(s => s.sendPulse()),
    /** Switching session: forget the previous session's stars, wells, comets, walkers… */
    resetScenes: (runningIds: string[] = []) => {
      resetZenGpuScenes(); circus.reset()
      // The new session's subagents are not "new events": show the ones already running
      if (circus.mounted) circus.agentSync(runningIds)
      else withActiveZenScene(s => { for (const id of runningIds) s.agentSpawn(id) })
    },
    /** Make the stage match the subagents that are running now (idempotent). */
    syncAgents: (runningIds: string[]) => {
      if (circus.mounted) circus.agentSync(runningIds)
      else withActiveZenScene(s => { for (const id of runningIds) s.agentSpawn(id) })
    },
    agentSpawn: (id: string) => each(s => s.agentSpawn(id)),
    agentComplete: (id: string) => each(s => s.agentComplete(id)),
  }
}
