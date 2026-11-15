/**
 * Makes commands and paths from subagent tool calls easier to read: drops a
 * leading `cd <dir> &&` (kept apart as the folder) and shortens home and agent
 * temp paths.
 */
import { shortenHomeIn } from '../homeDir'

// Claude Code's per-session temp folders: /private/tmp/claude-501/<project>/<session uuid>
const AGENT_TMP_RE = /\/(?:private\/)?tmp\/claude-\d+\/[^/\s'"]+\/[0-9a-f]{8}-[0-9a-f-]{27}/g
// A leading `cd <dir> &&` / `cd <dir>;`
const CD_RE = /^cd\s+("[^"]+"|'[^']+'|[^\s;&|]+)\s*(?:&&|;)\s*/

/** Shortens the home folder (once the server's is known) and agent temp paths: /Users/me/projects/x → ~/projects/x. */
export function tidyPaths(text: string): string {
  return shortenHomeIn(text.replace(AGENT_TMP_RE, '/tmp/…'))
}

export interface CleanCommand {
  /** The command without its leading `cd`, paths shortened. */
  command: string
  /** The folder it ran in when it started with `cd <dir> &&` (shortened), else ''. */
  cwd: string
}

/** `cd /Users/me/p && go test ./...` → { command: 'go test ./...', cwd: '~/p' } */
export function cleanCommand(raw: string): CleanCommand {
  let command = raw.trim()
  let cwd = ''
  for (let m = command.match(CD_RE); m; m = command.match(CD_RE)) {
    cwd = m[1]!.replace(/^["']|["']$/g, '')
    command = command.slice(m[0].length)
  }
  return { command: tidyPaths(command), cwd: tidyPaths(cwd) }
}

/** Splits a path for display: dimmed folder, emphasised name. */
export function splitPath(path: string): { dir: string; name: string } {
  const tidy = tidyPaths(path.trim())
  const cut = tidy.lastIndexOf('/', tidy.length - 2)
  return cut < 0 ? { dir: '', name: tidy } : { dir: tidy.slice(0, cut + 1), name: tidy.slice(cut + 1) }
}
