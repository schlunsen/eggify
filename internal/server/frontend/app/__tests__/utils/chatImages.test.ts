import { formatMessage } from '~/utils/agents/messageFormatters'
import { sanitizeHtml } from '~/utils/safeMarkdown'

const SID = '3a1c8121-628e-461b-99f9-42c320b7acd6'

function imgs(html: string) {
  const d = document.createElement('div')
  d.innerHTML = html
  return Array.from(d.querySelectorAll('img'))
}

describe('images in chat replies', () => {
  it('shows a file the agent wrote from the session image endpoint', () => {
    const html = formatMessage('Here it is:\n![Pop art woman being printed](output/imagegen/pop-art-woman.png)', { sessionId: SID })
    const [img] = imgs(html)
    expect(img?.getAttribute('src')).toBe(`/api/agent/sessions/${SID}/image?path=output%2Fimagegen%2Fpop-art-woman.png`)
    expect(img?.getAttribute('alt')).toBe('Pop art woman being printed')
    expect(html).not.toContain('![')
    // CopyableMessageContent sanitizes again: the image stays.
    expect(imgs(sanitizeHtml(html))).toHaveLength(1)
  })

  it('handles absolute paths, file:// and spaces', () => {
    const [img] = imgs(formatMessage('![x](file:///Users/me/proj/a%20b.png)', { sessionId: SID }))
    expect(img?.getAttribute('src')).toBe(`/api/agent/sessions/${SID}/image?path=%2FUsers%2Fme%2Fproj%2Fa%20b.png`)
  })

  it('never loads remote images, and without a session shows only the label', () => {
    const remote = formatMessage('![tracker](https://evil.example/p.png)', { sessionId: SID })
    expect(imgs(remote)).toHaveLength(0)
    expect(remote).toContain('🖼 tracker')
    const none = formatMessage('![chart](out/chart.png)')
    expect(imgs(none)).toHaveLength(0)
    expect(none).toContain('🖼 chart')
  })

  it('cannot be turned into markup or another URL', () => {
    const html = formatMessage('![a" onerror="alert(1)](x.png" onerror="alert(1))', { sessionId: SID })
    for (const img of imgs(html)) expect(img.getAttribute('onerror')).toBeNull()
    const js = formatMessage('![a](javascript:alert(1))', { sessionId: SID })
    expect(imgs(js)).toHaveLength(0)
    // An <img> the agent typed itself is escaped text, never an image.
    expect(imgs(formatMessage(`<img src="/api/agent/sessions/${SID}/image?path=x.png">`, { sessionId: SID }))).toHaveLength(0)
    // The sanitizer keeps only the exact endpoint shape.
    expect(imgs(sanitizeHtml(`<img src="/api/agent/sessions/${SID}/image?path=a&x=1">`))).toHaveLength(0)
    expect(imgs(sanitizeHtml('<img src="/api/config/api-key">'))).toHaveLength(0)
  })

  it('leaves image syntax inside code alone', () => {
    const html = formatMessage('`![x](a.png)`', { sessionId: SID })
    expect(imgs(html)).toHaveLength(0)
    expect(html).toContain('![x](a.png)')
  })
})
