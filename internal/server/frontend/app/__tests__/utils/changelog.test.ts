import {
  CHANGELOG_SEEN_KEY, compareVersions, fetchChangelog, filterReleases, hasUnseenChanges,
  markChangelogSeen, sectionKind, type ChangelogRelease,
} from '../../utils/changelog'

beforeEach(() => localStorage.clear())

describe('compareVersions', () => {
  it('compares numerically, not as text', () => {
    expect(compareVersions('2.21.0', '2.20.21')).toBe(1)
    expect(compareVersions('2.9.0', '2.10.0')).toBe(-1)
    expect(compareVersions('v1.0', '1.0.0')).toBe(0)
    expect(compareVersions('1.0.0-rc1', '1.0.0')).toBe(-1)
    expect(compareVersions('1.0.0-rc2', '1.0.0-rc10')).toBe(-1)
    expect(compareVersions('1.0.0-rc.2', '1.0.0-rc.10')).toBe(-1)
    expect(compareVersions('1.0.0-1', '1.0.0-a')).toBe(-1)
    expect(compareVersions('1.0.0-a', '1.0.0-a.1')).toBe(-1)
  })
})

describe('last seen version', () => {
  it('stores the current version silently on the first load', () => {
    expect(hasUnseenChanges('2.21.0')).toBe(false)
    expect(localStorage.getItem(CHANGELOG_SEEN_KEY)).toBe('2.21.0')
  })

  it('flags a newer running version until it is marked seen', () => {
    localStorage.setItem(CHANGELOG_SEEN_KEY, '2.20.0')
    expect(hasUnseenChanges('2.21.0')).toBe(true)
    expect(hasUnseenChanges('2.21.0')).toBe(true)
    markChangelogSeen('2.21.0')
    expect(hasUnseenChanges('2.21.0')).toBe(false)
  })

  it('does not flag an older running version (downgrade)', () => {
    localStorage.setItem(CHANGELOG_SEEN_KEY, '2.22.0')
    expect(hasUnseenChanges('2.21.0')).toBe(false)
  })

  it('survives blocked storage', () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    expect(hasUnseenChanges('2.21.0')).toBe(false)
    expect(() => markChangelogSeen('2.21.0')).not.toThrow()
    spy.mockRestore()
    set.mockRestore()
  })
})

describe('fetchChangelog', () => {
  it('passes limit and since and fills defaults', async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ current: '2.21.0', total: 1, releases: [] }) })
    const out = await fetchChangelog(fetcher, { limit: 5, since: '2.20.0' })
    expect(fetcher).toHaveBeenCalledWith('/api/changelog?limit=5&since=2.20.0')
    expect(out).toEqual({ current: '2.21.0', total: 1, releases: [] })
  })

  it('throws on an error status', async () => {
    await expect(fetchChangelog(async () => ({ ok: false, status: 500 }) as Response)).rejects.toThrow('500')
  })
})

describe('filterReleases', () => {
  const releases: ChangelogRelease[] = [
    { version: '2.0.0', date: '2026-01-01', sections: [{ title: 'Added', items: ['Dark mode', 'Teams'] }, { title: 'Fixed', items: ['Crash'] }] },
    { version: '1.0.0', date: '2025-01-01', sections: [{ title: 'Added', items: ['First'] }] },
  ]
  it('keeps matching items only', () => {
    const out = filterReleases(releases, 'teams')
    expect(out).toHaveLength(1)
    expect(out[0]!.sections).toEqual([{ title: 'Added', items: ['Teams'] }])
  })
  it('keeps a whole release when its version matches', () => {
    expect(filterReleases(releases, '1.0.0')[0]).toBe(releases[1])
  })
  it('returns everything for an empty filter', () => {
    expect(filterReleases(releases, '  ')).toBe(releases)
  })
})

it('sectionKind maps titles to a colour family', () => {
  expect(sectionKind('Added')).toBe('added')
  expect(sectionKind('Changed - BREAKING')).toBe('changed')
  expect(sectionKind('Technical')).toBe('other')
})
