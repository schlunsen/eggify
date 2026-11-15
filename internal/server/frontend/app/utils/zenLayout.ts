/**
 * Where the Zen HUD puts the latest response.
 *
 * On a wide stage the reply is a full-height panel on the right (below the top
 * bar, above the composer, clear of the right-edge controls), and the status
 * chips and "Your message" stay in a centre column left of it. On a narrow stage
 * it is the clamped card in the centre column.
 */

/** Stage width (px) from which the response becomes a side panel. */
export const ZEN_SIDE_PANEL_MIN_WIDTH = 1000
/** Gap kept between the panel and the right edge (the sound/fullscreen controls sit there). */
export const ZEN_SIDE_PANEL_RIGHT = 56
/** Top of the panel when nothing above it is in the way (below the avatar card). */
export const ZEN_SIDE_PANEL_TOP = 64
/** Height of the response box's edge fade (px): about two lines. */
export const ZEN_RESP_FADE_PX = 56

export type ZenResponseLayout = 'panel' | 'card'

export function zenResponseLayout(stageWidth: number): ZenResponseLayout {
  return stageWidth >= ZEN_SIDE_PANEL_MIN_WIDTH ? 'panel' : 'card'
}

/** Panel width for a stage width: 30 %, between 300 and 460 px (CSS: clamp(300px, 30%, 460px)). */
export function zenSidePanelWidth(stageWidth: number): number {
  return Math.min(460, Math.max(300, stageWidth * 0.3))
}

/**
 * Top of the side panel. The Circus name plate is centred under the bunting
 * (`left: 50%`, width min(520px, 100% - 520px), at least 220px); when its right
 * edge reaches the panel, the panel starts below the plate instead of under it.
 */
export function zenSidePanelTop(stageWidth: number, plateTop: number, plateHeight: number): number {
  if (plateHeight <= 0) return ZEN_SIDE_PANEL_TOP
  const plateHalf = Math.max(220, Math.min(520, stageWidth - 520)) / 2
  const plateRight = stageWidth / 2 + plateHalf
  const panelLeft = stageWidth - ZEN_SIDE_PANEL_RIGHT - zenSidePanelWidth(stageWidth)
  if (plateRight + 12 <= panelLeft) return ZEN_SIDE_PANEL_TOP
  return Math.max(ZEN_SIDE_PANEL_TOP, Math.ceil(plateTop + plateHeight + 14))
}
