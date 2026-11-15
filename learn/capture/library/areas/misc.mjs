// Odds and ends: Settings, the shortcuts sheet, What's new, Performance.
import { libShot, press } from '../lib.mjs'
import { openWorld, openSession, S, go, sleep } from '../world.mjs'

function perfBody() {
  const now = Date.now(), n = 80          // 40 minutes of samples, one every 30 s
  const wave = (i, base, amp, period) => +(base + amp * Math.sin(i / period) + amp * 0.3 * Math.sin(i / 3.1)).toFixed(1)
  const samples = Array.from({ length: n }, (_, i) => ({
    time: new Date(now - (n - i) * 30000).toISOString(), goroutines: Math.round(wave(i, 142, 9, 7)), heap_mb: wave(i, 61, 6, 9), sys_mb: 118,
    rss_mb: wave(i, 164, 8, 11), cpu_percent: Math.max(0.4, wave(i, 3.2, 2.6, 5)), open_fds: Math.round(wave(i, 74, 4, 8)), children: 7,
    children_rss_mb: wave(i, 1480, 120, 13), db_mb: 38.4, wal_mb: wave(i, 2.1, 1.2, 6), extra: { ws_clients: 2 },
  }))
  const client = Array.from({ length: n }, (_, i) => ({
    time: new Date(now - (n - i) * 30000).toISOString(), dom_nodes: Math.round(wave(i, 4200, 260, 10)), animations: Math.round(wave(i, 6, 2, 4)), fps: Math.round(wave(i, 59, 1, 5)),
    messages: 180 + i, sessions: 21, js_heap_mb: wave(i, 96, 7, 12), max_stall_ms: Math.round(wave(i, 28, 12, 6)), visible: true,
  }))
  return { started_at: new Date(now - 6 * 3600000).toISOString(), interval_seconds: 30, samples, client, findings: [] }
}

export default async function (server) {
  const app = await openWorld(server)
  const { browser, context, page } = app
  try {
    await context.route('**/api/perf', route => (route.request().method() === 'GET' ? route.fulfill({ json: perfBody() }) : route.continue()))

    await go(page, server, '/settings')
    await page.waitForSelector('.settings-nav__item')
    await sleep(900)
    await libShot(page, 'misc', 'settings-home', {
      shows: 'Settings: the section list on the left (Account & security … What\'s new) and Account & security open',
      how: 'seeded (the signed-in devices card shows this demo server\'s own sessions)',
      boxes: { nav: page.locator('.settings-nav__item').first().locator('xpath=..'), account: '#account' },
    })
    for (const [id, name, shows] of [
      ['appearance', 'settings-appearance', 'Settings → Appearance: theme and display options'],
      ['agents', 'settings-agents', 'Settings → Agents: what happens when nobody answers an agent\'s question (question policy), continuing after an update or a usage limit'],
      ['shortcuts', 'settings-shortcuts', 'Settings → Keyboard shortcuts: every shortcut with its key; click a key to record your own'],
      ['agent-logins', 'settings-agent-logins', 'Settings → Agents & logins: Claude Code and Codex installed and signed in'],
      ['performance', 'settings-performance', 'Settings → Performance: memory, CPU, child processes, goroutines, database and page readings as tiles with sparklines over the last 40 minutes'],
    ]) {
      await go(page, server, `/settings#${id}`)
      await page.waitForSelector(`#${id}`)
      await page.locator(`#${id}`).evaluate(el => el.scrollIntoView({ block: 'start' }))
      await sleep(1200)
      await libShot(page, 'misc', name, {
        shows, pad: 4,
        how: id === 'performance' ? 'stubbed API (/api/perf with 40 minutes of made-up, calm samples)' : id === 'agent-logins' ? 'stubbed API (/api/agent/runtimes: both CLIs installed and signed in, masked demo account)' : 'seeded (the server\'s own settings)',
        boxes: { section: `#${id}`, 'tiles?': id === 'performance' ? page.locator('.pm-tile').first().locator('xpath=..') : '#none', 'firstTile?': id === 'performance' ? page.locator('.pm-tile').first() : '#none' },
      })
    }

    // shortcuts sheet and What's new, over the agents page
    await openSession(app, S.steer)
    await press(page, 'Meta+Slash', 900)
    await page.waitForSelector('.sheet[role=dialog]')
    await sleep(500)
    await libShot(page, 'misc', 'shortcuts-sheet', {
      shows: 'The keyboard shortcuts sheet (⌘/): every shortcut by group, with the keys that apply in this browser', how: 'seeded',
      boxes: { sheet: '.sheet[role=dialog]' },
    })
    await page.keyboard.press('Escape')
    await page.locator('[data-testid=version-badge]').click()
    await page.waitForSelector('[data-testid=changelog-modal]')
    await sleep(900)
    await libShot(page, 'misc', 'whats-new', {
      shows: 'What\'s new: the in-app changelog opened from the header\'s version badge, newest release first', how: 'seeded (the repository\'s own CHANGELOG.md, built into the server)',
      boxes: { modal: '[data-testid=changelog-modal]', badge: '[data-testid=version-badge]' },
    })
  } finally { await browser.close() }
}
