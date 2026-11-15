import { nextTick } from 'vue'
import { desktopShell, loadWailsRuntime } from '~/utils/desktop'
import '~/assets/css/desktop-shell.css'

/**
 * Nuxt client plugin: window chrome for the Wails desktop app.
 *
 * On macOS the Wails window has no visible title bar (traffic lights over the
 * content, like Tauri's titleBarStyle "Overlay"). This marks <html> with
 * `desktop-wails` / `desktop-mac`, loads the Wails runtime (it implements the
 * `--wails-draggable` drag regions) and adds a drag strip for pages without
 * the navbar. See assets/css/desktop-shell.css. No-op in browsers and Tauri.
 */
export default defineNuxtPlugin((nuxtApp) => {
  if (desktopShell() !== 'wails') return

  const root = document.documentElement
  root.classList.add('desktop-wails')
  if (/Macintosh|Mac OS X/.test(navigator.userAgent)) root.classList.add('desktop-mac')

  loadWailsRuntime().catch((err) => {
    console.warn('[desktop] Wails runtime unavailable:', err)
  })

  const strip = document.createElement('div')
  strip.className = 'desktop-drag-strip'
  strip.setAttribute('aria-hidden', 'true')
  document.body.appendChild(strip)

  const syncNavbar = () => root.classList.toggle('has-navbar', !!document.querySelector('nav.navbar'))
  nuxtApp.hook('page:finish', () => nextTick(syncNavbar))
  nuxtApp.hook('app:mounted', () => nextTick(syncNavbar))
})
