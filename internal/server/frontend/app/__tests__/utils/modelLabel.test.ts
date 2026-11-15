import { describe, it, expect } from 'vitest'
import { modelLabel, modelTitle, modelFullName } from '~/utils/agents/modelLabel'

describe('modelLabel', () => {
  it.each([
    ['claude-haiku-4-5-20251001', 'Haiku 4.5'],
    ['claude-sonnet-5-5', 'Sonnet 5.5'],
    ['claude-opus-5-5', 'Opus 5.5'],
    ['claude-fable-5-5', 'Fable 5.5'],
    ['claude-fable-5-2', 'Fable 5.2'],
    ['claude-mythos-5-2', 'Mythos 5.2'],
    ['claude-mythos-5-1', 'Mythos 5.1'],
    ['claude-sonnet-4-20250514', 'Sonnet 4'],
    ['claude-3-5-sonnet-20241022', 'Sonnet 3.5'],
    ['claude-opus-4-1[1m]', 'Opus 4.1'],
    ['anthropic/claude-sonnet-4-5', 'Sonnet 4.5'],
    ['us.anthropic.claude-haiku-4-5-20251001-v1:0', 'Haiku 4.5'],
    ['sonnet', 'Sonnet'],
    ['opus', 'Opus'],
    ['haiku', 'Haiku'],
    ['gpt-6-astra', 'gpt-6-astra'],
    ['', ''],
  ])('%s → %s', (id, want) => {
    expect(modelLabel(id)).toBe(want)
  })

  it('shortens unknown long ids', () => {
    const label = modelLabel('some-provider/a-really-long-model-name-that-goes-on-and-on')
    expect(label.length).toBeLessThanOrEqual(24)
    expect(label.endsWith('…')).toBe(true)
  })

  it('says when a model is only requested', () => {
    expect(modelTitle('haiku', 'requested')).toMatch(/requested/)
    expect(modelTitle('claude-haiku-4-5-20251001', 'actual')).toBe('Runs on claude-haiku-4-5-20251001')
    expect(modelTitle(undefined)).toBe('')
  })
})

describe('modelFullName', () => {
  it('names DeepSeek models whose ids hide the version', () => {
    expect(modelFullName('deepseek-flash')).toBe('V4.1 Flash')
    expect(modelFullName('deepseek-v4-pro')).toBe('V4 Pro')
    expect(modelFullName('sonnet')).toBe('')
    expect(modelFullName(undefined)).toBe('')
  })
})
