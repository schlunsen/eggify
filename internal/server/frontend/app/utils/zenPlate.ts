/** Text limits for the Zen name plate, so a long name or summary never grows the plate. */
export const PLATE_NAME_MAX = 44
export const PLATE_SUMMARY_MAX = 200

export function clip(text: string, max: number): string {
  const t = (text || '').replace(/\s+/g, ' ').trim()
  if (t.length <= max) return t
  return t.slice(0, Math.max(1, max - 1)).trimEnd() + '…'
}

export const plateName = (name: string) => clip(name, PLATE_NAME_MAX)
export const plateSummary = (summary: string) => clip(summary, PLATE_SUMMARY_MAX)
