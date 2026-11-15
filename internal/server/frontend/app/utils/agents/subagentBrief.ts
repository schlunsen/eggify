/**
 * The parts of a subagent's run worth reading first, for the Subagents view's
 * detail pane: the steps that change something or went wrong (key steps), and
 * what the agent produced (pull requests, commits, pushed branches, changed
 * files). Everything else stays behind "All steps".
 *
 * Built from the timeline (subagentTimeline.ts), so only
 * what the server kept (tool lines and the first lines of each result) is seen.
 */
import type { TimelineEntry, TimelineTool } from './subagentTimeline'

const CHANGING_TOOLS = new Set(['Edit', 'MultiEdit', 'Write', 'NotebookEdit', 'FileChange'])
const LAUNCH_TOOLS = new Set(['Task', 'Agent'])

/** Shell commands that change shared state: git history, PRs, releases, deploys. */
const KEY_COMMAND_RE = new RegExp([
  String.raw`\bgit\s+(?:-C\s+\S+\s+)?(?:commit|push|merge|rebase|tag|cherry-pick|revert|reset\s+--hard|checkout\s+-b|switch\s+-c|branch\s+-[dDmM])\b`,
  String.raw`\bgh\s+(?:pr|issue|release|run|workflow)\s+(?:create|merge|close|reopen|comment|review|edit|rerun|run|delete)\b`,
  String.raw`\b(?:npm|pnpm|yarn)\s+publish\b`,
  // Flags may come first: `kubectl -n prod rollout restart ...`.
  String.raw`\bkubectl\b[^;&|]*?\s(?:apply|delete|rollout|set|scale|patch|replace|create)\b`,
  String.raw`\bhelm\b[^;&|]*?\s(?:install|upgrade|uninstall|rollback)\b`,
  String.raw`\bdocker\s+(?:push|compose\s+up)\b`,
  String.raw`\b(?:just|make)\s+[\w:-]*(?:deploy|release|publish)[\w:-]*`,
  String.raw`(?:^|[\s;&|/])deploy\.sh\b`,
].join('|'))

function toolName(entry: TimelineTool): string {
  return entry.tool.startsWith('mcp__') ? entry.tool.split('__').pop() || entry.tool : entry.tool
}

/** Whether a step changed something, launched an agent, or failed. */
export function isKeyStep(entry: TimelineTool): boolean {
  if (entry.failed) return true
  const name = toolName(entry)
  if (CHANGING_TOOLS.has(name) || LAUNCH_TOOLS.has(name)) return true
  return entry.detailKind === 'command' && KEY_COMMAND_RE.test(entry.detail)
}

export interface ProducedPR {
  number: string
  url: string
  /** "owner/repo" */
  repo: string
  /** "#12", or "repo#12" when PRs of several repositories are listed. */
  label: string
}
export interface ProducedCommit { sha: string; message: string; branch: string }

export interface SubagentProducts {
  prs: ProducedPR[]
  commits: ProducedCommit[]
  /** Branches pushed to a remote (not deleted ones). */
  branches: string[]
  /** Files written or edited, in the order first touched. */
  files: string[]
}

const PR_URL_RE = /https:\/\/github\.com\/([\w.-]+\/[\w.-]+)\/pull\/(\d+)/g
// `git commit` prints "[branch 1a2b3c4] message", "[branch (root-commit) 1a2b3c4] ..." or
// "[detached HEAD 1a2b3c4] ...".
const COMMIT_RE = /^\[(detached HEAD|[^\s\]]+)(?: \([^)]*\))? ([0-9a-f]{7,40})\] (.+)$/
// `git push` prints " * [new branch]  feat -> feat" or "   1a2b..3c4d  feat -> feat".
const PUSH_REF_RE = /(?:\[new branch\]|[0-9a-f]{6,}\.\.\.?[0-9a-f]{6,}|\+\s*[0-9a-f]{6,}\.\.\.[0-9a-f]{6,})\s+(\S+)\s+->\s+(\S+)/

/** The branch a `git push` command names, when its output did not say. */
function pushedBranchFromCommand(command: string): string {
  const match = command.match(/\bgit\s+(?:-C\s+\S+\s+)?push\b([^;&|]*)/)
  if (!match) return ''
  // Arguments end where a redirection starts (`2>&1`, `> log`).
  const argText = match[1]!.split(/\s\d*[<>]/)[0]!
  // `git push origin --delete feat` / `-d` / `origin :feat` delete a branch.
  if (/(?:^|\s)(?:--delete|-d)(?:\s|$)/.test(argText)) return ''
  const args = argText.trim().split(/\s+/).filter(arg => arg && !arg.startsWith('-'))
  // `git push origin feat` / `git push origin HEAD:feat`; a bare `git push` names no branch.
  if (args.length < 2) return ''
  const ref = args[args.length - 1]!
  if (ref.startsWith(':')) return ''
  const target = ref.includes(':') ? ref.split(':').pop()! : ref
  return target === 'HEAD' ? '' : target.replace(/^refs\/heads\//, '')
}

/** What the agent produced, from its steps. */
export function subagentProducts(timeline: TimelineEntry[]): SubagentProducts {
  const prs = new Map<string, ProducedPR>()
  const commits = new Map<string, ProducedCommit>()
  const branches = new Set<string>()
  const files = new Set<string>()

  const scanPRs = (text: string) => {
    for (const match of text.matchAll(PR_URL_RE)) {
      const key = `${match[1]}#${match[2]}`
      if (!prs.has(key)) prs.set(key, { repo: match[1]!, number: match[2]!, url: match[0], label: `#${match[2]}` })
    }
  }

  for (const entry of timeline) {
    if (entry.kind !== 'tool') continue
    const name = toolName(entry)
    if (CHANGING_TOOLS.has(name) && entry.detail && !entry.failed) files.add(entry.detail)
    if (entry.detailKind !== 'command' || entry.failed) continue
    // Only pull requests it opened: links in other output or the report may be ones it read.
    if (/\bgh\s+pr\s+create\b/.test(entry.detail)) scanPRs(entry.result.join('\n'))
    if (/\bgit\b[^;&|]*\bcommit\b/.test(entry.detail)) {
      for (const line of entry.result) {
        const match = line.trim().match(COMMIT_RE)
        if (match && !commits.has(match[2]!)) commits.set(match[2]!, { branch: match[1]!, sha: match[2]!, message: match[3]! })
      }
    }
    if (/\bgit\b[^;&|]*\bpush\b/.test(entry.detail)) {
      let found = false
      for (const line of entry.result) {
        const match = line.match(PUSH_REF_RE)
        if (match) { branches.add(match[2]!.replace(/^refs\/heads\//, '')); found = true }
      }
      if (!found) {
        const branch = pushedBranchFromCommand(entry.detail)
        if (branch) branches.add(branch)
      }
    }
  }
  const prList = [...prs.values()]
  if (new Set(prList.map(pr => pr.repo)).size > 1) {
    for (const pr of prList) pr.label = `${pr.repo.split('/').pop()}#${pr.number}`
  }

  return { prs: prList, commits: [...commits.values()], branches: [...branches], files: [...files] }
}

export function hasProducts(products: SubagentProducts): boolean {
  return products.prs.length + products.commits.length + products.branches.length + products.files.length > 0
}
