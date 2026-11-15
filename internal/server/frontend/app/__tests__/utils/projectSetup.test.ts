import {
  filterProjects, folderNameError, folderNameFromPath, folderNameFromProjectName, guessRepo,
  projectInitials, relativeTime, shortenPath, sortProjects,
} from '../../utils/projects/projectSetup'

describe('projectSetup helpers', () => {
  it('shortens paths under home', () => {
    expect(shortenPath('/Users/me/code/app', '/Users/me')).toBe('~/code/app')
    expect(shortenPath('/Users/me', '/Users/me/')).toBe('~')
    expect(shortenPath('/Users/meow/x', '/Users/me')).toBe('/Users/meow/x')
    expect(shortenPath('/opt/x', '')).toBe('/opt/x')
  })

  it('derives folder and project names', () => {
    expect(folderNameFromPath('~/code/my-app/')).toBe('my-app')
    expect(folderNameFromPath('~')).toBe('')
    expect(folderNameFromProjectName('My Café App!')).toBe('my-cafe-app')
    expect(folderNameFromProjectName('..evil..')).toBe('evil')
    expect(folderNameError('ok-name')).toBeNull()
    expect(folderNameError('../x')).not.toBeNull()
    expect(folderNameError('.hidden')).not.toBeNull()
  })

  it('guesses repositories from what people paste', () => {
    expect(guessRepo('octocat/Hello-World')).toEqual({ name: 'Hello-World', slug: 'octocat/Hello-World' })
    expect(guessRepo('https://github.com/vuejs/core.git')).toEqual({ name: 'core', slug: 'vuejs/core' })
    expect(guessRepo('git@github.com:golang/go.git')).toEqual({ name: 'go', slug: 'golang/go' })
    expect(guessRepo('github.com/a/b/')?.name).toBe('b')
    expect(guessRepo('just words')).toBeNull()
    expect(guessRepo('')).toBeNull()
  })

  it('formats relative times', () => {
    const now = Date.parse('2026-10-03T12:00:00Z')
    expect(relativeTime('2026-10-03 11:59:50', now)).toBe('just now')
    expect(relativeTime('2026-10-03T11:30:00Z', now)).toBe('30 min ago')
    expect(relativeTime('2026-10-02 12:00:00', now)).toBe('yesterday')
    expect(relativeTime('', now)).toBe('')
    expect(relativeTime('garbage', now)).toBe('')
  })

  it('sorts and filters', () => {
    const list = [
      { id: 'a', name: 'zeta', path: '/z', last_activity: '2026-01-01 00:00:00' },
      { id: 'b', name: 'Alpha', path: '/a', last_activity: '2026-03-01 00:00:00' },
      { id: 'c', name: 'beta 10', path: '/b', created_at: '2025-01-01T00:00:00Z', running_count: 1 },
      { id: 'd', name: 'beta 9', path: '/b9', git: { is_repo: true, branch: 'feature/login' } },
    ]
    expect(sortProjects(list, 'recent').map(p => p.id)).toEqual(['c', 'b', 'a', 'd'])
    expect(sortProjects(list, 'name').map(p => p.id)).toEqual(['b', 'd', 'c', 'a'])
    expect(filterProjects(list, 'BETA').map(p => p.id)).toEqual(['c', 'd'])
    expect(filterProjects(list, 'login').map(p => p.id)).toEqual(['d'])
    expect(projectInitials('my-cool app')).toBe('MC')
    expect(projectInitials('eggify')).toBe('EG')
  })
})
