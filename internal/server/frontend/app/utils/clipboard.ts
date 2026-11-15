/**
 * Copies text to the clipboard in every shell the dashboard runs in.
 *
 * In the Wails desktop app `navigator.clipboard.writeText` is rejected by the
 * WKWebView, so copy buttons silently did nothing there. Order:
 *  1. Wails: the native clipboard through the runtime (`Clipboard.SetText`);
 *  2. the async Clipboard API (browsers, Tauri);
 *  3. a hidden textarea + `document.execCommand('copy')` (older/locked-down webviews).
 *
 * Resolves when the text was copied; rejects when every route failed.
 */
import { desktopShell, loadWailsRuntime } from './desktop'

export async function copyText(text: string): Promise<void> {
  if (desktopShell() === 'wails') {
    try {
      const rt = await loadWailsRuntime()
      if (rt?.Clipboard?.SetText) {
        await rt.Clipboard.SetText(text)
        return
      }
    } catch { /* fall through to the web routes */ }
  }

  let apiError: unknown
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch (err) {
      apiError = err
    }
  }

  if (!execCommandCopy(text)) throw apiError ?? new Error('Clipboard unavailable')
}

function execCommandCopy(text: string): boolean {
  if (typeof document === 'undefined') return false
  const active = document.activeElement as HTMLElement | null
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '0'
  area.style.left = '0'
  area.style.opacity = '0'
  area.style.pointerEvents = 'none'
  document.body.appendChild(area)
  area.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  area.remove()
  active?.focus?.()
  return ok
}
