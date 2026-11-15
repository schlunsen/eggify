import { parseUnifiedDiff, editSegments } from '../../utils/git/unifiedDiff'
import {
  defaultSelection, dirtySummary, displayPath, filterBranches, scopesFor, changesQuery, selectionSignature,
  type GitBranch, type GitOverview, type GitWorktree,
} from '../../utils/git/gitOverview'
import { folderInProject } from '../../composables/agents/useAgentRouteNavigation'

const DIFF = [
  'diff --git a/a.ts b/a.ts',
  'index 1..2 100644',
  '--- a/a.ts',
  '+++ b/a.ts',
  '@@ -1,4 +1,5 @@ function main() {',
  ' keep',
  '-const x = 1',
  '+const x = 2',
  '+++counter',
  '--flag',
  ' end',
  '\\ No newline at end of file',
  '@@ -20,2 +21,2 @@',
  ' tail',
].join('\n')

describe('parseUnifiedDiff', () => {
  const hunks = parseUnifiedDiff(DIFF)

  it('numbers lines per hunk and keeps the section name', () => {
    expect(hunks).toHaveLength(2)
    expect(hunks[0]!.section).toBe('function main() {')
    expect(hunks[0]!.lines.map(l => [l.kind, l.old, l.new])).toEqual([
      ['ctx', 1, 1], ['del', 2, null], ['add', null, 2], ['add', null, 3], ['del', 3, null], ['ctx', 4, 4], ['note', null, null],
    ])
    expect(hunks[1]!.lines[0]).toMatchObject({ kind: 'ctx', old: 20, new: 21, text: 'tail' })
  })

  it('keeps added lines that start with "++" and removed lines that start with "--"', () => {
    expect(hunks[0]!.lines[3]).toMatchObject({ kind: 'add', text: '++counter' })
    expect(hunks[0]!.lines[4]).toMatchObject({ kind: 'del', text: '-flag' })
  })

  it('marks the changed part of an edited line', () => {
    const del = hunks[0]!.lines[1]!, add = hunks[0]!.lines[2]!
    expect(del.segments).toEqual([{ text: 'const x = ', changed: false }, { text: '1', changed: true }])
    expect(add.segments).toEqual([{ text: 'const x = ', changed: false }, { text: '2', changed: true }])
    expect(editSegments('abc', 'xyz')).toBeNull()
  })

  it('handles empty input', () => {
    expect(parseUnifiedDiff('')).toEqual([])
    expect(parseUnifiedDiff(undefined)).toEqual([])
  })
})

const wt = (over: Partial<GitWorktree>): GitWorktree => ({
  path: '/p', head: 'abc', is_main: false, locked: false, prunable: false, missing: false,
  ahead: 0, behind: 0, base_ahead: 0, base_behind: 0, dirty: { staged: 0, unstaged: 0, untracked: 0, conflicted: 0 }, ...over,
})
const br = (over: Partial<GitBranch>): GitBranch => ({
  name: 'x', head: 'h', subject: 'fix login', author: 'Ada', at: 1, ahead: 0, behind: 0,
  base_ahead: 0, base_behind: 0, merged: false, current: false, is_default: false, ...over,
})
const overview = (worktrees: GitWorktree[], branches: GitBranch[] = []): GitOverview => ({
  root: '/p', current_branch: 'main', default_branch: 'main', worktrees, branches, recent_commits: [],
})

describe('git overview helpers', () => {
  it('opens on uncommitted work first, then a branch ahead of main', () => {
    const main = wt({ path: '/p', is_main: true, branch: 'main' })
    const busy = wt({ path: '/p/.worktrees/a', branch: 'a', dirty: { staged: 0, unstaged: 2, untracked: 0, conflicted: 0 } })
    expect(defaultSelection(overview([main, busy]))).toEqual({ sel: { worktree: '/p/.worktrees/a', branch: 'a' }, scope: 'uncommitted' })
    const ahead = wt({ path: '/p', is_main: true, branch: 'feat', base_ahead: 2 })
    expect(defaultSelection(overview([ahead])).scope).toBe('branch')
    expect(defaultSelection(overview([main])).scope).toBe('uncommitted')
  })

  it('offers only the scopes that make sense', () => {
    expect(scopesFor({ worktree: '/p', branch: 'main' }, 'main')).toEqual(['uncommitted'])
    expect(scopesFor({ worktree: '/p', branch: 'feat' }, 'main')).toEqual(['uncommitted', 'branch'])
    expect(scopesFor({ branch: 'feat' }, 'main')).toEqual(['branch'])
    expect(scopesFor({ branch: 'main' }, 'main')).toEqual([])
  })

  it('filters branches', () => {
    const list = [br({ name: 'main', is_default: true, merged: false }), br({ name: 'old', merged: true }), br({ name: 'new', subject: 'add cart' })]
    expect(filterBranches(list, 'active', '').map(b => b.name)).toEqual(['main', 'new'])
    expect(filterBranches(list, 'merged', '').map(b => b.name)).toEqual(['old'])
    expect(filterBranches(list, 'all', 'cart').map(b => b.name)).toEqual(['new'])
  })

  it('builds queries and signatures', () => {
    expect(changesQuery({ worktree: '/p/w', branch: 'a' }, 'uncommitted', 'main')).toBe('scope=uncommitted&worktree=%2Fp%2Fw')
    expect(changesQuery({ branch: 'a/b' }, 'branch', 'main', 'x y.ts')).toBe('scope=branch&ref=a%2Fb&base=main&path=x+y.ts')
    const o = overview([wt({ path: '/p', head: 'h1' })])
    const before = selectionSignature(o, { worktree: '/p' })
    o.worktrees[0]!.dirty!.untracked = 1
    expect(selectionSignature(o, { worktree: '/p' })).not.toBe(before)
  })

  it('formats paths and dirty counts', () => {
    expect(displayPath('/p/.worktrees/a', '/p')).toBe('.worktrees/a')
    expect(displayPath('/Users/me/x', '/p', '/Users/me')).toBe('~/x')
    expect(dirtySummary({ staged: 1, unstaged: 2, untracked: 3, conflicted: 0 })).toBe('1 staged · 2 modified · 3 new')
    expect(dirtySummary({ staged: 0, unstaged: 0, untracked: 0, conflicted: 0 })).toBe('Clean')
  })

  it('opens New session in a worktree only when it is inside the project', () => {
    expect(folderInProject('/p', '/p/.worktrees/a')).toBe('/p/.worktrees/a')
    expect(folderInProject('/p', '/etc')).toBe('/p')
    expect(folderInProject('/p', '/p/../etc')).toBe('/p')
    expect(folderInProject('/p', '/pother')).toBe('/p')
    expect(folderInProject('/p', '')).toBe('/p')
  })
})
