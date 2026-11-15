import { CIRCUS_KINDS, circusCharacterFor, circusLeadSpeech, circusSpeech } from '../../utils/agents/circusCharacters'
import { DEFAULT_ZEN_STAGE, ZEN_STAGE_KEY, loadZenStage, saveZenStage } from '../../utils/agents/zenStage'

describe('circusCharacterFor', () => {
  it('gives the same character to the same subagent every time', () => {
    expect(circusCharacterFor('agent-42')).toEqual(circusCharacterFor('agent-42'))
  })

  it('uses every kind of character across a crowd, and parrots are blue', () => {
    const seen = new Set<string>()
    for (let i = 0; i < 400; i++) {
      const c = circusCharacterFor(`agent-${i}`)
      seen.add(c.kind)
      if (c.kind === 'parrot') expect(c.color).toBe('#3d7cc9')
      expect(c.accent.toLowerCase()).not.toBe(c.color.toLowerCase())
    }
    expect([...seen].sort()).toEqual([...CIRCUS_KINDS].sort())
  })
})

describe('circus speech', () => {
  it('rewrites finished and failed bubbles in character, and passes work through', () => {
    expect(circusSpeech({ glyph: '✓', text: 'Done!' }, 'completed')).toEqual({ glyph: '✓', text: 'Splendid!' })
    expect(circusSpeech({ glyph: '✕', text: 'Ouch' }, 'failed', 'no credentials').text).toBe("'Tis but a scratch: no credentials")
    expect(circusSpeech({ glyph: '>_', text: 'Bash ls' }, 'running')).toEqual({ glyph: '>_', text: 'Bash ls' })
  })

  it('has the main agent ponder, wait over tea, or hand over to you', () => {
    expect(circusLeadSpeech(true, 0).text).toBe('Pondering…')
    expect(circusLeadSpeech(false, 2).text).toBe('Awaiting 2 subagents. Tea, anyone?')
    expect(circusLeadSpeech(false, 0).text).toBe('Your move, I believe.')
  })
})

describe('zen stage preference', () => {
  beforeEach(() => localStorage.clear())

  it('defaults to the Flying Circus and remembers a choice', () => {
    expect(DEFAULT_ZEN_STAGE).toBe('circus')
    expect(loadZenStage()).toBe('circus')
    saveZenStage('synthwave')
    expect(loadZenStage()).toBe('synthwave')
    localStorage.setItem(ZEN_STAGE_KEY, 'nonsense')
    expect(loadZenStage()).toBe('circus')
  })
})
