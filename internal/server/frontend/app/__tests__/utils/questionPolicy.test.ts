import {
  autoPickLabels,
  describeDefaultPolicy,
  formatCountdown,
  formatWait,
  msUntil,
  parseQuestionPolicy,
  parseRecommended,
  policyFromKey,
  policyKey,
  questionPolicyApplies,
  questionPolicyChoices,
  questionPolicyLabel,
  samePolicy,
} from '~/utils/agents/questionPolicy'

describe('parseQuestionPolicy', () => {
  it('accepts the three modes and drops a timeout outside auto_after', () => {
    expect(parseQuestionPolicy({ mode: 'wait', timeout_seconds: 5 })).toEqual({ mode: 'wait' })
    expect(parseQuestionPolicy({ mode: 'auto' })).toEqual({ mode: 'auto' })
    expect(parseQuestionPolicy({ mode: 'auto_after', timeout_seconds: 300 })).toEqual({ mode: 'auto_after', timeout_seconds: 300 })
  })

  it('rejects garbage and clamps odd timeouts', () => {
    expect(parseQuestionPolicy(null)).toBeNull()
    expect(parseQuestionPolicy('auto')).toBeNull()
    expect(parseQuestionPolicy({ mode: 'later' })).toBeNull()
    expect(parseQuestionPolicy({ mode: 'auto_after' })).toEqual({ mode: 'auto_after', timeout_seconds: 120 })
    expect(parseQuestionPolicy({ mode: 'auto_after', timeout_seconds: 2 })).toEqual({ mode: 'auto_after', timeout_seconds: 10 })
    expect(parseQuestionPolicy({ mode: 'auto_after', timeout_seconds: 1e9 })).toEqual({ mode: 'auto_after', timeout_seconds: 86400 })
  })
})

it('labels policies in plain words', () => {
  expect(questionPolicyLabel({ mode: 'wait' })).toBe('Wait for me')
  expect(questionPolicyLabel({ mode: 'auto_after', timeout_seconds: 120 })).toBe('Auto-pick after 2 min')
  expect(questionPolicyLabel({ mode: 'auto_after', timeout_seconds: 30 })).toBe('Auto-pick after 30 s')
  expect(questionPolicyLabel({ mode: 'auto' })).toBe('Auto-pick now')
  expect(questionPolicyLabel(null)).toBe('Auto-pick after 2 min')
  expect(describeDefaultPolicy({ mode: 'auto_after', timeout_seconds: 120 })).toBe('Default: auto after 2 min')
  expect(describeDefaultPolicy({ mode: 'wait' })).toBe('Default: wait for me')
  expect(formatWait(1800)).toBe('30 min')
  expect(formatWait(90)).toBe('90 s')
  expect(formatWait(5400)).toBe('1 h 30 min')
})

it('formats a countdown as m:ss, rounding up and never below zero', () => {
  expect(formatCountdown(102_000)).toBe('1:42')
  expect(formatCountdown(4_100)).toBe('0:05')
  expect(formatCountdown(-3000)).toBe('0:00')
  expect(formatCountdown(3_900_000)).toBe('1:05:00')
  expect(msUntil('2026-01-01T00:01:00Z', Date.parse('2026-01-01T00:00:00Z'))).toBe(60_000)
  expect(msUntil(null)).toBeNull()
  expect(msUntil('not a date')).toBeNull()
})

it('round-trips dropdown keys and keeps a custom wait in the choices', () => {
  for (const p of [null, { mode: 'wait' }, { mode: 'auto' }, { mode: 'auto_after', timeout_seconds: 600 }] as const) {
    expect(policyFromKey(policyKey(p as any))).toEqual(p)
  }
  const choices = questionPolicyChoices({ mode: 'wait' }, { mode: 'auto_after', timeout_seconds: 45 })
  expect(choices[0]).toMatchObject({ value: 'default', label: 'Default (wait for me)' })
  expect(choices.map(c => c.value)).toContain('auto_after:45')
  expect(choices.map(c => c.label)).toEqual(expect.arrayContaining(['Wait for me', 'Auto-pick after 2 min', 'Auto-pick now']))
  expect(samePolicy({ mode: 'auto_after', timeout_seconds: 60 }, { mode: 'auto_after', timeout_seconds: 60 })).toBe(true)
  expect(samePolicy({ mode: 'auto_after', timeout_seconds: 60 }, { mode: 'auto_after', timeout_seconds: 120 })).toBe(false)
  expect(samePolicy(null, null)).toBe(true)
})

describe('auto-pick', () => {
  const options = [{ label: 'A' }, { label: 'B (Recommended) ' }, { label: 'C (recommended)' }]

  it('uses the server indexes when present', () => {
    expect(autoPickLabels({ options, recommended: [2] })).toEqual(['C (recommended)'])
    expect(parseRecommended([1, 1, 'x', -1, 9], 3)).toEqual([1])
  })

  it('falls back to the server rule', () => {
    expect(autoPickLabels({ options })).toEqual(['B (Recommended) '])
    expect(autoPickLabels({ options, multiSelect: true })).toEqual(['B (Recommended) ', 'C (recommended)'])
    expect(autoPickLabels({ options: [{ label: 'x' }, { label: 'y' }] })).toEqual(['x'])
    expect(autoPickLabels({ options, requires_user: true })).toEqual([])
  })
})

it('knows which runtimes ask questions', () => {
  expect(questionPolicyApplies('claude', 'claude-opus-5-5')).toBe(true)
  expect(questionPolicyApplies('deepseek', 'deepseek-chat')).toBe(true)
  expect(questionPolicyApplies(undefined, undefined)).toBe(true)
  expect(questionPolicyApplies('codex', 'gpt-6-astra')).toBe(false)
  expect(questionPolicyApplies('claude', 'gpt-5-codex')).toBe(false)
  expect(questionPolicyApplies('ollama', 'llama')).toBe(false)
  expect(questionPolicyApplies('custom', 'whatever')).toBe(true)
})
