import { ref } from 'vue'

/**
 * The server's home folder, the only folder paths are shortened to `~` for
 * (`/Users/Shared/praline` is a home in its own right, `/Users/Shared` is not).
 * Empty until the server has answered: paths then stay absolute.
 */
export const homeDir = ref('')

export function setHomeDir(home: string | null | undefined): void {
  homeDir.value = typeof home === 'string' ? home.replace(/[\\/]+$/, '') : ''
}

/** One request, shared: `/api/system-info` carries `system.home`. */
let loading: Promise<void> | null = null
export function loadHomeDir(fetcher: (url: string) => Promise<Response>): Promise<void> {
  if (homeDir.value) return Promise.resolve()
  loading ??= (async () => {
    try {
      const res = await fetcher('/api/system-info')
      if (res.ok) setHomeDir((await res.json())?.system?.home)
    } catch {
      // Paths stay absolute; the next page load asks again.
    } finally {
      loading = null
    }
  })()
  return loading
}

/** /Users/me/code/app → ~/code/app when home is /Users/me; any other path is returned as is. */
export function shortenHome(path: string, home: string | null | undefined = homeDir.value): string {
  if (!path || !home) return path || ''
  const h = home.replace(/[\\/]+$/, '')
  if (!h) return path
  if (path === h) return '~'
  if (path.startsWith(h + '/') || path.startsWith(h + '\\')) return '~' + path.slice(h.length)
  return path
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

/** `shortenHome` for the paths inside a text (a command line): only whole paths that start at the home folder. */
export function shortenHomeIn(text: string, home: string | null | undefined = homeDir.value): string {
  const h = (home || '').replace(/[\\/]+$/, '')
  if (!h || !text) return text
  const re = new RegExp(`(^|[\\s'"=:(,])${escapeRe(h)}(?=[/\\\\\\s'"),;]|$)`, 'g')
  return text.replace(re, '$1~')
}
