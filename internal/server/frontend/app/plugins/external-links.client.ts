import { desktopOpenExternal, isDesktop } from '~/utils/desktop'

/**
 * Nuxt client plugin: External Link Handler for the desktop apps
 *
 * In the desktop apps (Wails or Tauri), `target="_blank"` links don't
 * automatically open in the user's default browser — the webview blocks or
 * ignores them.
 *
 * This plugin adds a global click interceptor that:
 * - Detects if we're running inside a desktop shell (see utils/desktop.ts)
 * - Intercepts clicks on links with `target="_blank"` or external hrefs
 * - Opens them in the OS default browser via the shell's native API
 * - Does nothing in a regular browser — links work normally
 */
export default defineNuxtPlugin(() => {
  // Only activate inside a desktop shell
  if (!isDesktop()) return

  /**
   * Open a URL in the default browser. Tauri needs `shell:allow-open` in
   * src-tauri/capabilities/default.json; Wails uses DesktopService.OpenExternal.
   */
  async function openInBrowser(url: string) {
    try {
      await desktopOpenExternal(url)
    }
    catch (err) {
      console.error('[external-links] Failed to open URL in browser:', url, err)
      // Fallback: try window.open (may or may not work in webview)
      window.open(url, '_blank')
    }
  }

  /**
   * Determine if a URL is "external" — i.e. should open in the browser,
   * not navigate the webview.
   */
  function isExternalUrl(href: string): boolean {
    if (!href) return false
    // Explicit external protocols
    if (href.startsWith('http://') || href.startsWith('https://')) {
      // Only the app's own origin stays in the webview. Other local ports
      // belong to services linked from messages and must open in the browser.
      try {
        const url = new URL(href)
        if (url.origin === window.location.origin) {
          return false
        }
      }
      catch {
        // If URL parsing fails, treat as external to be safe
      }
      return true
    }
    // mailto:, tel:, etc.
    if (href.startsWith('mailto:') || href.startsWith('tel:')) {
      return true
    }
    return false
  }

  // Global click interceptor on the document
  document.addEventListener('click', (event) => {
    // Walk up the DOM to find the nearest <a> element
    const target = (event.target as HTMLElement)?.closest?.('a')
    if (!target) return

    const href = target.getAttribute('href')
    if (!href) return

    // Only open links that point to actual external URLs
    // (ignore relative paths like "/some/page" even if they have target="_blank")
    if (isExternalUrl(href)) {
      event.preventDefault()
      openInBrowser(href)
    }
  }, true) // Use capture phase to intercept before Vue handlers
})
