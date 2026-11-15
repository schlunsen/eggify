import { mount } from '@vue/test-utils'
import WorkModeChoice from '../../components/agents/WorkModeChoice.vue'
import {
  WORK_MODE_STORAGE_KEY, initialWorkMode, readSavedWorkMode, workModeFields, workModeOfForm, workModeOfSession, workModeSummary,
} from '../../utils/agents/workMode'

const slots = {
  autopilot: '<div data-testid="slot-autopilot" />',
  ask: '<div data-testid="slot-ask" />',
  classic: '<div data-testid="slot-classic" />',
}

const mountChoice = (props: Record<string, unknown>) => mount(WorkModeChoice, { props, slots })

beforeEach(() => localStorage.clear())

describe('the three options and the fields they set', () => {
  it('Autopilot is prompts off inside the sandbox', () => {
    expect(workModeFields('autopilot')).toEqual({ permissionMode: 'yolo', sandboxed: true })
  })
  it('Ask first has prompts on and leaves the sandbox to its own switch', () => {
    expect(workModeFields('ask')).toEqual({ permissionMode: 'default', sandboxed: null })
  })
  it('Full access is prompts off without a sandbox', () => {
    expect(workModeFields('full')).toEqual({ permissionMode: 'yolo', sandboxed: false })
  })
  it('reads the option back from the two fields', () => {
    expect(workModeOfForm('yolo', true)).toBe('autopilot')
    expect(workModeOfForm('yolo', false)).toBe('full')
    expect(workModeOfForm('default', null)).toBe('ask')
    expect(workModeOfForm('default', true)).toBe('ask')
    expect(workModeOfForm('allow-all', null)).toBeNull()
    expect(workModeOfForm('read-only', null)).toBeNull()
    expect(workModeOfForm('yolo', null)).toBeNull()
  })
})

describe('default selection', () => {
  it('is the last choice, else Autopilot', () => {
    expect(initialWorkMode('claude', null)).toBe('autopilot')
    expect(initialWorkMode('claude', 'ask')).toBe('ask')
    expect(initialWorkMode('deepseek', 'full')).toBe('full')
  })
  it('is nothing where Autopilot cannot hold', () => {
    for (const provider of ['codex', 'openai', 'ollama', 'openrouter', 'my-custom-provider']) {
      expect(initialWorkMode(provider, null)).toBeNull()
      expect(initialWorkMode(provider, 'autopilot')).toBeNull()
    }
  })
  it('ignores an unreadable remembered value', () => {
    localStorage.setItem(WORK_MODE_STORAGE_KEY, 'bogus')
    expect(readSavedWorkMode()).toBeNull()
  })
})

describe('WorkModeChoice', () => {
  it('starts a new form on Autopilot and shows its grants', () => {
    const wrapper = mountChoice({ permissionMode: 'default', sandboxed: null, provider: 'claude', applyDefault: true })
    expect(wrapper.emitted('update:permissionMode')?.at(-1)).toEqual(['yolo'])
    expect(wrapper.emitted('update:sandboxed')?.at(-1)).toEqual([true])
  })
  it('starts on the remembered choice', () => {
    localStorage.setItem(WORK_MODE_STORAGE_KEY, 'full')
    const wrapper = mountChoice({ permissionMode: 'default', sandboxed: null, provider: 'claude', applyDefault: true })
    expect(wrapper.emitted('update:permissionMode')?.at(-1)).toEqual(['yolo'])
    expect(wrapper.emitted('update:sandboxed')?.at(-1)).toEqual([false])
  })
  it('leaves a prefilled form alone', () => {
    const wrapper = mountChoice({ permissionMode: 'yolo', sandboxed: false, provider: 'claude', applyDefault: false })
    expect(wrapper.emitted('update:permissionMode')).toBeUndefined()
    expect(wrapper.emitted('update:sandboxed')).toBeUndefined()
  })
  it('sends each option\'s fields and remembers the choice', async () => {
    const wrapper = mountChoice({ permissionMode: 'yolo', sandboxed: true, provider: 'claude', applyDefault: false })
    for (const [id, pm, sb] of [['ask', 'default', null], ['full', 'yolo', false], ['autopilot', 'yolo', true]] as const) {
      await wrapper.get(`[data-testid="work-mode-${id}"] input`).trigger('change')
      expect(wrapper.emitted('update:permissionMode')?.at(-1)).toEqual([pm])
      expect(wrapper.emitted('update:sandboxed')?.at(-1)).toEqual([sb])
      expect(localStorage.getItem(WORK_MODE_STORAGE_KEY)).toBe(id)
    }
  })
  it('shows the grants only under Autopilot and the sandbox switch under Ask first', async () => {
    const autopilot = mountChoice({ permissionMode: 'yolo', sandboxed: true, provider: 'claude' })
    expect(autopilot.find('[data-testid="slot-autopilot"]').exists()).toBe(true)
    expect(autopilot.find('[data-testid="slot-ask"]').exists()).toBe(false)
    const ask = mountChoice({ permissionMode: 'default', sandboxed: null, provider: 'claude' })
    expect(ask.find('[data-testid="slot-ask"]').exists()).toBe(true)
    const full = mountChoice({ permissionMode: 'yolo', sandboxed: false, provider: 'claude' })
    expect(full.find('[data-testid="slot-autopilot"]').exists()).toBe(false)
    expect(full.find('[data-testid="slot-ask"]').exists()).toBe(false)
    expect(full.get('[data-testid="work-mode-full"]').classes()).toContain('risky')
  })
  it('marks a custom permission mode instead of picking an option', () => {
    const wrapper = mountChoice({ permissionMode: 'allow-all', sandboxed: null, provider: 'claude' })
    expect(wrapper.find('[data-testid="work-mode-custom"]').exists()).toBe(true)
    expect(wrapper.findAll('input[type="radio"]').some(r => (r.element as HTMLInputElement).checked)).toBe(false)
  })
  it('does not offer Autopilot for Codex or a direct provider, and says why', () => {
    for (const provider of ['codex', 'ollama']) {
      const wrapper = mountChoice({ permissionMode: 'default', sandboxed: null, provider, applyDefault: true })
      expect(wrapper.find('[data-testid="work-mode-autopilot"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="slot-classic"]').exists()).toBe(true)
      expect(wrapper.get('[data-testid="work-mode-unavailable"]').text()).not.toBe('')
      expect(wrapper.emitted('update:permissionMode')).toBeUndefined()
    }
  })
})

describe('a running session', () => {
  it('is named by its prompts and its sandbox', () => {
    expect(workModeOfSession({ options: { dangerously_skip_permissions: true, sandboxed: true } })).toBe('autopilot')
    expect(workModeOfSession({ options: { dangerously_skip_permissions: true, sandboxed: false } })).toBe('full')
    expect(workModeOfSession({ options: { sandboxed: true } })).toBe('ask')
    expect(workModeSummary({ options: { dangerously_skip_permissions: true, sandboxed: true } })).toBe('Autopilot · sandboxed')
    expect(workModeSummary({ options: { dangerously_skip_permissions: true } })).toBe('Full access · not sandboxed')
    expect(workModeSummary({ options: {} })).toBe('Ask first')
  })
})
