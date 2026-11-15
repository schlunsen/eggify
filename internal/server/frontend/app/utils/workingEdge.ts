/**
 * The "working" edge around the composer: a liquid gradient drawn with the
 * `shaders` library (WebGPU) while the agent works, or a CSS gradient edge
 * where WebGPU is missing or motion is reduced.
 */

/** Theme variables the edge is coloured from, with fallbacks for themes that lack one. */
export const WORKING_EDGE_ACCENT: readonly [string, string] = ['--accent-purple', '#8b5cf6']
export const WORKING_EDGE_TINT: readonly [string, string] = ['--accent-cyan', '#06b6d4']

export type EdgeColors = [string, string, string, string]

/** WebGPU edge only when the browser has WebGPU and the user allows motion. */
export function canUseShaderEdge(win: Pick<Window, 'navigator' | 'matchMedia'> | undefined = globalThis.window): boolean {
  if (!win?.navigator || !('gpu' in win.navigator) || !(win.navigator as any).gpu) return false
  return !win.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

let probe: CanvasRenderingContext2D | null | undefined

/**
 * Any CSS colour (hex, rgb(), color-mix(), names) as `#rrggbb`, which the
 * shaders library accepts. Resolved by painting one pixel; null if it can't.
 */
export function toHex(color: string): string | null {
  const value = color.trim()
  if (!value) return null
  if (/^#[0-9a-f]{6}$/i.test(value)) return value.toLowerCase()
  if (probe === undefined) {
    try {
      const canvas = document.createElement('canvas')
      canvas.width = canvas.height = 1
      probe = canvas.getContext('2d', { willReadFrequently: true })
    } catch {
      probe = null
    }
  }
  if (!probe) return null
  probe.clearRect(0, 0, 1, 1)
  probe.fillStyle = '#000'
  probe.fillStyle = value
  probe.fillRect(0, 0, 1, 1)
  const [r, g, b, a] = probe.getImageData(0, 0, 1, 1).data
  if (!a) return null
  return '#' + [r, g, b].map(n => n!.toString(16).padStart(2, '0')).join('')
}

function mixHex(a: string, b: string, weightOfA: number): string {
  const ch = (hex: string, i: number) => parseInt(hex.slice(1 + i * 2, 3 + i * 2), 16)
  return '#' + [0, 1, 2].map((i) => {
    const v = Math.round(ch(a, i) * weightOfA + ch(b, i) * (1 - weightOfA))
    return v.toString(16).padStart(2, '0')
  }).join('')
}

/** Four shades of one accent (as the CSS edge): accent, lighter, a touch of the tint, deeper. */
export function edgePalette(accent: string, tint: string): EdgeColors {
  return [accent, mixHex(accent, '#ffffff', 0.55), mixHex(accent, tint, 0.6), mixHex(accent, '#000000', 0.6)]
}

/** The edge's colours from the theme variables in effect on `el`. */
export function readEdgeColors(el: Element | null): EdgeColors {
  const style = el ? getComputedStyle(el) : null
  const read = ([name, fallback]: readonly [string, string]) => toHex(style?.getPropertyValue(name) ?? '') ?? fallback
  return edgePalette(read(WORKING_EDGE_ACCENT), read(WORKING_EDGE_TINT))
}
