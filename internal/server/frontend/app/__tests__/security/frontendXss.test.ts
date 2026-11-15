// jsdom: happy-dom's parser drops the first node of a DOMPurify fragment,
// which would hide what formatMessage really returns.
// @vitest-environment jsdom
/**
 * XSS regressions for v-html sinks that render agent/tool text:
 * search highlights (SearchOverlay, MessageHistoryDropdown) and the regex
 * markdown formatter (chat, ThinkingSection, MessageDetailModal, PlanMessage).
 * Also: the API key never goes into WebSocket URLs off loopback.
 */
import { describe, it, expect } from 'vitest'
import { useMessageSearch, highlightSearchMatches } from '~/composables/agents/useMessageSearch'
import { formatMessage } from '~/utils/agents/messageFormatters'
import { isLoopbackHostname, wsQueryToken, wsTokenQuery } from '~/utils/wsAuth'

const PAYLOADS = [
  '<img src=x onerror=alert(1)>',
  '<iframe srcdoc="<script>alert(1)</script>"></iframe>',
  '<svg onload=alert(1)>',
  '"><script>alert(1)</script>',
]

/** Parse html and report anything executable or style-bearing. */
function dangerous(html: string): string[] {
  const host = document.createElement('div')
  host.innerHTML = html
  const found: string[] = []
  for (const el of Array.from(host.querySelectorAll('*'))) {
    const tag = el.tagName.toLowerCase()
    if (['img', 'iframe', 'svg', 'script', 'object', 'embed', 'style', 'form'].includes(tag)) found.push(`<${tag}>`)
    for (const attr of Array.from(el.attributes)) {
      const name = attr.name.toLowerCase()
      if (name.startsWith('on') || name === 'style' || name === 'srcdoc') found.push(`${tag}[${name}]`)
      if ((name === 'href' || name === 'src') && /^\s*javascript:/i.test(attr.value)) found.push(`${tag}[${name}=js]`)
    }
  }
  return found
}

// Mirrors SearchOverlay's extractPreview (100 chars + ellipsis).
const preview = (text: string) => (text.length > 100 ? text.substring(0, 100) + '...' : text)

describe('search highlighting escapes agent text', () => {
  for (const payload of PAYLOADS) {
    it(`blank query: ${payload}`, () => {
      const s = useMessageSearch()
      for (const q of ['', '   ', '\t']) {
        s.searchQuery.value = q
        const html = s.highlightMatches(preview(`before ${payload} after`))
        expect(html).not.toContain('<img')
        expect(html).not.toContain('<iframe')
        expect(html).not.toContain('<svg')
        expect(html).not.toContain('<script')
        expect(dangerous(html)).toEqual([])
      }
    })

    it(`query not in preview: ${payload}`, () => {
      const s = useMessageSearch()
      s.searchQuery.value = 'zzz-not-there'
      const html = s.highlightMatches(preview(`${payload} plain text`))
      expect(dangerous(html)).toEqual([])
      expect(html).toContain('&lt;')
    })

    it(`match only after char 100: ${payload}`, () => {
      const s = useMessageSearch()
      const text = `${payload}${'x'.repeat(120)} needle`
      s.searchQuery.value = 'needle'
      // filterMessages finds the message, but the preview is cut before the match.
      expect(s.filterMessages([{ id: '1', role: 'assistant', content: text } as never])).toHaveLength(1)
      const html = s.highlightMatches(preview(text))
      expect(html).not.toContain('<mark>')
      expect(dangerous(html)).toEqual([])
    })
  }

  it('wraps matches in <mark> and escapes around them', () => {
    expect(highlightSearchMatches('a <b>Needle</b> & needle', 'needle')).toBe(
      'a &lt;b&gt;<mark>Needle</mark>&lt;/b&gt; &amp; <mark>needle</mark>',
    )
    expect(highlightSearchMatches('Needle needle', 'needle', true)).toBe('Needle <mark>needle</mark>')
  })

  it('a query that looks like markup or an entity cannot create tags', () => {
    const text = 'x &lt; <img src=x onerror=alert(1)> y'
    for (const q of ['<img', 'lt', 'amp', '&', '<', '>', '"', 'onerror=alert(1)>']) {
      const html = highlightSearchMatches(text, q)
      expect(dangerous(html)).toEqual([])
      const host = document.createElement('div')
      host.innerHTML = html
      expect(host.textContent).toBe(text)
    }
  })
})

describe('formatMessage cannot break out of attributes', () => {
  const cases = [
    // A code fence right after a URL: the old URL linker swallowed the code
    // placeholder, and the restored <pre><code class="..."> closed href.
    'see https://evil.example```x style=position:fixed;inset:0;background:red onmouseover=alert(1)\ncode\n```',
    'see https://evil.example`" style="position:fixed;inset:0" onmouseover="alert(1)`',
    'https://a.example/`x`https://b.example/```y onclick=alert(1)\nz\n```',
    '**https://evil.example/"onmouseover="alert(1)**',
    "https://evil.example/' style='position:fixed",
    ...PAYLOADS,
  ]

  for (const input of cases) {
    it(JSON.stringify(input).slice(0, 80), () => {
      const html = formatMessage(input)
      expect(dangerous(html)).toEqual([])
      const host = document.createElement('div')
      host.innerHTML = html
      for (const a of Array.from(host.querySelectorAll('a'))) {
        const href = a.getAttribute('href') || ''
        expect(href).not.toMatch(/[<>"'\s]/)
        expect(href).not.toContain('EGGIFY_CODE_')
        expect(a.getAttributeNames().sort()).toEqual(['class', 'href', 'rel', 'target'])
      }
      for (const el of Array.from(host.querySelectorAll('*'))) {
        for (const name of el.getAttributeNames()) {
          expect(['class', 'href', 'rel', 'target']).toContain(name)
        }
      }
    })
  }

  it('keeps the usual formatting', () => {
    const html = formatMessage('**bold** *it* `code` https://example.com/x\n```js\nconst a = 1 < 2\n```')
    expect(html).toContain('<strong>bold</strong>')
    expect(html).toContain('<em>it</em>')
    expect(html).toContain('<code>code</code>')
    expect(html).toMatch(/<a href="https:\/\/example\.com\/x"[^>]*class="message-link"/)
    expect(html).toContain('<pre><code class="language-js">const a = 1 &lt; 2\n</code></pre>')
  })

  it('links a URL followed by inline code without swallowing the code', () => {
    const host = document.createElement('div')
    host.innerHTML = formatMessage('https://example.com`code`')
    expect(host.querySelector('a')?.getAttribute('href')).toBe('https://example.com')
    expect(host.querySelector('code')?.textContent).toBe('code')
  })
})

describe('WebSocket URLs never carry the API key off loopback', () => {
  it('loopback detection', () => {
    for (const h of ['localhost', '127.0.0.1', '127.1.2.3', '::1', '[::1]', 'app.localhost']) {
      expect(isLoopbackHostname(h)).toBe(true)
    }
    for (const h of ['example.ngrok.app', '192.168.1.2', '10.0.0.1', 'localhost.evil.com', '127.0.0.1.nip.io', '']) {
      expect(isLoopbackHostname(h)).toBe(false)
    }
  })

  it('token only without a signed-in user on a loopback page', () => {
    expect(wsQueryToken('k', false, 'localhost')).toBe('k')
    expect(wsQueryToken('k', false, '127.0.0.1')).toBe('k')
    expect(wsQueryToken('k', true, 'localhost')).toBeNull()
    expect(wsQueryToken('k', false, 'abc.ngrok.app')).toBeNull()
    expect(wsQueryToken('k', true, 'abc.ngrok.app')).toBeNull()
    expect(wsQueryToken(null, false, 'localhost')).toBeNull()
    expect(wsTokenQuery('a&b', false, 'localhost')).toBe('token=a%26b')
    expect(wsTokenQuery('k', false, 'abc.ngrok.app')).toBe('')
  })
})
