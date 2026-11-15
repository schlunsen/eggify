import { computed, readonly, ref, onMounted } from 'vue'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { availableThemes, DEFAULT_THEME, pairedTheme, type ThemeVariant } from '../../composables/useTheme'

// useTheme relies on Nuxt auto-imports; provide them for the test
const states = new Map<string, ReturnType<typeof ref>>()
beforeEach(() => {
  states.clear()
  vi.stubGlobal('useState', (key: string, init: () => unknown) => {
    if (!states.has(key)) states.set(key, ref(init()))
    return states.get(key)
  })
  vi.stubGlobal('computed', computed)
  vi.stubGlobal('readonly', readonly)
  vi.stubGlobal('onMounted', onMounted)
})
afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
})

async function freshUseTheme() {
  const mod = await import('../../composables/useTheme')
  return mod.useTheme()
}

// vitest runs from the frontend root
const readCss = (file: string) => readFileSync(resolve(process.cwd(), 'assets/css', file), 'utf8')

describe('theme registry', () => {
  it('registers Puppet Theatre as a light/dark pair', () => {
    const light = availableThemes.find(t => t.id === 'theatre-light')
    const dark = availableThemes.find(t => t.id === 'theatre-dark')
    expect(light).toMatchObject({ name: 'Puppet Theatre Light', isDark: false, fontFamily: 'Fraunces' })
    expect(dark).toMatchObject({ name: 'Puppet Theatre Dark', isDark: true, fontFamily: 'Fraunces' })
  })

  it('has unique ids and every theme has both variants', () => {
    const ids = availableThemes.map(t => t.id)
    expect(new Set(ids).size).toBe(ids.length)
    for (const t of availableThemes) {
      const counterpart = pairedTheme(t.id)
      const other = availableThemes.find(x => x.id === counterpart)
      expect(other, `${t.id} has no counterpart`).toBeDefined()
      expect(other!.isDark).toBe(!t.isDark)
    }
  })

  it('defines CSS variables for both Puppet Theatre variants and imports the file', () => {
    const css = readCss('themes/theatre.css')
    expect(css).toContain('[data-theme="theatre-light"]')
    expect(css).toContain('[data-theme="theatre-dark"]')
    const main = readCss('main.css')
    expect(main).toContain("@import './themes/theatre.css';")
  })
})

describe('default theme', () => {
  it('is Puppet Theatre Light for new users', async () => {
    expect(DEFAULT_THEME).toBe('theatre-light')
    const { currentTheme } = await freshUseTheme()
    expect(currentTheme.value).toBe('theatre-light')
  })

  it('keeps a stored choice', async () => {
    localStorage.setItem('eggify-theme', 'nord-dark')
    const { currentTheme } = await freshUseTheme()
    expect(currentTheme.value).toBe('nord-dark')
  })

  it('ignores an unknown stored theme', async () => {
    localStorage.setItem('eggify-theme', 'no-such-theme')
    const { currentTheme } = await freshUseTheme()
    expect(currentTheme.value).toBe(DEFAULT_THEME)
  })
})

describe('dark/light toggle', () => {
  it.each<[ThemeVariant, ThemeVariant]>([
    ['theatre-light', 'theatre-dark'],
    ['theatre-dark', 'theatre-light'],
    ['flyingcircus-light', 'flyingcircus-dark'],
    ['eggify-dark', 'eggify-light'],
    ['neon-light', 'neon-dark'],
    ['default-dark', 'default-light']
  ])('pairs %s with %s', (from, to) => {
    expect(pairedTheme(from)).toBe(to)
  })

  it('toggles within the family and persists the choice', async () => {
    const theme = await freshUseTheme()
    theme.toggleDarkMode()
    expect(theme.currentTheme.value).toBe('theatre-dark')
    expect(theme.isDark.value).toBe(true)
    expect(document.documentElement.getAttribute('data-theme')).toBe('theatre-dark')
    expect(localStorage.getItem('eggify-theme')).toBe('theatre-dark')
    theme.toggleDarkMode()
    expect(theme.currentTheme.value).toBe('theatre-light')
  })
})
