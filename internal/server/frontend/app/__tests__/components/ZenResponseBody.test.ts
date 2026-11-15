import { mount } from '@vue/test-utils'
import { describe, it, expect, afterEach, vi } from 'vitest'
import { nextTick } from 'vue'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import ZenResponseBody from '../../components/agents/ZenResponseBody.vue'
import { ZEN_RESP_FADE_PX } from '../../utils/zenLayout'

const src = readFileSync(resolve(__dirname, '../../components/agents/ZenResponseBody.vue'), 'utf8')

const md = '**Live:** done\n\n- one\n- two\n\nUse `code` and [a](https://example.com).\n\n```\nlong\n```'

function mockHeights(scroll: number, client: number) {
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(scroll)
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(client)
}
afterEach(() => vi.restoreAllMocks())

describe('ZenResponseBody', () => {
  it('renders markdown instead of raw characters', () => {
    const w = mount(ZenResponseBody, { props: { text: md } })
    const el = w.get('[data-testid="zen-resp-md"]')
    expect(el.find('strong').text()).toBe('Live:')
    expect(el.findAll('li').length).toBe(2)
    expect(el.find('pre code').exists()).toBe(true)
    expect(el.find('a').attributes('href')).toBe('https://example.com')
    expect(el.text()).not.toContain('**')
  })

  it('does not let scripts, handlers, images or javascript: links through', () => {
    const w = mount(ZenResponseBody, {
      props: { text: '<script>alert(1)</script><img src=x onerror=alert(1)>\n\n![p](https://evil.test/a.png)\n\n[x](javascript:alert(1))' },
    })
    const h = w.get('[data-testid="zen-resp-md"]').html()
    expect(h).not.toContain('<script')
    expect(h).not.toContain('<img')
    expect(h).not.toContain('onerror')
    expect(h).not.toContain('javascript:')
  })

  it('does not clamp or offer Read more for a short reply', () => {
    mockHeights(40, 40)
    const w = mount(ZenResponseBody, { props: { text: 'Hi' } })
    expect(w.find('[data-testid="zen-resp-more"]').exists()).toBe(false)
    expect(w.find('[data-testid="zen-resp-fade"]').exists()).toBe(false)
  })

  it('clamps a long reply, fades only the hidden edge and emits expand', async () => {
    mockHeights(900, 180)
    const w = mount(ZenResponseBody, { props: { text: md } })
    await nextTick()
    const box = w.get('[data-testid="zen-resp-box"]')
    // Pinned to the end of the reply: the text above is hidden, so the top edge fades
    expect(box.classes()).toContain('fade-top')
    expect(box.classes()).not.toContain('fade-bottom')
    await w.get('[data-testid="zen-resp-more"]').trigger('click')
    expect(w.emitted('expand')).toHaveLength(1)
  })

  it('fades the bottom edge once the reader scrolls up', async () => {
    mockHeights(900, 180)
    const w = mount(ZenResponseBody, { props: { text: md } })
    await nextTick()
    const box = w.get('[data-testid="zen-resp-box"]')
    ;(box.element as HTMLElement).scrollTop = 0
    await box.trigger('scroll')
    expect(box.classes()).toContain('fade-bottom')
    expect(box.classes()).not.toContain('fade-top')
  })

  it('keeps the fade to about two lines and fades with a mask, not by dimming the text', () => {
    expect(ZEN_RESP_FADE_PX).toBeGreaterThanOrEqual(48)
    expect(ZEN_RESP_FADE_PX).toBeLessThanOrEqual(64)
    const w = mount(ZenResponseBody, { props: { text: md } })
    expect(w.get('[data-testid="zen-resp"]').attributes('style')).toContain(`--zen-resp-fade: ${ZEN_RESP_FADE_PX}px`)
    expect(w.find('[data-testid="zen-resp-fade"]').exists()).toBe(false)
    const css = src.slice(src.indexOf('<style'))
    // Every fade rule is a mask bounded by --zen-resp-fade
    const fades = css.match(/\.fade-(top|bottom)[^{]*\{[^}]*\}/g) || []
    expect(fades.length).toBeGreaterThanOrEqual(3)
    for (const rule of fades) {
      expect(rule).toMatch(/mask-image:/)
      expect(rule).toMatch(/var\(--zen-resp-fade\)/)
      expect(rule).not.toMatch(/opacity|color:/)
    }
  })

  it('prints paragraphs, lists, headings and code in light ink (not the theme text colour)', () => {
    const css = src.slice(src.indexOf('<style'))
    expect(css).toMatch(/\.zen-resp-md :deep\(p\),[\s\S]*?\{ color: inherit; \}/)
    // Colours are variables (light ink by default; the Circus prints them on paper)
    expect(css).toMatch(/--zr-ink: #fbf5e6;/)
    expect(css).toMatch(/--zr-strong: #fff3d0;/)
    expect(css).toMatch(/--zr-code: #f6e7c8;/)
    expect(css).toMatch(/--zr-link: #f2c46d;/)
    expect(css).toMatch(/:deep\(h1\)[\s\S]*?color: var\(--zr-strong\)/)
    expect(css).toMatch(/:deep\(code\) \{[^}]*color: var\(--zr-code\)/)
    expect(css).toMatch(/:deep\(a\) \{ color: var\(--zr-link\)/)
    // No literal colour left outside the variable block
    const rules = css.slice(css.indexOf('}'))
    expect(rules).not.toMatch(/color: #[0-9a-f]{3,6}/i)
  })

  it('on the Circus paper every ink passes WCAG AA (4.5:1)', () => {
    const zen = readFileSync(resolve(__dirname, '../../components/agents/ZenMode.vue'), 'utf8')
    const block = zen.match(/\.zen-response-card :deep\(\.zen-resp\) \{([^}]*)\}/)
    expect(block).not.toBeNull()
    const lum = (hex: string) => {
      const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
        .map(c => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4))
      return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!
    }
    const contrast = (a: string, b: string) => {
      const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p)
      return (x! + 0.05) / (y! + 0.05)
    }
    const paper = zen.match(/--zen-paper: (#[0-9a-f]{6});/)![1]!
    for (const name of ['ink', 'strong', 'link', 'link-hover', 'code']) {
      const ink = block![1]!.match(new RegExp(`--zr-${name}: (#[0-9a-f]{6});`))![1]!
      expect(contrast(ink, paper), name).toBeGreaterThanOrEqual(4.5)
    }
    // the small mono labels
    expect(contrast(zen.match(/--zen-ink-soft: (#[0-9a-f]{6});/)![1]!, paper)).toBeGreaterThanOrEqual(4.5)
  })

  it('every ink on the umber card passes WCAG AA (4.5:1)', () => {
    const lum = (hex: string) => {
      const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
        .map(c => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4))
      return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!
    }
    const contrast = (a: string, b: string) => {
      const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p)
      return (x! + 0.05) / (y! + 0.05)
    }
    const card = '#1c1610' // rgb(28, 22, 16), the Circus card base
    // body, headings/strong, links, link hover, code, list markers, label (white 60% on the card)
    for (const ink of ['#fbf5e6', '#fff3d0', '#f2c46d', '#ffd98a', '#f6e7c8', '#e8c98f', '#a4a19f']) {
      expect(contrast(ink, card)).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('panel layout: the whole reply scrolls, no Read more or Copy under it', async () => {
    mockHeights(900, 180)
    const w = mount(ZenResponseBody, { props: { text: md, layout: 'panel' } })
    await nextTick()
    expect(w.get('[data-testid="zen-resp"]').classes()).toContain('is-panel')
    expect(w.find('[data-testid="zen-resp-more"]').exists()).toBe(false)
    expect(w.find('[data-testid="zen-resp-copy"]').exists()).toBe(false)
    expect((w.get('[data-testid="zen-resp-box"]').element as HTMLElement).scrollTop).toBe(900)
  })

  it('follows the end of a streaming reply', async () => {
    mockHeights(900, 180)
    const w = mount(ZenResponseBody, { props: { text: 'a' } })
    await nextTick()
    await w.setProps({ text: 'a\n\nb' })
    await nextTick()
    expect((w.get('[data-testid="zen-resp-box"]').element as HTMLElement).scrollTop).toBe(900)
  })

  it('copies the raw text', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const w = mount(ZenResponseBody, { props: { text: md } })
    await w.get('[data-testid="zen-resp-copy"]').trigger('click')
    expect(writeText).toHaveBeenCalledWith(md)
  })
})
