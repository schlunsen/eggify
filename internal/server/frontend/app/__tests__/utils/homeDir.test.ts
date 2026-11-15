import { beforeEach, describe, expect, it, vi } from 'vitest'
import { homeDir, loadHomeDir, setHomeDir, shortenHome, shortenHomeIn } from '~/utils/homeDir'
import { shortenPath } from '~/utils/projects/projectSetup'
import { tidyPaths } from '~/utils/agents/commandText'

beforeEach(() => setHomeDir(''))

describe('shortenHome', () => {
  it('shortens only the given home folder', () => {
    const home = '/Users/Shared/praline'
    expect(shortenHome('/Users/Shared/praline/projects/cheese-shop', home)).toBe('~/projects/cheese-shop')
    expect(shortenHome('/Users/Shared/praline', home)).toBe('~')
    expect(shortenHome('/Users/Shared/praline/', home)).toBe('~/')
    expect(shortenHome('/Users/Shared/mr.praline/x', home)).toBe('/Users/Shared/mr.praline/x')
    expect(shortenHome('/Users/Shared/praline2/x', home)).toBe('/Users/Shared/praline2/x')
    expect(shortenHome('/Users/Shared/x', home)).toBe('/Users/Shared/x')
    expect(shortenHome('/Users/other/x', home)).toBe('/Users/other/x')
    expect(shortenHome('/Users/Shared/mr.praline/projects/cheese-shop', '/Users/Shared/mr.praline')).toBe('~/projects/cheese-shop')
  })
  it('handles Linux, Windows and a trailing slash on home', () => {
    expect(shortenHome('/home/x/code', '/home/x')).toBe('~/code')
    expect(shortenHome('/home/y/code', '/home/x')).toBe('/home/y/code')
    expect(shortenHome('C:\\Users\\me\\code', 'C:\\Users\\me')).toBe('~\\code')
    expect(shortenHome('/Users/me/a', '/Users/me/')).toBe('~/a')
  })
  it('keeps the full path while the home is unknown', () => {
    expect(shortenHome('/Users/me/a', '')).toBe('/Users/me/a')
    expect(shortenHome('/Users/me/a', null)).toBe('/Users/me/a')
    expect(shortenHome('/Users/me/a')).toBe('/Users/me/a')
    expect(shortenHome('')).toBe('')
  })
  it('uses the shared home by default, in every helper', () => {
    setHomeDir('/Users/Shared/praline/')
    expect(homeDir.value).toBe('/Users/Shared/praline')
    expect(shortenHome('/Users/Shared/praline/.claude/skills/x')).toBe('~/.claude/skills/x')
    expect(shortenPath('/Users/Shared/praline/projects/a')).toBe('~/projects/a')
    expect(shortenPath('/Users/Shared/y')).toBe('/Users/Shared/y')
    expect(tidyPaths('ls /Users/Shared/praline/a /Users/Shared/b')).toBe('ls ~/a /Users/Shared/b')
  })
})

describe('shortenHomeIn', () => {
  it('shortens whole paths in a text', () => {
    expect(shortenHomeIn('cat /Users/me/a.txt /Users/meow/b', '/Users/me')).toBe('cat ~/a.txt /Users/meow/b')
    expect(shortenHomeIn('S=/Users/me', '/Users/me')).toBe('S=~')
    expect(shortenHomeIn('cat /Users/me/a', '')).toBe('cat /Users/me/a')
  })
  it('escapes the home in its pattern', () => {
    expect(shortenHomeIn('ls /Users/mr.praline/x /Users/mrXpraline/x', '/Users/mr.praline')).toBe('ls ~/x /Users/mrXpraline/x')
  })
})

describe('loadHomeDir', () => {
  it('asks /api/system-info once and keeps the answer', async () => {
    const fetcher = vi.fn(async () => ({ ok: true, json: async () => ({ system: { home: '/Users/Shared/praline' } }) }) as Response)
    await Promise.all([loadHomeDir(fetcher), loadHomeDir(fetcher)])
    await loadHomeDir(fetcher)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher).toHaveBeenCalledWith('/api/system-info')
    expect(homeDir.value).toBe('/Users/Shared/praline')
  })
  it('stays unknown when the request fails, and tries again later', async () => {
    const bad = vi.fn(async () => { throw new Error('offline') })
    await loadHomeDir(bad)
    expect(homeDir.value).toBe('')
    const ok = vi.fn(async () => ({ ok: true, json: async () => ({ system: { home: '/home/x' } }) }) as Response)
    await loadHomeDir(ok)
    expect(homeDir.value).toBe('/home/x')
  })
})
