import { mount } from '@vue/test-utils'
import { describe, it, expect } from 'vitest'
import ZenNamePlate from '../../components/agents/ZenNamePlate.vue'
import { PLATE_NAME_MAX, PLATE_SUMMARY_MAX } from '../../utils/zenPlate'

const plate = (props: Record<string, unknown>) => mount(ZenNamePlate, { props: { name: 'Randy Catnaps', ...props } })

describe('ZenNamePlate', () => {
  it('shows the name alone when there is no summary yet', () => {
    const w = plate({})
    expect(w.get('[data-testid="zen-plate-name"]').text()).toBe('Randy Catnaps')
    expect(w.find('[data-testid="zen-plate-summary"]').exists()).toBe(false)
  })

  it('shows name and summary, without tags or a backdrop name', () => {
    const w = plate({ summary: 'Fixing the login redirect loop', tags: ['auth', 'bug'], caption: 'Seaside pier' })
    expect(w.get('[data-testid="zen-plate-summary"]').text()).toBe('Fixing the login redirect loop')
    expect(w.find('[data-testid="zen-plate-tags"]').exists()).toBe(false)
    expect(w.find('[data-testid="zen-plate-caption"]').exists()).toBe(false)
    expect(w.text()).not.toContain('Seaside pier')
    expect(w.text()).not.toContain('auth')
  })

  it('truncates a very long name but keeps the full one in the tooltip', () => {
    const long = 'A'.repeat(120)
    const w = plate({ name: long })
    const shown = w.get('[data-testid="zen-plate-name"]')
    expect(shown.text().length).toBeLessThanOrEqual(PLATE_NAME_MAX)
    expect(shown.text().endsWith('…')).toBe(true)
    expect(shown.attributes('title')).toBe(long)
  })

  it('caps a long summary with an ellipsis', () => {
    const t = plate({ summary: 'word '.repeat(100) }).get('[data-testid="zen-plate-summary"]').text()
    expect(t.length).toBeLessThanOrEqual(PLATE_SUMMARY_MAX)
    expect(t.endsWith('…')).toBe(true)
  })

  it('ghosts while the user types', () => {
    expect(plate({ ghost: true }).classes()).toContain('zen-plate-ghost')
    expect(plate({ ghost: false }).classes()).not.toContain('zen-plate-ghost')
  })

  it('ignores an empty summary', () => {
    expect(plate({ summary: '   ' }).find('[data-testid="zen-plate-summary"]').exists()).toBe(false)
  })
})
