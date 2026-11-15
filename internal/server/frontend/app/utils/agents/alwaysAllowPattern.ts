// Builds the pattern for an "Allow similar" always-allow rule.
//
// The server refuses rules that cover everything (Bash(*), an empty prefix,
// Read(**), "/", the home directory, ...), so the pattern is always scoped:
// - Bash: the command's first word (skipping VAR=value assignments), e.g. "git".
//   The server matches whole words only and never auto-approves compound
//   commands (;, &&, |, $(...), redirections), so "git" does not cover
//   "git status; curl x | sh".
// - Read/Write/Edit: the directory of the file in the request.
// - Grep/Glob: read-only, any path.
export interface AlwaysAllowPattern {
  command_prefix?: string
  directory_path?: string
  path_pattern?: string
}

const ENV_ASSIGNMENT = /^[A-Za-z_][A-Za-z0-9_]*=/

export function bashCommandPrefix(command: unknown): string | undefined {
  if (typeof command !== 'string') return undefined
  const words = command.trim().split(/\s+/).filter(Boolean)
  const first = words.find(w => !ENV_ASSIGNMENT.test(w))
  return first || undefined
}

export function parentDirectory(filePath: unknown): string | undefined {
  if (typeof filePath !== 'string' || !filePath.startsWith('/')) return undefined
  const trimmed = filePath.replace(/\/+$/, '')
  const idx = trimmed.lastIndexOf('/')
  if (idx <= 0) return undefined // file directly under "/" — no safe directory scope
  return trimmed.slice(0, idx)
}

export function generateAlwaysAllowPattern(toolName: string, details: any): AlwaysAllowPattern {
  const pattern: AlwaysAllowPattern = {}
  switch (toolName) {
    case 'Bash': {
      const prefix = bashCommandPrefix(details?.command)
      if (prefix) pattern.command_prefix = prefix
      break
    }
    case 'Read':
    case 'Write':
    case 'Edit': {
      const dir = parentDirectory(details?.file_path)
      if (dir) pattern.directory_path = dir
      break
    }
    case 'Grep':
    case 'Glob':
      pattern.path_pattern = '*'
      break
  }
  return pattern
}
