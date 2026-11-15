import { mount } from '@vue/test-utils'
import SubagentsZen from '../../components/agents/SubagentsZen.vue'

const agents = [
  { id: 'lead', name: 'Fix the tests', status: 'running', lines: ['TOOL:Bash: go test ./...'] },
  { id: 'helper', name: 'Review the fix', status: 'running', lines: [], parentId: 'lead' },
  { id: 'done', name: 'Map the API', status: 'completed', lines: ['Found it'] },
  { id: 'broke', name: 'Deploy', status: 'failed', lines: [], error: 'no credentials' }
]

it('puts every subagent in the scene, saying what it is doing', () => {
  const wrapper = mount(SubagentsZen, { props: { agents, stage: 'synthwave' } })
  const critters = wrapper.findAll('.critter')
  expect(critters).toHaveLength(4)
  expect(critters[0].get('.bubble').text()).toBe('>_Bash go test ./...')
  expect(critters[0].get('.name').text()).toBe('Fix the tests')
  expect(critters[1].classes()).toContain('nested')
  expect(critters[2].classes()).toContain('is-completed')
  expect(critters[2].find('.zzz').exists()).toBe(true)
  expect(critters[3].get('.bubble').text()).toContain('Ouch: no credentials')
  expect(critters[0].attributes('aria-label')).toBe('Fix the tests, working: Bash go test ./.... Show its activity.')
  // Positions are written straight to each character's slot.
  const slot = wrapper.findAll('.critter-slot')[2].element as HTMLElement
  expect(slot.style.top).toMatch(/%$/)
  wrapper.unmount()
})

it('updates the bubble live and opens the subagent when clicked', async () => {
  const wrapper = mount(SubagentsZen, { props: { agents, stage: 'synthwave' } })
  await wrapper.setProps({ agents: [{ ...agents[0], lines: [...agents[0].lines, 'TOOL:Edit: /app/a.ts'] }, ...agents.slice(1)] })
  const lead = wrapper.findAll('.critter')[0]
  expect(lead.get('.bubble').text()).toBe('✎Edit /app/a.ts')
  expect(lead.classes()).toContain('talking')
  await lead.trigger('click')
  expect(wrapper.emitted('select')).toEqual([['lead']])
  wrapper.unmount()
})

it('puts the main agent on stage, tethered to its subagents, and takes you back to it', async () => {
  const lead = { name: 'Ricky', status: 'processing', lines: ['TOOL:Agent: Fix the tests'], avatarImage: null, color: '#EC609A' }
  const wrapper = mount(SubagentsZen, { props: { agents, lead, stage: 'synthwave' } })
  const main = wrapper.get('.lead')
  expect(main.get('.role').text()).toBe('Main agent')
  expect(main.get('.lead-name').text()).toBe('Ricky')
  expect(main.get('.initial').text()).toBe('R')
  expect(main.classes()).toContain('is-working')
  expect(main.get('.lead-bubble').text()).toBe('✦Agent Fix the tests')
  // One line per running subagent: lead → top-level, subagent → nested.
  const tethers = wrapper.findAll('.tether')
  expect(tethers).toHaveLength(2)
  expect(tethers[1].classes()).toContain('nested')
  await wrapper.setProps({ lead: { ...lead, status: 'idle' } })
  expect(wrapper.get('.lead-bubble').text()).toBe('◔Waiting on 2 subagents…')
  await wrapper.get('.lead').trigger('click')
  expect(wrapper.emitted('lead')).toHaveLength(1)
  wrapper.unmount()
})

it('shows the main agent alone when there are no subagents yet', () => {
  const wrapper = mount(SubagentsZen, { props: { agents: [], lead: { name: 'Ricky', status: 'idle', lines: [], avatarImage: '/a.png' }, stage: 'synthwave' } })
  expect(wrapper.get('.lead img').attributes('src')).toBe('/a.png')
  expect(wrapper.get('.lead-bubble').text()).toBe('☾All quiet. Waiting for you.')
  expect(wrapper.get('.zen-hint').text()).toContain('step out from Ricky')
  wrapper.unmount()
})


describe('Flying Circus stage (the default)', () => {
  it('draws cut-out characters on a paper collage and speaks in character', () => {
    const wrapper = mount(SubagentsZen, { props: { agents } })
    expect(wrapper.get('.zen-stage').classes()).toContain('stage-circus')
    // The scenery is an engraved Circus backdrop, the floor the stage boards
    expect(wrapper.find('.circus-scenery .cc-backdrop').exists()).toBe(true)
    expect(wrapper.find('.circus-floor').exists()).toBe(true)
    expect(wrapper.find('.zen-city').exists()).toBe(false)
    const critters = wrapper.findAll('.critter')
    expect(critters).toHaveLength(4)
    expect(critters.every(c => c.find('.circus-figure').exists())).toBe(true)
    // Every subagent is an engraved egg in costume, like the main egg gentleman
    for (const c of critters) expect(c.get('.circus-figure img').attributes('src')).toMatch(/^\/zen\/circus\/eggs\/egg-(gent|knight|hanky|parrot|egg|lady)\.webp$/)
    // A failed one is cracked
    expect(critters[3].find('.crack').exists()).toBe(true)
    expect(critters[2].find('.crack').exists()).toBe(false)
    expect(critters[0].get('.bubble').text()).toBe('>_Bash go test ./...')
    expect(critters[2].get('.bubble').text()).toContain('Splendid!')
    expect(critters[3].get('.bubble').text()).toContain("'Tis but a scratch: no credentials")
    expect(wrapper.get('.circus-bench').text()).toBe('Resting, not dead')
    wrapper.unmount()
  })

  it('puts the main agent on a soapbox', () => {
    const wrapper = mount(SubagentsZen, { props: { agents: [], lead: { name: 'Ricky', status: 'idle', lines: [], avatarImage: '/a.png' } } })
    expect(wrapper.find('.circus-lead').exists()).toBe(true)
    expect(wrapper.get('.cameo').attributes('src')).toBe('/a.png')
    expect(wrapper.get('.lead-bubble').text()).toContain('Your move, I believe.')
    expect(wrapper.find('.crown').exists()).toBe(false)
    wrapper.unmount()
  })

  it('brings the foot down on a subagent that fails', async () => {
    const running = [{ id: 'x', name: 'Deploy', status: 'running', lines: [] }]
    const wrapper = mount(SubagentsZen, { props: { agents: running } })
    expect(wrapper.get('.circus-foot').classes()).not.toContain('stomping')
    await wrapper.setProps({ agents: [{ ...running[0], status: 'failed', error: 'boom' }] })
    expect(wrapper.get('.circus-foot').classes()).toContain('stomping')
    wrapper.unmount()
  })
})

describe('Circus subagent eggs', () => {
  it('the session stage uses the backdrop its Zen shows', async () => {
    const { resolveCircusBackdrop } = await import('../../composables/agents/zen/useCircusBackdrop')
    const wrapper = mount(SubagentsZen, { props: { agents: [], lead: { name: 'Ricky', status: 'idle', lines: [] }, sessionId: 'sess-42' } })
    expect(wrapper.get('.circus-scenery').classes()).toContain(`cc-bd-${resolveCircusBackdrop('sess-42')}`)
    wrapper.unmount()
  })

  it('every kind has its egg file, with the recorded size', async () => {
    const { readFileSync } = await import('node:fs')
    const { resolve } = await import('node:path')
    const { CIRCUS_KINDS, EGG_ASPECT, circusEggArt } = await import('../../utils/agents/circusCharacters')
    for (const kind of CIRCUS_KINDS) {
      const buf = readFileSync(resolve(__dirname, '../../../public', circusEggArt(kind).slice(1)))
      // WebP VP8X header: canvas width/height minus one, 24-bit little endian at 24 / 27
      expect(buf.toString('ascii', 0, 4)).toBe('RIFF')
      const w = buf.readUIntLE(24, 3) + 1
      const h = buf.readUIntLE(27, 3) + 1
      expect(h).toBe(600)
      expect(EGG_ASPECT[kind]).toBeCloseTo(w / h, 3)
    }
  })
})

it.each(['synthwave', 'circus'])('draws markdown replies as plain words in the bubbles (%s stage)', (stage) => {
  const said = 'Three subagents are working: the **schema**, the **store** and `api`'
  const lead = { name: 'Ricky', status: 'processing', lines: [said], avatarImage: null, color: '#EC609A' }
  const wrapper = mount(SubagentsZen, { props: { agents: [{ id: 'a', name: 'A', status: 'running', lines: [said] }], lead, stage } })
  for (const bubble of wrapper.findAll('.bubble')) expect(bubble.text()).not.toMatch(/[*`]/)
  expect(wrapper.get('.lead-bubble').text()).toContain('the schema, the store and api')
  wrapper.unmount()
})
