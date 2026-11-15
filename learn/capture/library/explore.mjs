import { readServer, libShot, PORT, press } from './lib.mjs'
import { openWorld, openSession, S, go, sleep, permissionRequest, question } from './world.mjs'
const server = readServer(PORT)
const app = await openWorld(server)
const { browser, page, bridge } = app
page.on('console', m => { if (m.type() === 'error') console.log('  [console]', m.text().slice(0, 200)) })
await openSession(app, S.steer)
await libShot(page, '_explore', 'steer', {})
bridge.inject(permissionRequest()); bridge.inject(question())
await sleep(1500)
await libShot(page, '_explore', 'steer-needs', {})
page.on("response", r => { if (r.status() === 404) console.log("404", r.url()) }); await go(page, server, "/agents"); await sleep(2500); await libShot(page, "_explore", "agents-all", {}); await go(page, server, "/projects"); await sleep(1500)
await libShot(page, '_explore', 'projects', {})
console.log([...new Set(bridge.seen.map(m => m.type))].join(' '))
console.log(bridge.sent.map(m => m.type).join(' '))
await browser.close()
