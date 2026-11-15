// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import FilePreview from '../../components/agents/FilePreview.vue'
import SlashCommandAutocomplete from '../../components/SlashCommandAutocomplete.vue'
import PlanMessage from '../../components/agents/PlanMessage.vue'
import {
  renderSafeMarkdown,
  resolveProjectRelativePath,
  sanitizeHtml,
  svgToDataUrl,
} from '../../utils/safeMarkdown'

function dom(html: string): HTMLElement {
  const div = document.createElement('div')
  div.innerHTML = html
  return div
}

/** No element may carry an event handler or a javascript:/vbscript: URL. */
function expectInert(root: HTMLElement) {
  for (const el of Array.from(root.querySelectorAll('*'))) {
    for (const attr of Array.from(el.attributes)) {
      expect(attr.name.startsWith('on'), `${el.tagName} has ${attr.name}`).toBe(false)
      expect(/^\s*(javascript|vbscript|data:text)/i.test(attr.value), `${el.tagName} ${attr.name}=${attr.value}`).toBe(false)
    }
  }
  expect(root.querySelector('script, iframe, object, embed, svg, style, form, video, audio, source')).toBeNull()
}

describe('renderSafeMarkdown', () => {
  it('renders ordinary markdown like before', () => {
    const root = dom(renderSafeMarkdown('# Title\n\nSome **bold** and *em* and `code`.\n\n- a\n- b\n\n```js\nconst x = 1 < 2\n```\n\n> quote\n\n---\n'))
    expect(root.querySelector('h1')?.textContent).toBe('Title')
    expect(root.querySelector('strong')?.textContent).toBe('bold')
    expect(root.querySelector('em')?.textContent).toBe('em')
    expect(root.querySelectorAll('li')).toHaveLength(2)
    expect(root.querySelector('pre code.language-js')?.textContent).toBe('const x = 1 < 2\n')
    expect(root.querySelector('blockquote')).not.toBeNull()
    expect(root.querySelector('hr')).not.toBeNull()
  })

  it('does not let an image alt/src break out of its attribute', () => {
    const root = dom(renderSafeMarkdown('![x](x" onerror="alert(1))\n\n![a" onerror="alert(1)](https://e.com/i.png)'))
    expectInert(root)
    expect(root.querySelector('[onerror]')).toBeNull()
  })

  it('does not let a link break out of its attribute', () => {
    const root = dom(renderSafeMarkdown('[x](https://e.com/" onclick="alert(1))\n\n[y]("onmouseover="alert(1))'))
    expectInert(root)
  })

  it.each([
    '[x](javascript:alert(1))',
    '[x](JaVaScRiPt:alert(1))',
    '[x](java\tscript:alert(1))',
    '[x](  javascript:alert(1))',
    '[x](vbscript:msgbox(1))',
    '[x](data:text/html,<script>alert(1)</script>)',
    '<a href="javascript:alert(1)">x</a>',
    '<a href="&#106;avascript:alert(1)">x</a>',
  ])('drops dangerous link %s', (md) => {
    const root = dom(renderSafeMarkdown(md))
    expectInert(root)
    const a = root.querySelector('a')
    if (a) expect(a.getAttribute('href')).toBeNull()
  })

  it('keeps http(s), mailto and relative links, opened safely', () => {
    const root = dom(renderSafeMarkdown('[a](https://e.com) [b](http://e.com) [c](mailto:x@e.com) [d](./docs/x.md) [e](#top)'))
    const links = Array.from(root.querySelectorAll('a'))
    expect(links.map(a => a.getAttribute('href'))).toEqual(['https://e.com', 'http://e.com', 'mailto:x@e.com', './docs/x.md', '#top'])
    for (const a of links.slice(0, 4)) {
      expect(a.getAttribute('target')).toBe('_blank')
      expect(a.getAttribute('rel')).toBe('noopener noreferrer')
    }
  })

  it('strips raw HTML scripts, handlers, iframes, styles and forms', () => {
    const root = dom(renderSafeMarkdown([
      '<script>alert(1)</script>',
      '<img src=x onerror=alert(1)>',
      '<iframe src="https://e.com"></iframe>',
      '<svg onload=alert(1)><circle/></svg>',
      '<div style="background:url(https://e.com/leak)" onclick="alert(1)">hi</div>',
      '<style>body{background:url(https://e.com/leak)}</style>',
      '<form action="https://e.com"><input name=q><button>go</button></form>',
      '<details open ontoggle=alert(1)>x</details>',
      '<object data="x.swf"></object>',
      '<video poster="https://e.com/p.png" src="https://e.com/v.mp4"></video>',
    ].join('\n\n')))
    expectInert(root)
    expect(root.querySelector('[style]')).toBeNull()
    expect(root.innerHTML).not.toContain('e.com/leak')
  })

  it('never loads remote images; shows a link instead', () => {
    const root = dom(renderSafeMarkdown('![diagram](https://evil.example/track.png?secret=1)\n\n<img src="//evil.example/x.png">\n\n<img srcset="https://evil.example/a.png 1x" src="data:image/png;base64,AAAA">'))
    const imgs = Array.from(root.querySelectorAll('img'))
    for (const img of imgs) {
      expect(img.getAttribute('src')).toMatch(/^data:image\//)
      expect(img.hasAttribute('srcset')).toBe(false)
    }
    const remote = root.querySelector('a.md-remote-image')
    expect(remote?.getAttribute('href')).toBe('https://evil.example/track.png?secret=1')
    expect(remote?.getAttribute('rel')).toBe('noopener noreferrer')
    expect(remote?.textContent).toContain('diagram')
    expect(remote?.textContent).toContain('evil.example')
    // Protocol-relative image: not loaded, not linked.
    expect(root.querySelector('.md-image-unavailable')).not.toBeNull()
  })

  it('keeps data: images and resolves relative images through the resolver only', () => {
    const md = '![a](data:image/png;base64,iVBORw0KGgo=)\n\n![b](./img/b.png)\n\n![c](../../etc/passwd)'
    const without = dom(renderSafeMarkdown(md))
    expect(without.querySelectorAll('img')).toHaveLength(1)

    const resolved = dom(renderSafeMarkdown(md, {
      resolveImage: (src) => {
        const rel = resolveProjectRelativePath('docs/README.md', src)
        return rel ? `/api/projects/p1/files/raw?path=${encodeURIComponent(rel)}` : null
      },
    }))
    const srcs = Array.from(resolved.querySelectorAll('img')).map(i => i.getAttribute('src'))
    expect(srcs).toEqual([
      'data:image/png;base64,iVBORw0KGgo=',
      '/api/projects/p1/files/raw?path=docs%2Fimg%2Fb.png',
    ])
  })

  it('ignores a resolver that returns a remote URL', () => {
    const root = dom(renderSafeMarkdown('![x](a.png)', { resolveImage: () => 'https://evil.example/a.png' }))
    expect(root.querySelector('img')).toBeNull()
  })
})

describe('resolveProjectRelativePath', () => {
  it('resolves against the markdown file directory', () => {
    expect(resolveProjectRelativePath('docs/guide/README.md', 'img/a.png')).toBe('docs/guide/img/a.png')
    expect(resolveProjectRelativePath('docs/guide/README.md', '../a.png')).toBe('docs/a.png')
    expect(resolveProjectRelativePath('docs/README.md', '/assets/a.png')).toBe('assets/a.png')
    expect(resolveProjectRelativePath('README.md', 'a%20b.png?x=1#y')).toBe('a b.png')
  })
  it('refuses to leave the project or follow a scheme', () => {
    expect(resolveProjectRelativePath('README.md', '../secret.png')).toBeNull()
    expect(resolveProjectRelativePath('README.md', 'https://e.com/a.png')).toBeNull()
    expect(resolveProjectRelativePath('README.md', '//e.com/a.png')).toBeNull()
  })
})

describe('sanitizeHtml', () => {
  it('keeps formatter markup and classes', () => {
    const html = '<em class="system-message">x</em><pre><code class="language-ts">a &lt;b&gt;</code></pre><a href="https://e.com" class="message-link">l</a>'
    const root = dom(sanitizeHtml(html))
    expect(root.querySelector('em.system-message')).not.toBeNull()
    expect(root.querySelector('code.language-ts')?.textContent).toBe('a <b>')
    expect(root.querySelector('a.message-link')?.getAttribute('rel')).toBe('noopener noreferrer')
  })
})

describe('SVG preview', () => {
  const evilSvg = '<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script><circle r="5"/></svg>'

  it('encodes SVG (including non-ASCII) as a base64 data URL', () => {
    const url = svgToDataUrl('<svg><text>é ✓</text></svg>')
    expect(url.startsWith('data:image/svg+xml;base64,')).toBe(true)
    const bytes = Uint8Array.from(atob(url.split(',')[1]!), c => c.charCodeAt(0))
    expect(new TextDecoder().decode(bytes)).toBe('<svg><text>é ✓</text></svg>')
  })

  it('FilePreview renders SVG through <img>, never inline', () => {
    const wrapper = mount(FilePreview, {
      props: {
        isLoading: false,
        content: { path: 'evil.svg', content: evilSvg, language: 'svg', size: evilSvg.length, binary: false, image: true, mimeType: 'image/svg+xml' },
      },
    })
    try {
      expect(wrapper.find('svg[onload]').exists()).toBe(false)
      expect(wrapper.find('.image-container script').exists()).toBe(false)
      const img = wrapper.find('.image-container img')
      expect(img.exists()).toBe(true)
      expect(img.attributes('src')).toBe(svgToDataUrl(evilSvg))
    } finally {
      wrapper.unmount()
    }
  })

  it('FilePreview renders malicious markdown inertly', () => {
    const md = '# Hi\n\n![x](x" onerror="alert(1))\n\n[x](javascript:alert(1))\n\n![t](https://evil.example/t.png)\n\n<img src=x onerror=alert(1)>'
    const wrapper = mount(FilePreview, {
      props: {
        isLoading: false,
        content: { path: 'docs/README.md', content: md, language: 'markdown', size: md.length, binary: false },
        rawUrlFor: (p: string) => `/api/projects/p/files/raw?path=${encodeURIComponent(p)}`,
      },
    })
    try {
      const root = wrapper.find('.markdown-preview').element as HTMLElement
      expect(root.querySelector('h1')?.textContent).toBe('Hi')
      expectInert(root)
      for (const img of Array.from(root.querySelectorAll('img'))) {
        expect(img.getAttribute('src')).toMatch(/^\/api\/projects\/p\/files\/raw\?path=/)
      }
      expect(root.querySelector('a.md-remote-image')).not.toBeNull()
    } finally {
      wrapper.unmount()
    }
  })
})

describe('other agent-controlled HTML', () => {
  it('SlashCommandAutocomplete escapes command names and regex-special queries', async () => {
    const wrapper = mount(SlashCommandAutocomplete, {
      props: {
        visible: true,
        query: '<img (',
        isFeatureMode: true,
        availableFeatures: ['x<img (src=x onerror=alert(1)>'],
        position: { top: 0, left: 0 },
      },
      global: { stubs: { Teleport: true } },
    })
    try {
      expect(wrapper.find('img').exists()).toBe(false)
      expect(wrapper.find('.highlight').text()).toBe('<img (')
      expect(wrapper.text()).toContain('onerror=alert(1)>')
    } finally {
      wrapper.unmount()
    }
  })

  it('PlanMessage default formatter escapes the plan', () => {
    const wrapper = mount(PlanMessage, { props: { content: '# Plan\n<img src=x onerror=alert(1)>' } })
    try {
      expect(wrapper.find('img').exists()).toBe(false)
      expect(wrapper.find('h1').text()).toBe('Plan')
    } finally {
      wrapper.unmount()
    }
  })
})
