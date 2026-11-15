import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import WorkingEdge from '../../components/ui/WorkingEdge.vue'
import ChatInput from '../../components/agents/ChatInput.vue'
import { canUseShaderEdge, edgePalette, readEdgeColors, toHex } from '../../utils/workingEdge'

vi.mock('shaders/vue', () => ({
  Shader: { name: 'Shader', template: '<div class="shader-stub"><slot /></div>' },
  FlowingGradient: { name: 'FlowingGradient', props: ['colorA', 'colorB', 'colorC', 'colorD', 'speed', 'distortion'], template: '<i class="flowing-stub" />' },
}))

const motion = (reduce: boolean) => vi.fn((q: string) => ({ matches: reduce && q.includes('reduce') }))

afterEach(() => {
  vi.unstubAllGlobals()
  document.documentElement.removeAttribute('style')
})

describe('canUseShaderEdge', () => {
  it('needs WebGPU and allowed motion', () => {
    expect(canUseShaderEdge({ navigator: {} as Navigator, matchMedia: motion(false) as any })).toBe(false)
    expect(canUseShaderEdge({ navigator: { gpu: {} } as any, matchMedia: motion(false) as any })).toBe(true)
    expect(canUseShaderEdge({ navigator: { gpu: {} } as any, matchMedia: motion(true) as any })).toBe(false)
    expect(canUseShaderEdge(undefined)).toBe(false)
  })
})

describe('edge colours', () => {
  it('keeps hex colours and falls back per variable when unreadable', () => {
    expect(toHex('#ABCDEF')).toBe('#abcdef')
    expect(toHex('  ')).toBeNull()
    document.documentElement.style.setProperty('--accent-purple', '#112233')
    const colors = readEdgeColors(document.documentElement)
    expect(colors[0]).toBe('#112233')
    expect(colors).toHaveLength(4)
    colors.forEach(c => expect(c).toMatch(/^#[0-9a-f]{6}$/))
  })
})

describe('edgePalette', () => {
  it('makes shades of the accent: itself, lighter, tinted, deeper', () => {
    expect(edgePalette('#804020', '#00ffff')).toEqual(['#804020', '#b99684', '#4d8c79', '#4d2613'])
  })
})

describe('WorkingEdge', () => {
  it('renders nothing while idle and the CSS edge without WebGPU', async () => {
    const wrapper = mount(WorkingEdge, { props: { active: false }, global: { stubs: { transition: false } } })
    expect(wrapper.find('[data-testid="working-edge"]').exists()).toBe(false)
    await wrapper.setProps({ active: true })
    const edge = wrapper.get('[data-testid="working-edge"]')
    expect(edge.classes()).not.toContain('gpu')
    expect(wrapper.find('.shader-stub').exists()).toBe(false)
  })

  it('draws the shader edge with WebGPU and drops to CSS when the GPU is unavailable', async () => {
    vi.stubGlobal('navigator', { ...navigator, gpu: {} })
    vi.stubGlobal('matchMedia', motion(false))
    const wrapper = mount(WorkingEdge, { props: { active: true }, global: { stubs: { transition: false } } })
    await vi.dynamicImportSettled()
    await flushPromises()
    expect(wrapper.get('[data-testid="working-edge"]').classes()).toContain('gpu')
    const gradient = wrapper.getComponent({ name: 'FlowingGradient' })
    // Theme colours are read in readEdgeColors (tested above); happy-dom doesn't inherit custom properties.
    expect(gradient.props('colorB')).toMatch(/^#[0-9a-f]{6}$/)

    wrapper.getComponent({ name: 'Shader' }).vm.$emit('unavailable')
    await nextTick()
    expect(wrapper.get('[data-testid="working-edge"]').classes()).not.toContain('gpu')
    expect(wrapper.find('.shader-stub').exists()).toBe(false)
  })
})

describe('composer', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: vi.fn(async () => ({ ok: false, json: async () => ({}) })) }))
  })

  const mountInput = (props: Record<string, unknown>) => mount(ChatInput, {
    props: { inputMessage: '', connected: true, hasActiveSession: true, ...props },
    global: { stubs: { MessageHistoryDropdown: true, transition: false } },
  })

  it('shows the edge around the message box only while the agent works', () => {
    expect(mountInput({ busy: true }).find('.message-input-wrap [data-testid="working-edge"]').exists()).toBe(true)
    expect(mountInput({ busy: false }).find('[data-testid="working-edge"]').exists()).toBe(false)
  })

  it('leaves the edge out of the Zen composer', () => {
    expect(mountInput({ busy: true, variant: 'zen' }).find('[data-testid="working-edge"]').exists()).toBe(false)
  })
})

describe('edge pulse', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('brightens on activity, then not again until the gap has passed', async () => {
    vi.spyOn(Math, 'random').mockReturnValue(0)
    const wrapper = mount(WorkingEdge, { props: { active: true, activity: 'a' }, global: { stubs: { transition: false } } })
    const edge = () => wrapper.get('[data-testid="working-edge"]')
    expect(edge().classes()).not.toContain('pulse')
    await wrapper.setProps({ activity: 'b' })
    vi.advanceTimersByTime(0)
    await nextTick()
    expect(edge().classes()).toContain('pulse')
    vi.advanceTimersByTime(2_200)
    await nextTick()
    expect(edge().classes()).not.toContain('pulse')
    await wrapper.setProps({ activity: 'c' })
    vi.advanceTimersByTime(100)
    await nextTick()
    expect(edge().classes()).not.toContain('pulse')
    vi.restoreAllMocks()
  })
})
