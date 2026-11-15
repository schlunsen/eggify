import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import {
  zenResponseLayout, zenSidePanelTop, zenSidePanelWidth,
  ZEN_SIDE_PANEL_MIN_WIDTH, ZEN_SIDE_PANEL_TOP, ZEN_SIDE_PANEL_RIGHT,
} from '../../utils/zenLayout'
import { HUD_SELECTORS } from '../../composables/agents/zen/circusLanes'

const zenMode = readFileSync(resolve(__dirname, '../../components/agents/ZenMode.vue'), 'utf8')
const chatArea = readFileSync(resolve(__dirname, '../../components/agents/ChatArea.vue'), 'utf8')

describe('Zen latest-response layout', () => {
  it('is a side panel on wide stages and the centred card on narrow ones', () => {
    expect(ZEN_SIDE_PANEL_MIN_WIDTH).toBe(1000)
    expect(zenResponseLayout(1440)).toBe('panel')
    expect(zenResponseLayout(1000)).toBe('panel')
    expect(zenResponseLayout(999)).toBe('card')
    expect(zenResponseLayout(700)).toBe('card')
    expect(zenResponseLayout(0)).toBe('card')
  })

  it('panel width is 30 % between 300 and 460 px (matches the CSS clamp)', () => {
    expect(zenSidePanelWidth(1000)).toBe(300)
    expect(zenSidePanelWidth(1200)).toBe(360)
    expect(zenSidePanelWidth(2400)).toBe(460)
    expect(zenMode).toMatch(/--zen-side-w:\s*clamp\(300px, 30%, 460px\)/)
    expect(zenMode).toMatch(new RegExp(`right:\\s*${ZEN_SIDE_PANEL_RIGHT}px`))
  })

  it('starts below the top bar, or below the name plate when the plate reaches the panel', () => {
    // No plate (other scenes): right under the avatar card
    expect(zenSidePanelTop(1400, 58, 0)).toBe(ZEN_SIDE_PANEL_TOP)
    // 1100 px: the plate (290..810) runs into the panel (left edge 714): go below it
    expect(zenSidePanelTop(1100, 58, 60)).toBe(58 + 60 + 14)
    // 2200 px: the plate (840..1360) ends well before the panel (1684): full height
    expect(zenSidePanelTop(2200, 58, 90)).toBe(ZEN_SIDE_PANEL_TOP)
  })

  it('ZenMode switches the card into the panel and moves the centre column left of it', () => {
    expect(zenMode).toMatch(/'zen-side-layout': responseLayout === 'panel'/)
    expect(zenMode).toMatch(/:class="\[responseLayout === 'panel' \? 'zen-response-panel' : 'zen-last-message-clickable'/)
    expect(zenMode).toMatch(/<ZenResponseBody :text="lastAssistantMessage" :layout="responseLayout"/)
    expect(zenMode).toMatch(/\.zen-mode\.zen-side-layout \.zen-overlay \{\s*padding-right: calc\(var\(--zen-side-w\) \+ \d+px\);/)
    // Full height: from --zen-side-top to above the composer, which ChatArea measures
    const panel = zenMode.match(/\.zen-last-message\.zen-response-panel \{([^}]*)\}/)
    expect(panel).not.toBeNull()
    expect(panel![1]).toMatch(/top: var\(--zen-side-top/)
    expect(panel![1]).toMatch(/bottom: max\(calc\(var\(--zen-composer-h/)
    expect(chatArea).toMatch(/'--zen-composer-h': `\$\{composerHeight\}px`/)
    // Copy and read-aloud stay in the panel's header
    expect(zenMode).toMatch(/data-testid="zen-resp-panel-copy"/)
    // "Your message" and the status chips stay in the centre column
    const overlay = zenMode.slice(zenMode.indexOf('<div class="zen-overlay">'))
    expect(overlay.indexOf('zen-status-badge')).toBeGreaterThan(-1)
    expect(overlay.indexOf('zen-user-message')).toBeGreaterThan(-1)
  })

  it('stage props treat the panel as HUD', () => {
    expect(HUD_SELECTORS).toContain('.zen-response-panel')
    expect(HUD_SELECTORS).toContain('.zen-last-message')
  })

  it('a streaming reply does not re-mount the card for every chunk', () => {
    expect(zenMode).toMatch(/:key="'asst-' \+ latestResponseKey"/)
    expect(zenMode).not.toMatch(/:key="'asst-' \+ lastAssistantMessage"/)
  })
})
