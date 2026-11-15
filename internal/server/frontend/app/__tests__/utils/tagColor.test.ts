import { fitTags, tagChipStyle, tagHash, tagHue } from '../../utils/tagColor'

describe('tag colours', () => {
  it('gives a tag the same hue whatever its case or surrounding spaces', () => {
    expect(tagHue('Bug')).toBe(tagHue('bug'))
    expect(tagHue('  bug ')).toBe(tagHue('bug'))
    expect(tagChipStyle('Refactor')).toEqual({ '--tag-hue': String(tagHue('refactor')) })
  })

  it('keeps hues in 0..359 and spreads different tags apart', () => {
    const tags = ['bug', 'refactor', 'ios', 'sidebar', 'release', 'docs', 'perf', 'ui', 'security', 'tests']
    const hues = tags.map(tagHue)
    for (const h of hues) {
      expect(h).toBeGreaterThanOrEqual(0)
      expect(h).toBeLessThan(360)
      expect(Number.isInteger(h)).toBe(true)
    }
    expect(new Set(hues).size).toBeGreaterThan(7)
    expect(tagHash('')).toBe(0x811c9dc5)
  })
})

describe('fitTags', () => {
  it('keeps adding tags while they fit and hides the rest', () => {
    expect(fitTags(['bug', 'ios', 'release-train'], 12)).toEqual({ shown: ['bug', 'ios'], hidden: ['release-train'] })
  })

  it('always shows the first tag, however long', () => {
    expect(fitTags(['a-very-long-tag-name-indeed', 'x'], 10)).toEqual({ shown: ['a-very-long-tag-name-indeed'], hidden: ['x'] })
  })

  it('stops at the maximum number of chips', () => {
    expect(fitTags(['a', 'b', 'c', 'd', 'e'], 100, 3)).toEqual({ shown: ['a', 'b', 'c'], hidden: ['d', 'e'] })
    expect(fitTags([], 10)).toEqual({ shown: [], hidden: [] })
  })
})
