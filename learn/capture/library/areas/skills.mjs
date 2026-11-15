// Episode 10: skills, connectors, providers.
import { libShot } from '../lib.mjs'
import { openWorld, openSession, S, go, sleep } from '../world.mjs'

export default async function (server) {
  const app = await openWorld(server)
  const { browser, page } = app
  try {
    await go(page, server, '/skills')
    await page.waitForSelector('.skill-card', { timeout: 15000 })
    await sleep(900)
    await libShot(page, 'skills', 'skills-page', {
      shows: 'Skills page: the review queue on top (skills found on disk wait for approval), then the approved skills',
      how: 'seeded (real SKILL.md folders in the demo home; four approved through the review API as the signed-in user)',
      boxes: { 'queue?': '.review-queue', 'discover?': '[data-test=discover-btn]', 'firstPending?': page.locator('[data-test=rq-item]').first() },
    })
    await page.locator('.skill-card').first().evaluate(el => el.scrollIntoView({ block: 'center' }))
    await sleep(500)
    await libShot(page, 'skills', 'skills-approved', {
      shows: 'The approved skills as cards: name, description, scope (personal), what each may use', how: 'seeded',
      boxes: { firstSkill: page.locator('.skill-card').first(), 'tabs?': page.locator('.filter-tab').first().locator('xpath=..'), 'releaseChecklist?': page.locator('.skill-card', { hasText: 'release-checklist' }).first() },
    })
    await page.locator('.skill-card', { hasText: 'tea-tasting-notes' }).first().click().catch(() => {})
    await sleep(800)
    await libShot(page, 'skills', 'skill-detail', {
      shows: 'A skill opened: its SKILL.md text and settings', how: 'seeded',
      boxes: {},
    })
    await page.keyboard.press('Escape')

    // --- a session's skills
    await openSession(app, S.steer)
    await page.locator('[data-testid=skills-row] button.skills-open').click()
    await page.waitForSelector('.skills-pop')
    await sleep(700)
    await libShot(page, 'skills', 'session-skills-panel', {
      shows: 'A running session\'s Skills panel (from the right sidebar): the skills active in this session and why (everywhere), others that can be added, Use puts /name in the composer',
      how: 'seeded (the server\'s own list for the session)',
      boxes: { panel: '.skills-pop', 'active?': '[data-testid=skills-active]', 'other?': '[data-testid=skills-other]', row: '[data-testid=skills-row]' },
    })
    await page.keyboard.press('Escape')
    await page.locator('textarea.message-input').click()
    await page.locator('textarea.message-input').pressSequentially('/rel', { delay: 60 })
    await sleep(700)
    await libShot(page, 'skills', 'slash-command', {
      shows: 'Typing / in the composer: slash-command suggestions, with the approved skill release-checklist', how: 'seeded',
      boxes: { composer: '.input-area', 'suggestions?': '.slash-autocomplete, .slash-command-autocomplete, [class*=slash]' },
    })
    await page.locator('textarea.message-input').fill('')

    // --- Connectors
    await go(page, server, '/connectors')
    await page.waitForSelector('text=GitHub', { timeout: 15000 })
    await sleep(1200)
    await libShot(page, 'skills', 'connectors-page', {
      shows: 'Connectors page: the n0 card (not connected) and the catalogue; GitHub, Linear and Sentry show as connected with masked keys',
      how: 'seeded (three connections saved with made-up keys through the API; nothing is contacted)',
      boxes: { 'n0?': page.locator('section, div', { hasText: /^.{0,40}n0/ }).filter({ has: page.getByRole('button', { name: /Connect n0/i }) }).last(), 'connectN0?': page.getByRole('button', { name: /Connect n0/i }).first(), 'github?': page.locator('[class*=connector]', { hasText: 'GitHub' }).last() },
    })
    await page.evaluate(() => window.scrollTo(0, 0))
    const connected = page.getByText('Linear', { exact: true }).first()
    await connected.evaluate(el => el.scrollIntoView({ block: 'center' })).catch(() => {})
    await sleep(500)
    await libShot(page, 'skills', 'connectors-connected', {
      shows: 'The connectors catalogue scrolled to the connected ones (GitHub, Linear) with their status', how: 'seeded (made-up keys)',
      boxes: {},
    })

    // --- Providers
    await go(page, server, '/providers')
    await page.waitForSelector('.provider-row', { timeout: 15000 })
    await sleep(900)
    await libShot(page, 'skills', 'providers-page', {
      shows: 'Providers page: every model provider with its default model and whether it is ready', how: 'seeded (the server\'s own list; a made-up DeepSeek key marks one as configured)',
      boxes: { firstProvider: page.locator('.provider-row').first(), 'default?': page.locator('.tag-default').first(), 'ready?': page.locator('.tag-ready').first() },
    })
  } finally { await browser.close() }
}
