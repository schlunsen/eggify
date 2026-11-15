import { describe, it, expect } from 'vitest'
import { cleanCommand, latestTool, plainLine, splitPath, subagentTimeline, tidyPaths } from '~/utils/agents/subagentTimeline'

describe('cleanCommand', () => {
  it('drops a leading cd and keeps the folder apart', () => {
    expect(cleanCommand('cd /Users/me/projects/app && go test ./...')).toEqual({ command: 'go test ./...', cwd: '~/projects/app' })
    expect(cleanCommand('cd "/home/me/my app"; ls')).toEqual({ command: 'ls', cwd: '~/my app' })
    expect(cleanCommand('cd a && cd b && make')).toEqual({ command: 'make', cwd: 'b' })
  })
  it('leaves other commands alone apart from paths', () => {
    expect(cleanCommand('git status')).toEqual({ command: 'git status', cwd: '' })
    expect(cleanCommand('cat /Users/me/a.txt /etc/hosts')).toEqual({ command: 'cat ~/a.txt /etc/hosts', cwd: '' })
  })
})

describe('tidyPaths', () => {
  it('shortens agent temp folders and home', () => {
    expect(tidyPaths('S=/private/tmp/claude-501/-Users-me-projects-x/d565512d-bfc2-4e7d-9584-e713dcfdab03/tasks/a.out'))
      .toBe('S=/tmp/…/tasks/a.out')
    expect(tidyPaths('/Users/me')).toBe('~')
    expect(tidyPaths('/Users/me2x/file /Usersfoo')).toBe('~/file /Usersfoo')
  })
})

it('splitPath separates folder and name', () => {
  expect(splitPath('/Users/me/p/src/a.ts')).toEqual({ dir: '~/p/src/', name: 'a.ts' })
  expect(splitPath('a.ts')).toEqual({ dir: '', name: 'a.ts' })
})

describe('subagentTimeline', () => {
  const lines = [
    'Running in the background',
    'TOOL:Bash: cd /Users/me/p && go test ./...',
    'RESULT_START', 'FAIL ./x', '[exited with code 1]', 'RESULT_END',
    'TOOL:Read: /Users/me/p/a.go',
    'RESULT_START', 'package a', 'RESULT_END',
    'All done, tests fixed.'
  ]
  it('pairs tool calls with results, flags failures and keeps notes', () => {
    const entries = subagentTimeline(lines)
    expect(entries.map(e => e.kind)).toEqual(['note', 'tool', 'tool', 'note'])
    const bash = entries[1] as any
    expect(bash).toMatchObject({ n: 1, label: 'Shell', detailKind: 'command', detail: 'go test ./...', cwd: '~/p', failed: true, result: ['FAIL ./x', '[exited with code 1]'] })
    expect(entries[2]).toMatchObject({ n: 2, label: 'Read', detailKind: 'file', detail: '~/p/a.go', failed: false })
    expect(latestTool(entries)).toBe(entries[2])
  })
  it('drops the closing report note when asked', () => {
    expect(subagentTimeline(lines, true).map(e => e.kind)).toEqual(['note', 'tool', 'tool'])
  })

  // One time per line (unix ms): note, Bash call, its result (4 lines), Read, its result (3), note.
  const times = [1000, 2000, 2100, 2100, 2100, 4300, 5000, 5050, 5050, 5050, 9000]
  it('dates each step by its first line and times results by their end', () => {
    const entries = subagentTimeline(lines, false, times) as any[]
    expect(entries[0]).toMatchObject({ kind: 'note', at: 1000 })
    expect(entries[1]).toMatchObject({ at: 2000, endedAt: 4300, durationMs: 2300 })
    expect(entries[2]).toMatchObject({ at: 5000, endedAt: 5050, durationMs: 50 })
    expect(entries[3]).toMatchObject({ kind: 'note', at: 9000 })
  })
  it('leaves times out when they are unknown', () => {
    const unknown = subagentTimeline(lines, false, times.map((t, i) => (i === 1 || i === 5 ? 0 : t))) as any[]
    expect(unknown[1].at).toBeUndefined()
    expect(unknown[1].durationMs).toBeUndefined()
    expect(unknown[2]).toMatchObject({ at: 5000, durationMs: 50 })
    for (const entry of subagentTimeline(lines) as any[]) {
      expect(entry.at).toBeUndefined()
      expect(entry.durationMs).toBeUndefined()
    }
    // Times that don't line up with the lines are ignored.
    expect((subagentTimeline(lines, false, [1, 2, 3]) as any[])[1].at).toBeUndefined()
  })
  it('has no end for a result still being written', () => {
    const open = subagentTimeline(['TOOL:Bash: sleep 5', 'RESULT_START', 'zzz'], false, [100, 200, 300]) as any[]
    expect(open[0]).toMatchObject({ at: 100, result: ['zzz'] })
    expect(open[0].durationMs).toBeUndefined()
  })
})

it('plainLine strips markdown', () => {
  expect(plainLine('The **handler** in `a.go`, see [docs](http://x).\n- next')).toBe('The handler in a.go, see docs. next')
})
