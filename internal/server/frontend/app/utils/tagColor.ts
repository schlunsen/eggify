/**
 * Colours for the user's session tags. Every tag gets its own hue from a hash
 * of its lowercased text, so "bug" is the same colour on every row and in
 * every view. Only the hue comes from here: the chip's lightness comes from
 * theme variables (`.tag-chip` mixes the hue with `--text-primary` for the
 * text and with `--card-bg` for the fill), so it reads on light and dark
 * themes alike. Put `tagChipStyle()` on the chip and copy the `.tag-chip`
 * rules from `components/agents/SessionItem.vue`.
 */

/** FNV-1a over the lowercased, trimmed tag. */
export function tagHash(tag: string): number {
  const text = (tag || '').trim().toLowerCase()
  let hash = 0x811c9dc5
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193)
  }
  return hash >>> 0
}

/** 0..359, stable per tag (case-insensitive). */
export function tagHue(tag: string): number {
  return tagHash(tag) % 360
}

/** Inline style for a `.tag-chip`: just the hue; the theme supplies the rest. */
export function tagChipStyle(tag: string): Record<string, string> {
  return { '--tag-hue': String(tagHue(tag)) }
}

/**
 * Which tags fit on a row. Rows are a fixed width, so instead of measuring we
 * keep adding tags while their text stays within `budget` characters; the
 * first tag is always shown (CSS ellipsizes it if it is very long) and the
 * rest become a "+N" chip.
 */
export function fitTags(tags: readonly string[], budget: number, maxShown = 4): { shown: string[]; hidden: string[] } {
  const shown: string[] = []
  let used = 0
  for (const tag of tags) {
    // Each chip costs its text plus ~2 characters of padding and gap.
    const cost = tag.length + 2
    if (shown.length > 0 && (used + cost > budget || shown.length >= maxShown)) break
    shown.push(tag)
    used += cost
  }
  return { shown, hidden: tags.slice(shown.length) }
}
