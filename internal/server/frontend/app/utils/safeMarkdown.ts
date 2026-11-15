/**
 * Safe rendering of agent-controlled markdown / SVG.
 *
 * Files in a project and the output of tools are written by agents, and an
 * agent can be steered by text it read. Everything here treats that content as
 * hostile: markdown is parsed with `marked`, then sanitized with DOMPurify, and
 * the result is post-processed so that
 *   - links only keep http(s):, mailto:, in-page (#) and relative targets, and
 *     open in a new tab with rel="noopener noreferrer";
 *   - images never load from another host on their own: remote images become a
 *     link the user can choose to open; data: images and project-relative
 *     images (when a resolver is given) are kept;
 *   - no scripts, event handlers, styles, forms, frames or media survive.
 */
import { Marked } from 'marked'
import DOMPurify from 'dompurify'

export interface SafeMarkdownOptions {
  /**
   * Maps a relative image path (as written in the markdown) to a URL on the
   * Eggify origin, or returns null to leave the image out. Without it relative
   * images are shown as text.
   */
  resolveImage?: (src: string) => string | null
}

const marked = new Marked({ gfm: true, breaks: false, async: false })

const SCHEME_RE = /^[a-z][a-z0-9+.-]*:/i
// Characters browsers strip from URLs before reading the scheme
// (e.g. "java\tscript:" is still javascript:).
// eslint-disable-next-line no-control-regex
const URL_NOISE_RE = /[\x00-\x20\x7f-\x9f]/g

function normalizeUrl(url: string): string {
  return url.replace(URL_NOISE_RE, '')
}

/** True for http:, https: and mailto: URLs. */
export function isSafeExternalUrl(url: string): boolean {
  return /^(https?:|mailto:)/i.test(normalizeUrl(url))
}

/** True for URLs without a scheme or authority (relative paths, #anchors, ?query). */
export function isRelativeUrl(url: string): boolean {
  const u = normalizeUrl(url)
  if (u === '') return false
  if (/^[\\/][\\/]/.test(u)) return false // protocol-relative (//host, \\host, /\host)
  return !SCHEME_RE.test(u)
}

/** sessionImageUrl()'s shape, the one relative image URL kept without a resolver. */
export const SESSION_IMAGE_SRC_RE = /^\/api\/agent\/sessions\/[0-9a-f-]{36}\/image\?path=[A-Za-z0-9%._~!*'()-]+$/

/** URL of an image a session's agent wrote (served with the session's folder rules). */
export function sessionImageUrl(sessionId: string, path: string): string {
  return `/api/agent/sessions/${encodeURIComponent(sessionId)}/image?path=${encodeURIComponent(path)}`
}

const SAFE_DATA_IMAGE_RE = /^data:image\/(png|jpe?g|gif|webp|avif|bmp|x-icon|svg\+xml)[;,]/i

const FORBID_TAGS = [
  'style', 'form', 'input', 'button', 'textarea', 'select', 'option', 'iframe', 'frame',
  'object', 'embed', 'audio', 'video', 'source', 'track', 'picture', 'link', 'meta', 'base',
  'svg', 'math', 'template', 'dialog',
]
// Every attribute that can make the browser fetch something, except a/href and
// img/src which are checked below.
const FORBID_ATTR = [
  'style', 'srcset', 'background', 'poster', 'action', 'formaction', 'ping', 'xlink:href',
  'usemap', 'longdesc', 'target', 'rel',
]

let purifier: ReturnType<typeof DOMPurify> | null = null
function getPurifier() {
  // A dedicated instance so this configuration never leaks into other
  // DOMPurify users.
  if (!purifier) purifier = DOMPurify(window)
  return purifier
}

/** Sanitize an HTML fragment with the same policy as rendered markdown. */
export function sanitizeHtml(html: string, options: SafeMarkdownOptions = {}): string {
  if (typeof window === 'undefined') return ''
  const fragment = getPurifier().sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS,
    FORBID_ATTR,
    ALLOW_DATA_ATTR: false,
    ALLOW_UNKNOWN_PROTOCOLS: false,
    RETURN_DOM_FRAGMENT: true,
  }) as DocumentFragment

  for (const a of Array.from(fragment.querySelectorAll('a'))) {
    const href = a.getAttribute('href')
    if (href == null) continue
    if (isSafeExternalUrl(href) || (isRelativeUrl(href) && !href.startsWith('#'))) {
      a.setAttribute('target', '_blank')
      a.setAttribute('rel', 'noopener noreferrer')
    } else if (!isRelativeUrl(href)) {
      a.removeAttribute('href')
    }
  }

  for (const img of Array.from(fragment.querySelectorAll('img'))) {
    const src = img.getAttribute('src') || ''
    const alt = img.getAttribute('alt') || ''
    if (SAFE_DATA_IMAGE_RE.test(normalizeUrl(src))) continue
    // A session's own image endpoint (formatMessage's ![alt](path) in chat):
    // same origin, read-only, and it applies the folder rules itself.
    if (SESSION_IMAGE_SRC_RE.test(src)) {
      img.setAttribute('loading', 'lazy')
      continue
    }
    if (src && isRelativeUrl(src) && !src.startsWith('#') && options.resolveImage) {
      const resolved = options.resolveImage(src)
      if (resolved && isRelativeUrl(resolved)) {
        img.setAttribute('src', resolved)
        img.setAttribute('loading', 'lazy')
        continue
      }
    }
    img.replaceWith(imagePlaceholder(img.ownerDocument, src, alt))
  }

  const container = document.createElement('div')
  container.appendChild(fragment)
  return container.innerHTML
}

function imagePlaceholder(doc: Document, src: string, alt: string): HTMLElement {
  const label = alt.trim() || 'image'
  if (/^https?:/i.test(normalizeUrl(src))) {
    // Remote image: never fetched automatically (loading it would tell the
    // remote host that, and when, the file was viewed - and a URL can carry
    // data out). The user can open it explicitly.
    const a = doc.createElement('a')
    a.className = 'md-remote-image'
    a.setAttribute('href', src)
    a.setAttribute('target', '_blank')
    a.setAttribute('rel', 'noopener noreferrer')
    a.setAttribute('title', `Remote image (not loaded): ${src}`)
    a.textContent = `🖼 ${label}`
    const host = doc.createElement('span')
    host.className = 'md-remote-image-host'
    try {
      host.textContent = ` (remote image: ${new URL(normalizeUrl(src)).host})`
    } catch {
      host.textContent = ' (remote image)'
    }
    a.appendChild(host)
    return a
  }
  const span = doc.createElement('span')
  span.className = 'md-image-unavailable'
  span.setAttribute('title', src ? `Image not shown: ${src}` : 'Image not shown')
  span.textContent = `🖼 ${label}`
  return span
}

/** Render untrusted markdown to sanitized HTML. */
export function renderSafeMarkdown(markdown: string, options: SafeMarkdownOptions = {}): string {
  if (!markdown) return ''
  const html = marked.parse(markdown) as string
  return sanitizeHtml(html, options)
}

/**
 * Resolve a markdown-relative path against the markdown file's own path,
 * staying inside the project (".." above the root gives null). Returns null
 * for anything that is not a plain relative path.
 */
export function resolveProjectRelativePath(fromFile: string, target: string): string | null {
  const clean = normalizeUrl(target).split(/[?#]/)[0] ?? ''
  if (!clean || !isRelativeUrl(clean)) return null
  let decoded: string
  try {
    decoded = decodeURIComponent(clean)
  } catch {
    return null
  }
  const parts = decoded.startsWith('/') ? [] : fromFile.split('/').slice(0, -1).filter(Boolean)
  for (const seg of decoded.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      if (parts.length === 0) return null
      parts.pop()
      continue
    }
    parts.push(seg)
  }
  return parts.length ? parts.join('/') : null
}

/**
 * Turn SVG source into a data: URL for an <img>. SVG shown through <img> is
 * rendered as an image: scripts, event handlers and external loads inside it
 * do not run, unlike inline SVG.
 */
export function svgToDataUrl(svg: string): string {
  const bytes = new TextEncoder().encode(svg)
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return `data:image/svg+xml;base64,${btoa(binary)}`
}
