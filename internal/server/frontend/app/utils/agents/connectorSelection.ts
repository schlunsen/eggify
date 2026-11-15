// Connector selection for Create Session.
//
// The last selection is remembered in localStorage and preselected next time.
// Without a saved selection, connected API-key connectors are preselected but
// n0 is not: it brings the whole workspace's app tools (often hundreds).
// The create_session payload always carries the `connectors` array: an empty
// array means "no connectors" (a missing field makes the server enable all).

export const LAST_CONNECTORS_KEY = 'eggify-last-connectors'
export const N0_CONNECTOR_SLUG = 'n0'

export interface SavedConnectorSelection {
  /** Connectors the user chose. */
  selected: string[]
  /** Connectors that were offered at the time (to tell "unticked" from "new"). */
  offered: string[]
}

function strings(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((s): s is string => typeof s === 'string') : []
}

export function loadLastConnectorSelection(): SavedConnectorSelection | null {
  try {
    const raw = localStorage.getItem(LAST_CONNECTORS_KEY)
    if (raw == null) return null
    const parsed = JSON.parse(raw)
    if (Array.isArray(parsed)) {
      const selected = strings(parsed)
      return { selected, offered: selected }
    }
    if (parsed && typeof parsed === 'object' && Array.isArray(parsed.selected)) {
      return { selected: strings(parsed.selected), offered: strings(parsed.offered) }
    }
    return null
  } catch {
    return null
  }
}

/** Remember a selection. Nothing is saved when no connectors were offered. */
export function saveLastConnectorSelection(selected: readonly string[], offered: readonly string[]): void {
  if (offered.length === 0) return
  try {
    localStorage.setItem(LAST_CONNECTORS_KEY, JSON.stringify({ selected: [...selected], offered: [...offered] }))
  } catch {
    // storage unavailable: nothing to remember
  }
}

/**
 * The connectors to preselect: the saved selection (limited to what is
 * available now), or every available connector except n0. A connector that
 * was not offered when the selection was saved (connected since) gets the
 * no-saved-selection default.
 */
export function defaultConnectorSelection(available: readonly string[], saved: SavedConnectorSelection | null): string[] {
  return available.filter((slug) => {
    if (saved && saved.offered.includes(slug)) return saved.selected.includes(slug)
    if (saved && saved.selected.includes(slug)) return true
    return slug !== N0_CONNECTOR_SLUG
  })
}

/** The `connectors` value for create_session: always an array. */
export function connectorsPayload(selected: unknown): string[] {
  return strings(selected)
}
