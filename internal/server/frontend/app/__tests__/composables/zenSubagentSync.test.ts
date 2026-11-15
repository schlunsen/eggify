import { defineComponent, h, ref, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { createCircusScene } from '../../composables/agents/zen/sceneCircus'
import { useZenGpuScenes } from '../../composables/agents/zen/useZenGpuScenes'

const walkers = (el: HTMLElement) => el.querySelectorAll('.cc-walker').length

beforeEach(() => {
  ;(globalThis as any).ResizeObserver ??= class { observe() {} disconnect() {} unobserve() {} }
})

describe('circus scene subagent catch-up', () => {
  it('a scene created after the subagents exist shows the running ones', () => {
    const el = document.createElement('div')
    const scene = createCircusScene()
    scene.mount(el)
    scene.agentSync(['a', 'b'])
    expect(walkers(el)).toBe(2)
    scene.unmount()
  })

  it('is idempotent: events after a sync do not duplicate walkers', () => {
    const el = document.createElement('div')
    const scene = createCircusScene()
    scene.mount(el)
    scene.agentSync(['a', 'b'])
    scene.agentSpawn('a')
    scene.agentSync(['a', 'b'])
    expect(walkers(el)).toBe(2)
    scene.unmount()
  })

  it('keeps at most the walker cap and drops ones that stopped running', () => {
    const el = document.createElement('div')
    const scene = createCircusScene()
    scene.mount(el)
    scene.agentSync(['1', '2', '3', '4', '5', '6', '7', '8', '9'])
    expect(walkers(el)).toBeLessThanOrEqual(6)
    scene.agentSync([])
    expect(walkers(el)).toBe(0)
    scene.unmount()
  })
})

describe('switching session inside zen', () => {
  it('clears the previous session walkers and shows the new session ones', async () => {
    const container = ref<HTMLElement | null>(null)
    const running = ref<string[]>(['old1'])
    let api!: ReturnType<typeof useZenGpuScenes>
    const Comp = defineComponent({
      setup() {
        api = useZenGpuScenes({
          container, activeViz: ref('circus') as any, sessionStatus: ref('idle'), projectColor: ref(''),
          showWaves: () => {}, hideWaves: () => {}, runningAgentIds: () => running.value, onUnavailable: () => {},
        })
        return () => h('div', { ref: (e: any) => { container.value = e } })
      },
    })
    const w = mount(Comp, { attachTo: document.body })
    await nextTick()
    await api.start()
    const el = container.value!
    expect(walkers(el)).toBe(1)

    // Switch to a session whose subagents are already in the store
    api.resetScenes(['new1', 'new2'])
    expect(walkers(el)).toBe(2)
    // A later event for one of them does not add another
    api.agentSpawn('new1')
    expect(walkers(el)).toBe(2)
    // Switch to a session with none
    api.resetScenes([])
    expect(walkers(el)).toBe(0)
    w.unmount()
  })
})
