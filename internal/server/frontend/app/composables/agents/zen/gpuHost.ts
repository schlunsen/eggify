/**
 * GPU host for the zen scenes that run on three.js' WebGPURenderer
 * (Galaxy, Ink). One renderer, one canvas, one frame loop — shared by all
 * scenes and kept across mount/unmount, like the Waves singleton.
 *
 * WebGPURenderer picks WebGPU when the browser has it and falls back to
 * WebGL2 otherwise, so the same TSL scene code runs everywhere. Scenes can
 * check `ctx.webgpu` to scale particle counts.
 */
import { getMicLevel, isMicActive } from '~/composables/useMicLevel'
import type { FrameInfo, SceneContext, ZenGpuScene } from './types'
import { registerFlyTarget } from './flyState'

const FLY_OWNER = Symbol('zen-gpu')

type SceneFactory = () => ZenGpuScene

const factories = new Map<string, SceneFactory>()
const scenes = new Map<string, ZenGpuScene>()
const building = new Map<string, Promise<ZenGpuScene>>()

let ctx: SceneContext | null = null
let creating: Promise<SceneContext> | null = null
let active: ZenGpuScene | null = null
let container: HTMLElement | null = null
let resizeObserver: ResizeObserver | null = null
let raf: number | null = null
let lastTime = 0
let elapsed = 0
let voiceOn = 0
let failed = false
let visibilityHooked = false
let pendingStatus = 'idle'
let pendingColor = ''
let failureHandler: ((reason: string) => void) | null = null

/** Called when the GPU scene stops working (frame error, device lost). */
export function onZenGpuFailure(handler: ((reason: string) => void) | null) {
  failureHandler = handler
}

function fail(reason: string) {
  failed = true
  hideZenGpu()
  failureHandler?.(reason)
}

export function registerZenGpuScene(id: string, factory: SceneFactory) {
  factories.set(id, factory)
}

async function ensureContext(): Promise<SceneContext> {
  if (ctx) return ctx
  if (creating) return creating
  creating = (async () => {
    const [THREE, TSL] = await Promise.all([import('three/webgpu'), import('three/tsl')])
    const renderer = new THREE.WebGPURenderer({ antialias: false, alpha: false, powerPreference: 'high-performance' })
    await renderer.init()
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
    renderer.setClearColor(0x030208, 1)
    renderer.toneMapping = THREE.ACESFilmicToneMapping
    renderer.toneMappingExposure = 1.0

    const canvas = renderer.domElement as HTMLCanvasElement
    Object.assign(canvas.style, {
      position: 'absolute', inset: '0', width: '100%', height: '100%',
      zIndex: '0', opacity: '0', transition: 'opacity 0.8s ease', display: 'block',
    })

    // GPU reset / driver crash: fall back instead of freezing on a dead canvas
    ;(renderer as any).onDeviceLost = (info: any) => {
      console.error('[zen-gpu] device lost:', info?.message || info)
      fail('GPU device lost')
    }

    const webgpu = !!(renderer as any).backend?.isWebGPUBackend
    const reducedMotion = !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    console.log(`[zen-gpu] renderer ready (${webgpu ? 'WebGPU' : 'WebGL2 fallback'})`)
    ctx = { THREE, TSL, renderer, webgpu, reducedMotion }
    return ctx
  })()
  try {
    return await creating
  } catch (err) {
    creating = null
    failed = true
    throw err
  }
}

async function getScene(id: string): Promise<ZenGpuScene> {
  const existing = scenes.get(id)
  if (existing) return existing
  const inFlight = building.get(id)
  if (inFlight) return inFlight
  const factory = factories.get(id)
  if (!factory) throw new Error(`Unknown zen scene "${id}"`)
  const p = (async () => {
    const c = await ensureContext()
    const scene = factory()
    await scene.build(c)
    scene.setStatus(pendingStatus)
    if (pendingColor) scene.setProjectColor(pendingColor)
    scenes.set(id, scene)
    return scene
  })()
  building.set(id, p)
  try {
    return await p
  } finally {
    building.delete(id)
  }
}

function handleResize() {
  if (!ctx || !container || !active) return
  const w = Math.max(1, container.clientWidth)
  const h = Math.max(1, container.clientHeight)
  ctx.renderer.setSize(w, h, false)
  active.resize(w, h, ctx.renderer.getPixelRatio())
}

function loop(now: number) {
  raf = requestAnimationFrame(loop)
  if (!ctx || !active) return
  const dt = Math.min(0.1, lastTime ? (now - lastTime) / 1000 : 0.016)
  lastTime = now
  elapsed += dt
  const live = isMicActive()
  voiceOn += ((live ? 1 : 0) - voiceOn) * (1 - Math.exp(-(live ? 4 : 1.5) * dt))
  const info: FrameInfo = { dt, time: elapsed, voice: live ? getMicLevel() : 0, voiceOn }
  try {
    active.frame(info)
  } catch (err) {
    console.error('[zen-gpu] frame failed, falling back:', err)
    fail('Scene failed to render')
  }
}

function startLoop() {
  if (raf !== null) return
  lastTime = 0
  raf = requestAnimationFrame(loop)
}

function stopLoop() {
  if (raf !== null) cancelAnimationFrame(raf)
  raf = null
}

function onVisibility() {
  if (document.hidden) stopLoop()
  else if (active && container) startLoop()
}

/**
 * Show scene `id` inside `el`. Resolves true once the first frame is
 * scheduled, false if `isCurrent()` turned false while the scene was building
 * (nothing is attached then). Rejects if the GPU renderer cannot be created.
 */
export async function showZenGpuScene(id: string, el: HTMLElement, isCurrent: () => boolean = () => true): Promise<boolean> {
  if (failed) throw new Error('GPU renderer unavailable')
  const scene = await getScene(id)
  // The user moved on (other scene, left zen) while this one was building
  if (!isCurrent()) return false
  const c = ctx!
  const canvas = c.renderer.domElement as HTMLCanvasElement

  if (active && active !== scene) active.leave()
  active = scene
  registerFlyTarget(FLY_OWNER, null)
  if (scene.canFly) registerFlyTarget(FLY_OWNER, (on) => scene.setFlying?.(on))
  container = el
  if (canvas.parentElement !== el) {
    canvas.style.opacity = '0'
    el.prepend(canvas)
  }
  resizeObserver?.disconnect()
  resizeObserver = new ResizeObserver(handleResize)
  resizeObserver.observe(el)
  handleResize()
  scene.enter(el)

  if (!visibilityHooked) {
    document.addEventListener('visibilitychange', onVisibility)
    visibilityHooked = true
  }
  if (!document.hidden) startLoop()
  requestAnimationFrame(() => requestAnimationFrame(() => { canvas.style.opacity = '1' }))
  return true
}

/** Hide the GPU canvas and pause (e.g. when switching back to Waves or leaving zen). */
export function hideZenGpu() {
  registerFlyTarget(FLY_OWNER, null)
  stopLoop()
  resizeObserver?.disconnect()
  resizeObserver = null
  if (active) active.leave()
  active = null
  const canvas = ctx?.renderer.domElement as HTMLCanvasElement | undefined
  if (canvas?.parentElement) canvas.parentElement.removeChild(canvas)
  container = null
}

/** Run `fn` against the visible scene, if any. */
export function withActiveZenScene(fn: (scene: ZenGpuScene) => void) {
  if (!active) return
  try {
    fn(active)
  } catch (err) {
    console.warn('[zen-gpu] scene event failed:', err)
  }
}

/** Clear per-session state in every built scene (e.g. switching session). */
export function resetZenGpuScenes() {
  for (const s of scenes.values()) {
    try { s.reset() } catch (err) { console.warn('[zen-gpu] reset failed:', err) }
  }
}

/** Status and project colour are remembered so scenes built later start right. */
export function setZenGpuStatus(status: string) {
  pendingStatus = status
  for (const s of scenes.values()) s.setStatus(status)
}

export function setZenGpuProjectColor(hex: string) {
  pendingColor = hex
  for (const s of scenes.values()) s.setProjectColor(hex)
}

/** Start downloading three/webgpu early (e.g. when the viz picker opens). */
export function preloadZenGpu() {
  if (typeof window === 'undefined') return
  import('three/webgpu').catch(() => {})
  import('three/tsl').catch(() => {})
}
