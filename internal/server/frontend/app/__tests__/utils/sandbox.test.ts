import {
  sandboxSupportForProvider,
  parseSandboxDomains,
  isValidSandboxDomain,
  isSessionSandboxed,
} from '../../utils/agents/sandbox'

describe('sandboxSupportForProvider', () => {
  it('supports the Claude CLI and Codex providers', () => {
    for (const p of ['claude', 'deepseek', 'glm', 'kimi', 'codex', 'CLAUDE', undefined, null, '']) {
      expect(sandboxSupportForProvider(p as any)).toBe('supported')
    }
  })
  it('refuses OpenAI-compatible providers', () => {
    for (const p of ['openai', 'ollama', 'openrouter']) {
      expect(sandboxSupportForProvider(p)).toBe('unsupported')
    }
  })
  it('custom providers depend on their URL', () => {
    expect(sandboxSupportForProvider('custom')).toBe('depends')
  })
})

describe('parseSandboxDomains', () => {
  it('normalises, de-duplicates and sorts', () => {
    const { domains, invalid } = parseSandboxDomains(' GitHub.com\npypi.org, github.com  *.Example.org\n\n# comment\nregistry.npmjs.org. # trailing')
    expect(invalid).toEqual([])
    expect(domains).toEqual(['*.example.org', 'github.com', 'pypi.org', 'registry.npmjs.org'])
  })
  it('reports entries that are not host names', () => {
    const { domains, invalid } = parseSandboxDomains('https://github.com\nlocalhost\n*\n10.0.0.1\nok.dev')
    expect(domains).toEqual(['ok.dev'])
    expect(invalid).toEqual(['https://github.com', 'localhost', '*', '10.0.0.1'])
  })
  it('mirrors the server-side domain rule', () => {
    expect(isValidSandboxDomain('*.github.io')).toBe(true)
    expect(isValidSandboxDomain('*.com')).toBe(false)
    expect(isValidSandboxDomain('host:443')).toBe(false)
  })
})

describe('isSessionSandboxed', () => {
  it('is true only for an explicit flag', () => {
    expect(isSessionSandboxed({ options: { sandboxed: true } })).toBe(true)
    expect(isSessionSandboxed({ options: { sandboxed: false } })).toBe(false)
    expect(isSessionSandboxed({ options: {} })).toBe(false)
    expect(isSessionSandboxed(null)).toBe(false)
  })
})
