#!/usr/bin/env node
// Contact sheets of the proofs: node sheet.mjs <area> [names…] → .proofs/_sheets/<area>-<n>.jpg (2×2, 1600 px wide)
import sharp from 'sharp'
import { mkdirSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { PROOFS } from './lib.mjs'
const [area, ...names] = process.argv.slice(2)
const dir = join(PROOFS, area)
const files = (names.length ? names.map(n => `${n}.jpg`) : readdirSync(dir).filter(f => f.endsWith('.jpg')).sort())
mkdirSync(join(PROOFS, '_sheets'), { recursive: true })
for (let i = 0; i < files.length; i += 4) {
  const group = files.slice(i, i + 4)
  const tiles = await Promise.all(group.map(async (f, k) => ({
    input: await sharp(join(dir, f)).resize(800, 450).jpeg({ quality: 78 }).toBuffer(), left: (k % 2) * 800, top: Math.floor(k / 2) * 450,
  })))
  const out = join(PROOFS, '_sheets', `${area}-${i / 4 + 1}.jpg`)
  await sharp({ create: { width: 1600, height: group.length > 2 ? 900 : 450, channels: 3, background: '#222' } }).composite(tiles).jpeg({ quality: 78 }).toFile(out)
  console.log(out, group.join(' '))
}
