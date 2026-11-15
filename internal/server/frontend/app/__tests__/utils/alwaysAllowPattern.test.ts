import { bashCommandPrefix, generateAlwaysAllowPattern, parentDirectory } from '../../utils/agents/alwaysAllowPattern'

describe('always-allow "Allow similar" pattern', () => {
  it('uses the first word of a Bash command', () => {
    expect(generateAlwaysAllowPattern('Bash', { command: 'git status --short' })).toEqual({ command_prefix: 'git' })
    expect(generateAlwaysAllowPattern('Bash', { command: '  npm test' })).toEqual({ command_prefix: 'npm' })
  })

  it('skips leading VAR=value assignments', () => {
    expect(bashCommandPrefix('FOO=1 BAR=2 go test ./...')).toBe('go')
  })

  it('never produces a wildcard Bash prefix', () => {
    expect(generateAlwaysAllowPattern('Bash', {})).toEqual({})
    expect(generateAlwaysAllowPattern('Bash', { command: '   ' })).toEqual({})
    expect(generateAlwaysAllowPattern('Bash', { command: 'FOO=1' })).toEqual({})
  })

  it('scopes file rules to the directory of the file', () => {
    expect(generateAlwaysAllowPattern('Read', { file_path: '/home/me/project/src/main.go' }))
      .toEqual({ directory_path: '/home/me/project/src' })
    expect(generateAlwaysAllowPattern('Edit', { file_path: '/home/me/project/README.md' }))
      .toEqual({ directory_path: '/home/me/project' })
  })

  it('never produces a wildcard or root file scope', () => {
    expect(generateAlwaysAllowPattern('Write', {})).toEqual({})
    expect(generateAlwaysAllowPattern('Write', { file_path: 'relative.txt' })).toEqual({})
    expect(parentDirectory('/etc')).toBeUndefined()
    expect(parentDirectory('/')).toBeUndefined()
  })

  it('keeps Grep/Glob as read-only wildcards', () => {
    expect(generateAlwaysAllowPattern('Grep', { pattern: 'x' })).toEqual({ path_pattern: '*' })
  })
})
