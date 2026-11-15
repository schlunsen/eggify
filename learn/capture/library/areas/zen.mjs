// Episode 9: Zen and themes.
import { libShot, press } from '../lib.mjs'
import { openWorld, openSession, S, go, sleep } from '../world.mjs'

async function setZen(app, id, mode) {
  // what the Zen / Live switch does: PATCH the session's view mode
  await app.page.evaluate(async ([sid, m]) => {
    await fetch(`/api/agent/sessions/${sid}/view-mode`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ view_mode: m }) })
  }, [id, mode])
}
const setStorage = (page, key, value) => page.evaluate(([k, v]) => localStorage.setItem(k, v), [key, value])

export default async function (server) {
  const app = await openWorld(server)
  const { browser, page } = app
  const zenOpts = { settleOpts: { timeout: 2500 } }
  try {
    // --- into Zen from the chat header
    await openSession(app, S.steer)
    await page.locator('.view-mode-toggle button', { hasText: 'Zen' }).click()
    await page.waitForSelector('.zen-mode.zen-viz-circus', { timeout: 15000 })
    await page.waitForSelector('[data-testid=zen-latest-response]', { timeout: 15000 })
    await sleep(3500)
    await libShot(page, 'zen', 'zen-circus', {
      ...zenOpts,
      shows: 'A session in Zen on the Circus stage: the engraved paper backdrop with the egg gentleman as the agent, the name plate (session name and summary), the latest reply as a paper panel on the right, the composer below',
      how: 'seeded session and digest; sound is off and motion reduced for the still (a clip would show the stage moving to the music)',
      boxes: {
        stage: '.zen-mode', plate: '[data-testid=zen-plate]', 'plateName?': '[data-testid=zen-plate-name]', 'plateSummary?': '[data-testid=zen-plate-summary]', reply: '[data-testid=zen-latest-response]',
        'vizPicker?': '.zen-viz-picker', 'sound?': '[data-testid=zen-sound-toggle]', 'avatarCard?': '.zen-avatar-card', 'composer?': '.chat-input-wrapper', 'liveSwitch?': '.view-mode-toggle',
      },
    })
    await page.locator('.zen-viz-active-btn').click()
    await page.waitForSelector('.zen-viz-option')
    await sleep(500)
    await libShot(page, 'zen', 'zen-viz-picker', {
      ...zenOpts,
      shows: 'The Zen scene picker opened: Circus, Waves, Galaxy, Ink, and the Circus backdrop button',
      how: 'seeded',
      boxes: { picker: '.zen-viz-picker', options: page.locator('.zen-viz-option').first().locator('xpath=..'), 'backdrop?': '[data-testid=zen-backdrop-cycle]' },
    })
    await page.keyboard.press('Escape')

    // --- Zen full screen: the stage is wide enough for the reply as a panel on the right
    const fullscreen = async on => {
      const is = await page.locator('.zen-mode.zen-fullscreen').count()
      if (!!is !== on) { await page.locator('.zen-fullscreen-btn:not(.zen-fly-btn)').click(); await sleep(1500) }
    }
    // The stage lays its figures out for the width it opens at: open it wide (both sidebars folded) first.
    await press(page, 'Meta+KeyB', 600)
    await press(page, 'Meta+Alt+KeyB', 900)
    await openSession(app, S.steer)
    await page.waitForSelector('.zen-mode.zen-viz-circus', { timeout: 15000 })
    await sleep(1500)
    await fullscreen(true)
    await sleep(3000)
    await libShot(page, 'zen', 'zen-fullscreen', {
      ...zenOpts,
      shows: 'Zen in full screen: the whole window is the Circus stage, with the name plate on top, the latest reply printed on paper as a panel on the right, "Your message" and the composer in the middle',
      how: 'seeded session and digest; sound off, motion reduced for the still',
      boxes: { stage: '.zen-mode', plate: '[data-testid=zen-plate]', reply: '[data-testid=zen-latest-response]', 'vizPicker?': '.zen-viz-picker', 'sound?': '[data-testid=zen-sound-toggle]', 'avatarCard?': '.zen-avatar-card', 'composer?': '.chat-input-wrapper' },
    })

    await page.locator('[data-testid=zen-latest-response] .zen-card-close').click()
    await sleep(1200)
    await libShot(page, 'zen', 'zen-fullscreen-stage', {
      ...zenOpts,
      shows: 'The same full-screen stage with the reply card closed (×): the egg gentleman, the backdrop, the name plate and "Your message"; "Show reply" brings the card back',
      how: 'seeded; the reply was closed with its own × button',
      boxes: { stage: '.zen-mode', plate: '[data-testid=zen-plate]', 'showReply?': page.getByText('Show reply').first(), 'vizPicker?': '.zen-viz-picker', 'sound?': '[data-testid=zen-sound-toggle]' },
    })

    // --- other backdrops of the same stage
    for (const [backdrop, words] of [['seaside', 'seaside'], ['gothic', 'gothic'], ['snow', 'snow']]) {
      await setStorage(page, 'eggify-zen-circus-backdrop', backdrop)
      await openSession(app, S.steer)
      await page.waitForSelector('.zen-mode.zen-viz-circus', { timeout: 15000 })
      await fullscreen(true)
      await page.locator('[data-testid=zen-latest-response] .zen-card-close').click().catch(() => {})
      await sleep(3000)
      await libShot(page, 'zen', `zen-backdrop-${backdrop}`, {
        ...zenOpts,
        shows: `The same Zen session in full screen with the "${words}" Circus backdrop (each session gets its own; the picker overrides it), reply card closed`,
        how: 'seeded; backdrop pinned through the picker\'s own setting',
        boxes: { stage: '.zen-mode', plate: '[data-testid=zen-plate]' },
      })
    }
    await setStorage(page, 'eggify-zen-circus-backdrop', 'meadow')

    // --- a working session with subagents on stage
    await openSession(app, S.postgres)
    await setZen(app, S.postgres, 'zen')
    await openSession(app, S.postgres)
    await page.waitForSelector('.zen-mode.zen-viz-circus', { timeout: 15000 })
    await fullscreen(true)
    await sleep(4000)
    await libShot(page, 'zen', 'zen-working', {
      ...zenOpts,
      shows: 'A working session in Zen, full screen: status chips, the agent\'s latest words, its subagents counted on the avatar card',
      how: 'seeded as working; subagents are a stubbed API answer',
      boxes: { stage: '.zen-mode', plate: '[data-testid=zen-plate]', 'reply?': '[data-testid=zen-latest-response]', 'avatarCard?': '.zen-avatar-card', 'agents?': '.zen-agents-summary' },
    })
    await setZen(app, S.postgres, 'live')
    await fullscreen(false)
    await press(page, 'Meta+KeyB', 600)          // sidebars back
    await press(page, 'Meta+Alt+KeyB', 900)

    // --- Zen in the dark theatre theme
    await setStorage(page, 'eggify-theme', 'theatre-dark')
    await openSession(app, S.steer)
    await page.waitForSelector('.zen-mode.zen-viz-circus', { timeout: 15000 })
    await sleep(3500)
    await libShot(page, 'zen', 'zen-circus-theatre-dark', {
      ...zenOpts,
      shows: 'The Zen Circus stage with the app in Puppet Theatre Dark', how: 'seeded',
      boxes: { stage: '.zen-mode', plate: '[data-testid=zen-plate]', reply: '[data-testid=zen-latest-response]' },
    })
    await setZen(app, S.steer, 'live')

    // --- the same chat in several themes
    for (const [theme, label] of [['theatre-light', 'Puppet Theatre Light (the default)'], ['theatre-dark', 'Puppet Theatre Dark'], ['flyingcircus-light', 'Flying Circus Light'], ['nord-dark', 'Nord Dark'], ['dracula-dark', 'Dracula Dark'], ['eggify-light', 'Eggify Light']]) {
      await setStorage(page, 'eggify-theme', theme)
      await openSession(app, S.steer)
      await page.waitForSelector('.edit-diff-message')
      await page.locator('.edit-diff-message').first().evaluate(el => el.scrollIntoView({ block: 'center' }))
      await sleep(700)
      await libShot(page, 'zen', `theme-${theme}`, {
        shows: `The same chat (sidebar, Edit diff, right sidebar) in the ${label} theme`, how: 'seeded',
        boxes: { sidebar: '.sessions-sidebar', chat: '.messages-container', rightSidebar: '.metrics-sidebar' },
      })
    }

    // --- theme picker in the header's settings menu
    await setStorage(page, 'eggify-theme', 'theatre-light')
    await openSession(app, S.steer)
    await page.locator('.settings-button').click()
    await page.waitForSelector('.settings-theme-row')
    await sleep(400)
    await libShot(page, 'zen', 'settings-menu', {
      shows: 'The header\'s settings menu: What\'s New, keyboard shortcuts, the theme row', how: 'seeded',
      boxes: { menu: page.locator('.settings-theme-row').locator('xpath=ancestor::div[contains(@class,"dropdown")][1]'), themeRow: '.settings-theme-row', button: '.settings-button' },
    })
    await page.locator('.settings-theme-row .theme-selector-button').click()
    await page.waitForSelector('.theme-option')
    await sleep(500)
    await libShot(page, 'zen', 'theme-picker', {
      shows: 'The theme picker opened from the settings menu: every theme with its light / dark badge, the current one ticked', how: 'seeded',
      boxes: { picker: '.theme-dropdown', current: '.theme-option.theme-option-active', 'firstOption?': page.locator('.theme-option').first() },
    })
    await page.keyboard.press('Escape')
    await go(page, server, '/themes')
    await sleep(1500)
    await libShot(page, 'zen', 'themes-page', {
      shows: 'The Themes page: previews of every theme', how: 'seeded',
      boxes: {},
    })
  } finally {
    await setZen(app, S.steer, 'live').catch(() => {})
    await browser.close()
  }
}
