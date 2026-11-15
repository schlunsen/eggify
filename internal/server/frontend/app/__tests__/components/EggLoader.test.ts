import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EggLoader from '~/components/ui/EggLoader.vue'
import LoadingSpinner from '~/components/ui/LoadingSpinner.vue'
import { CIRCUS_KINDS } from '~/utils/agents/circusCharacters'

describe('EggLoader', () => {
  it('shows the chosen costume egg on a board', () => {
    const w = mount(EggLoader, { props: { kind: 'knight' } })
    expect(w.find('img').attributes('src')).toBe('/zen/circus/eggs/egg-knight.webp')
    expect(w.classes()).toContain('is-stage')
    expect(w.find('.board').exists()).toBe(true)
    expect(w.attributes('aria-hidden')).toBe('true')
  })

  it('picks one of the Circus costumes when none is given', () => {
    const src = mount(EggLoader).find('img').attributes('src') || ''
    expect(CIRCUS_KINDS.some(k => src.endsWith(`egg-${k}.webp`))).toBe(true)
  })

  it('compact variant hops in place without a board', () => {
    const w = mount(EggLoader, { props: { compact: true, kind: 'lady' } })
    expect(w.classes()).toContain('is-compact')
    expect(w.find('.board').exists()).toBe(false)
    expect(w.attributes('style')).toContain('--egg-h: 22px')
  })
})

describe('LoadingSpinner', () => {
  it('shows an egg and the message while loading', () => {
    const w = mount(LoadingSpinner, { props: { show: true, message: 'Polishing the monocle...' } })
    expect(w.find('[data-test="egg-loader"]').exists()).toBe(true)
    expect(w.text()).toContain('Polishing the monocle...')
  })

  it('renders nothing when hidden', () => {
    expect(mount(LoadingSpinner, { props: { show: false } }).find('[data-test="egg-loader"]').exists()).toBe(false)
  })
})
