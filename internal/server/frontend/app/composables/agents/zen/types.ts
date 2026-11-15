/**
 * Shared contract for the GPU zen scenes (Galaxy, Ink).
 *
 * Scenes are plain objects driven by the GPU host (gpuHost.ts): the host owns
 * the WebGPURenderer, the canvas and the frame loop; a scene owns its own
 * three.js objects and reacts to session events.
 */

export type TSLModule = typeof import('three/tsl')
export type ThreeModule = typeof import('three/webgpu')

export type ToolCategory = 'read' | 'edit' | 'bash' | 'search' | 'web' | 'agent' | 'message' | 'error' | 'other'

export interface ZenToolEvent {
  tool: string
  category: ToolCategory
  /** Full file path for file tools (Read/Edit/Write), else '' */
  filePath: string
  /** Short human label (file name, command, pattern…) */
  label: string
  isError: boolean
}

export interface SceneContext {
  THREE: ThreeModule
  TSL: TSLModule
  renderer: any
  /** true on the WebGPU backend, false on the WebGL2 fallback */
  webgpu: boolean
  reducedMotion: boolean
}

export interface FrameInfo {
  dt: number
  time: number
  /** Smoothed mic level 0..1 (0 when the mic is off) */
  voice: number
  /** 0..1, eases in while the mic is live */
  voiceOn: number
}

export interface ZenGpuScene {
  readonly id: string
  /** Supports setFlying() */
  readonly canFly?: boolean
  setFlying?(on: boolean): void
  build(ctx: SceneContext): Promise<void>
  /** Called when the scene becomes visible (after build). `overlay` is the
   *  element the canvas lives in — scenes may add DOM layers (labels) to it. */
  enter(overlay: HTMLElement): void
  /** Called when the scene is hidden; stop anything that should not linger */
  leave(): void
  /** Forget everything tied to the current session (switching session) */
  reset(): void
  resize(width: number, height: number, pixelRatio: number): void
  frame(info: FrameInfo): void
  setStatus(status: string): void
  setProjectColor(hex: string): void
  toolEvent(e: ZenToolEvent): void
  sendPulse(): void
  agentSpawn(id: string): void
  agentComplete(id: string): void
}

/** Map a tool name to a category shared by all scenes. */
export function toolCategory(tool: string, isError = false): ToolCategory {
  if (isError) return 'error'
  const t = tool.toLowerCase()
  if (t === 'read' || t === 'notebookread') return 'read'
  if (t === 'edit' || t === 'write' || t === 'multiedit' || t === 'notebookedit' || t === 'filechange') return 'edit'
  if (t === 'bash' || t === 'bashoutput' || t === 'killshell') return 'bash'
  if (t === 'grep' || t === 'glob' || t === 'search' || t === 'ls') return 'search'
  if (t === 'webfetch' || t === 'websearch') return 'web'
  if (t === 'task' || t === 'agent') return 'agent'
  if (t === 'message' || t === 'complete') return 'message'
  return 'other'
}

/** Deterministic 32-bit hash → [0, 1). */
export function hash01(s: string, salt = 0): number {
  let h = 2166136261 ^ salt
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  h ^= h >>> 13
  h = Math.imul(h, 0x5bd1e995)
  h ^= h >>> 15
  return (h >>> 0) / 4294967296
}

export function hexToRgb01(hex: string): [number, number, number] | null {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex?.trim() || '')
  if (!m) return null
  const n = parseInt(m[1]!, 16)
  return [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255]
}
