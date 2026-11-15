/**
 * How much of the top bar's machine chip fits on the window's middle
 * (components/MachineChip.vue). The chip is centred on the bar, so it may use
 * twice the smaller distance from the middle to the logo side or the right
 * controls, minus a gap on each side. Pure, unit-tested.
 */
export type MachineChipMode = 'full' | 'dot' | 'hidden'

export interface MachineChipRoom {
  /** x of the bar's middle */
  center: number
  /** right edge of the logo side's content */
  leftEnd: number
  /** left edge of the right controls */
  rightStart: number
}

/** Space kept free between the chip and each side. */
export const MACHINE_CHIP_GAP = 12

/** Width the chip may take on the middle (never negative). */
export function machineChipRoom(r: MachineChipRoom, gap = MACHINE_CHIP_GAP): number {
  const half = Math.min(r.center - r.leftEnd, r.rightStart - r.center) - gap
  return Math.max(0, 2 * half)
}

/** The largest form that fits: the name, the dot alone, or nothing. */
export function machineChipMode(room: number, fullWidth: number, dotWidth: number): MachineChipMode {
  if (fullWidth > 0 && fullWidth <= room) return 'full'
  if (dotWidth > 0 && dotWidth <= room) return 'dot'
  return 'hidden'
}
