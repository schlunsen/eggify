#!/usr/bin/env node
// Takes the screen library: learn/public/library/shots/<area>/<name>.png + .json.
//
//   learn/capture/library/server.sh                      # in another terminal (port 3420)
//   node learn/capture/library/run.mjs                   # every area
//   node learn/capture/library/run.mjs --only git,zen    # some areas
//   node learn/capture/library/run.mjs --no-seed         # keep the timestamps as they are
//
// Before the shots the library seed is run again, so sessions are "just now" and "2 min ago".
// Proofs (1600 px JPEGs with the boxes drawn) go to learn/capture/library/.proofs/<area>/.
// What each shot shows and how it was made is collected in index/<area>.json (see index.mjs).
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { LIB_DIR, PORT, parseArgs, readServer, taken } from './lib.mjs'
import { seedExtra } from './seed-extra.mjs'

const AREAS = ['projects', 'steering', 'subagents', 'teams', 'cage', 'git', 'remote', 'zen', 'skills', 'misc']
const args = parseArgs()
const server = readServer(Number(args.port || PORT))
const only = typeof args.only === 'string' ? args.only.split(',') : AREAS
if (!args['no-seed']) await seedExtra(server)
mkdirSync(join(LIB_DIR, 'index'), { recursive: true })
let failed = 0
for (const area of only) {
  console.log(`== ${area}`)
  const before = taken.length
  try {
    const mod = await import(`./areas/${area}.mjs`)
    await mod.default(server, args)
  } catch (e) {
    failed++
    console.error(`!! ${area}: ${e.stack || e}`)
  }
  const shots = taken.slice(before)
  if (shots.length) writeFileSync(join(LIB_DIR, 'index', `${area}.json`), JSON.stringify(shots, null, 2) + '\n')
}
process.exit(failed ? 1 : 0)
