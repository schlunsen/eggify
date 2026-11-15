import { describe, expect, it } from 'vitest'
import { hasProducts, isKeyStep, subagentProducts } from '~/utils/agents/subagentBrief'
import { subagentTimeline, type TimelineTool } from '~/utils/agents/subagentTimeline'

const tools = (lines: string[]) => subagentTimeline(lines).filter((e): e is TimelineTool => e.kind === 'tool')

describe('isKeyStep', () => {
  it('keeps changes, launches, state-changing commands and failures', () => {
    const steps = tools([
      'TOOL:Write: /a/new.ts',
      'TOOL:Agent: Review the diff',
      'TOOL:Bash: cd /repo && git push origin main',
      'TOOL:Bash: kubectl -n prod rollout restart deploy/api',
      'TOOL:Bash: ./scripts/deploy.sh staging',
      'TOOL:Bash: go test ./...', 'RESULT_START', 'exit status 1', 'RESULT_END',
    ])
    expect(steps.map(isKeyStep)).toEqual([true, true, true, true, true, true])
  })

  it('leaves out reading and looking around', () => {
    const steps = tools([
      'TOOL:Read: /a/b.ts', 'TOOL:Grep: deploy', 'TOOL:Bash: grep -rn deploy .', 'TOOL:Bash: git status', 'TOOL:Bash: gh pr view 12',
    ])
    expect(steps.map(isKeyStep)).toEqual([false, false, false, false, false])
  })
})

describe('subagentProducts', () => {
  it('takes the pushed branch from the command when the output was not kept', () => {
    const products = subagentProducts(subagentTimeline(['TOOL:Bash: git push -u origin HEAD:feature/x']))
    expect(products.branches).toEqual(['feature/x'])
  })

  it('lists the pull requests it opened, once each, and not ones it only read', () => {
    const lines = [
      'TOOL:Bash: gh pr view 12', 'RESULT_START', 'https://github.com/a/b/pull/12', 'RESULT_END',
      'TOOL:Bash: gh pr create --fill', 'RESULT_START', 'https://github.com/a/b/pull/7', 'RESULT_END',
      'TOOL:Bash: gh pr create --fill', 'RESULT_START', 'a pull request already exists: https://github.com/a/b/pull/9', 'Exit code 1', 'RESULT_END',
      'TOOL:Bash: gh pr create --title again', 'RESULT_START', 'https://github.com/a/b/pull/7', 'RESULT_END',
    ]
    expect(subagentProducts(subagentTimeline(lines)).prs).toEqual([{ repo: 'a/b', number: '7', url: 'https://github.com/a/b/pull/7', label: '#7' }])
  })

  it('keeps same-numbered pull requests of different repositories apart', () => {
    const lines = [
      'TOOL:Bash: gh pr create -R acme/api', 'RESULT_START', 'https://github.com/acme/api/pull/42', 'RESULT_END',
      'TOOL:Bash: gh pr create -R acme/web', 'RESULT_START', 'https://github.com/acme/web/pull/42', 'RESULT_END',
    ]
    expect(subagentProducts(subagentTimeline(lines)).prs.map(pr => pr.label)).toEqual(['api#42', 'web#42'])
  })

  it('does not count a deleted branch as pushed', () => {
    for (const cmd of ['git push origin --delete feat', 'git push -d origin feat', 'git push origin :feat']) {
      expect(subagentProducts(subagentTimeline([`TOOL:Bash: ${cmd}`])).branches).toEqual([])
    }
  })

  it('does not take a redirect for the pushed branch', () => {
    const lines = ['TOOL:Bash: git push origin main 2>&1', 'RESULT_START', 'Everything up-to-date', 'RESULT_END']
    expect(subagentProducts(subagentTimeline(lines)).branches).toEqual(['main'])
    expect(subagentProducts(subagentTimeline(['TOOL:Bash: git push 2>&1 | tail -2'])).branches).toEqual([])
  })

  it('reads commits made on a detached HEAD', () => {
    const lines = ['TOOL:Bash: git commit -m fix', 'RESULT_START', '[detached HEAD 1a2b3c4] fix', 'RESULT_END']
    expect(subagentProducts(subagentTimeline(lines)).commits).toEqual([{ branch: 'detached HEAD', sha: '1a2b3c4', message: 'fix' }])
  })

  it('ignores failed edits and commits, and reports nothing for a read-only run', () => {
    const products = subagentProducts(subagentTimeline([
      'TOOL:Edit: /a/x.ts', 'RESULT_START', '<tool_use_error>String not found</tool_use_error>', 'RESULT_END',
      'TOOL:Bash: git commit -m wip', 'RESULT_START', 'error: nothing to commit', 'RESULT_END',
      'TOOL:Read: /a/y.ts',
    ]))
    expect(products).toEqual({ prs: [], commits: [], branches: [], files: [] })
    expect(hasProducts(products)).toBe(false)
  })
})
