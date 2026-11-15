// Renders the web Circus scene's art (sceneCircus.ts, CircusFigure.vue,
// public/zen/circus/metrics.webp) into PNG imagesets for the iOS Zen view.
// Needs rsvg-convert and ImageMagick (brew install librsvg imagemagick) and the
// frontend's node_modules (for esbuild). Run: node scripts/ios-circus-assets.mjs
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const FE = `${REPO}/internal/server/frontend`
const ASSETS = `${REPO}/ios/eggify/eggify/Assets.xcassets`
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), 'circus-svg-'))
fs.mkdirSync(TMP, { recursive: true })

// ── Scene art: evaluate the SVG constants from sceneCircus.ts ──
// esbuild comes from the frontend's node_modules, or from the main checkout when
// this runs in a worktree that has none (ESBUILD_MAIN overrides the path).
function findEsbuild() {
  const candidates = [process.env.ESBUILD_MAIN, `${FE}/node_modules/esbuild/lib/main.js`]
  const common = execFileSync('git', ['-C', REPO, 'rev-parse', '--git-common-dir']).toString().trim()
  candidates.push(`${path.resolve(REPO, common, '..')}/internal/server/frontend/node_modules/esbuild/lib/main.js`)
  const hit = candidates.find(c => c && fs.existsSync(c))
  if (!hit) throw new Error('esbuild not found; run npm install in internal/server/frontend')
  return hit
}
const { transformSync, buildSync } = await import(findEsbuild())

let src = fs.readFileSync(`${FE}/app/composables/agents/zen/sceneCircus.ts`, 'utf8')
const start = src.indexOf("const INK = ")
const end = src.indexOf('export function createCircusScene')
let code = transformSync(src.slice(start, end), { loader: 'ts' }).code
const art = new Function(code + '; return { INK, PALETTE, BUNTING_SVG, CURTAIN_SVG, BOOK_SVG, QUILL_SVG, MAGNIFIER_SVG, PUFF_SVG, BURST_SVG }')()

// Bundles one of the web's pure TS modules (backdrops, props) and returns its exports.
function loadModule(file) {
  const out = buildSync({ entryPoints: [`${FE}/app/composables/agents/zen/${file}`], bundle: true, write: false, format: 'cjs', platform: 'node', logLevel: 'silent' })
  const mod = { exports: {} }
  new Function('module', 'exports', out.outputFiles[0].text)(mod, mod.exports)
  return mod.exports
}
const backdrops = loadModule('circusBackdrops.ts')
const props = loadModule('circusProps.ts')

const ns = s => s.includes('xmlns=') ? s : s.replace('<svg ', '<svg xmlns="http://www.w3.org/2000/svg" ')

function imageset(name, file, svg, width) {
  const dir = `${ASSETS}/${name}.imageset`
  fs.mkdirSync(dir, { recursive: true })
  const svgPath = `${TMP}/${file}.svg`
  fs.writeFileSync(svgPath, ns(svg))
  execFileSync('rsvg-convert', ['-w', String(width), '-o', `${dir}/${file}.png`, svgPath])
  fs.writeFileSync(`${dir}/Contents.json`, JSON.stringify({
    images: [{ filename: `${file}.png`, idiom: 'universal' }],
    info: { author: 'xcode', version: 1 },
  }, null, 2) + '\n')
}

const sized = (svg, w, h) => svg.replace('<svg ', `<svg width="${w}" height="${h}" `)

// The ten backdrops, one JPEG each (opaque, so JPEG keeps the app small). The
// beat-driven lightning flash is left out of the still picture.
for (const info of backdrops.CIRCUS_BACKDROPS) {
  // The engraved cut-outs are WebP files; rsvg reads PNGs next to the SVG.
  const svg = backdrops.backdropSvg(info.id)
    .replace(/<path class="cc-b-flash"[^>]*\/>/g, '')
    .replace(/<g class="cc-b-flash">[\s\S]*?<\/g>/g, '')
    .replace(/href="\/zen\/circus\/backdrops\/([a-z-]+)\.webp"/g, (_m, name) => {
      const png = `${TMP}/art-${name}.png`
      if (!fs.existsSync(png)) execFileSync('magick', [`${FE}/public/zen/circus/backdrops/${name}.webp`, png])
      return `href="art-${name}.png"`
    })
    .replace('<svg ', '<svg width="1600" height="900" ')
  const Name = info.id[0].toUpperCase() + info.id.slice(1)
  const dir = `${ASSETS}/CircusBackdrop${Name}.imageset`
  fs.mkdirSync(dir, { recursive: true })
  const svgPath = `${TMP}/backdrop-${info.id}.svg`
  const pngPath = `${TMP}/backdrop-${info.id}.png`
  fs.writeFileSync(svgPath, ns(svg))
  execFileSync('rsvg-convert', ['-w', '1920', '-b', '#ffffff', '-o', pngPath, svgPath])
  execFileSync('magick', [pngPath, '-quality', '74', '-sampling-factor', '4:2:0', `${dir}/backdrop-${info.id}.jpg`])
  fs.writeFileSync(`${dir}/Contents.json`, JSON.stringify({
    images: [{ filename: `backdrop-${info.id}.jpg`, idiom: 'universal' }],
    info: { author: 'xcode', version: 1 },
  }, null, 2) + '\n')
}
// The old single backdrop is the meadow now.
fs.rmSync(`${ASSETS}/CircusBackdrop.imageset`, { recursive: true, force: true })

// Props and costumes (ambient gags, hats on the egg, the tea parade, the bureaucrat).
const cap = s => s[0].toUpperCase() + s.slice(1)
const propSet = (name, file, a, scale = 3) =>
  imageset(name, file, a.svg.replace('<svg ', `<svg width="${a.w}" height="${a.h}" `), Math.round(a.w * scale))
for (const [k, a] of Object.entries(props.CROSSING_ART)) propSet(`CircusProp${cap(k)}`, `prop-${k}`, a)
for (const [k, a] of Object.entries(props.HAT_ART)) if (k !== 'bowler') propSet(`CircusHat${cap(k)}`, `hat-${k}`, a)
propSet('CircusTeapot', 'teapot', props.TEAPOT_ART)
propSet('CircusTeacup', 'teacup', props.TEACUP_ART)
propSet('CircusStalkHead', 'stalkhead', props.STALK_HEAD_ART)
imageset('CircusCurtainLeft', 'curtain-left', sized(art.CURTAIN_SVG('l').trim(), 120, 900), 240)
imageset('CircusCurtainRight', 'curtain-right', sized(art.CURTAIN_SVG('r').trim(), 120, 900), 240)
imageset('CircusBook', 'book', sized(art.BOOK_SVG.trim(), 120, 70), 360)
imageset('CircusQuill', 'quill', sized(art.QUILL_SVG.trim(), 60, 120), 180)
imageset('CircusMagnifier', 'magnifier', sized(art.MAGNIFIER_SVG.trim(), 110, 110), 330)
imageset('CircusPuff', 'puff', sized(art.PUFF_SVG.trim(), 80, 60), 240)
// One burst in white, tinted on the device.
imageset('CircusBurst', 'burst', sized(art.BURST_SVG('#ffffff').trim(), 100, 100), 300)

// The engraved cloud cut-out (the web shows it as an <img>), downscaled.
{
  const dir = `${ASSETS}/CircusCloud.imageset`
  fs.rmSync(dir, { recursive: true, force: true })
  fs.mkdirSync(dir, { recursive: true })
  execFileSync('magick', [`${FE}/public/zen/circus/backdrops/cloud.webp`, '-resize', '360x', `${dir}/cloud.png`])
  fs.writeFileSync(`${dir}/Contents.json`, JSON.stringify({
    images: [{ filename: 'cloud.png', idiom: 'universal' }],
    info: { author: 'xcode', version: 1 },
  }, null, 2) + '\n')
}

// The steam machine (metrics.webp) as PNG.
{
  const dir = `${ASSETS}/CircusSteamMachine.imageset`
  fs.mkdirSync(dir, { recursive: true })
  execFileSync('magick', [`${FE}/public/zen/circus/metrics.webp`, `${dir}/steam-machine.png`])
  fs.writeFileSync(`${dir}/Contents.json`, JSON.stringify({
    images: [{ filename: 'steam-machine.png', idiom: 'universal' }],
    info: { author: 'xcode', version: 1 },
  }, null, 2) + '\n')
}

// ── Subagent figures from CircusFigure.vue ──
// Each kind becomes three images: the figure with coat/accent left white
// (cut-out edge and shadow included), and a luminance mask each for the coat
// and the accent, so the app tints them with the subagent's own colours.
const vue = fs.readFileSync(`${FE}/app/components/agents/CircusFigure.vue`, 'utf8')
const tpl = vue.slice(vue.indexOf('<template>'), vue.indexOf('</template>\n\n<script'))
const groups = {}
const re = /<g v-(?:else-)?if="look\.kind === '(\w+)'">([\s\S]*?)<\/g>\n\n/g
let m
while ((m = re.exec(tpl))) groups[m[1]] = m[2]
groups.lady = tpl.match(/<g v-else>([\s\S]*?)<\/g>\n\n/)[1]
if (Object.keys(groups).length !== 6) throw new Error('expected 6 figure kinds, got ' + Object.keys(groups))

// Running face: two dot eyes (not the knight), moustache (gent/egg), mouth (not knight/parrot).
function face(kind) {
  const shift = kind === 'egg' ? 'translate(0 12)' : kind === 'parrot' ? 'translate(4 1)' : ''
  let f = ''
  if (kind !== 'knight') f += '<circle class="ink-fill" cx="28.6" cy="21.6" r="1.2"/><circle class="ink-fill" cx="35.4" cy="21.6" r="1.2"/>'
  if (kind === 'gent' || kind === 'egg') f += '<path class="ink-fill" d="M32 25.6 C29 24 26 25.5 24.5 27.5 C27 26.6 29 27 32 27.2 C35 27 37 26.6 39.5 27.5 C38 25.5 35 24 32 25.6 Z"/>'
  if (kind !== 'knight' && kind !== 'parrot') f += '<ellipse class="mouth" cx="32" cy="28.6" rx="1.8" ry="0.5"/>'
  return `<g transform="${shift}">${f}</g>`
}

const css = vue.slice(vue.indexOf('<style scoped>') + 14, vue.indexOf('/* Cut-out animation'))
  .replace(/^\.talking[^\n]*\n|^@keyframes[^\n]*\n/gm, '')
  .replace(/transform-box:[^;]*;|transform-origin:[^;]*;|transform: scaleY\(\.5\);/g, '')
  .replace('color-mix(in srgb, var(--c) 70%, #12203a)', '#2f5f9e') // parrot is always #3d7cc9
  .replace('color-mix(in srgb, var(--c) 45%, #fbf5e6)', '#a8bfdc')
  .replaceAll('var(--c)', '#ffffff')
  .replaceAll('var(--acc)', '#ffffff')

const EDGE = `<filter id="edge" x="-25%" y="-20%" width="150%" height="140%">
  <feMorphology in="SourceAlpha" operator="dilate" radius="1.7" result="grown"/>
  <feFlood flood-color="#fbf5e6"/><feComposite in2="grown" operator="in" result="edge"/>
  <feOffset in="grown" dx="2" dy="2.4" result="moved"/>
  <feFlood flood-color="#2b1d10" flood-opacity="0.24"/><feComposite in2="moved" operator="in" result="shadow"/>
  <feMerge><feMergeNode in="shadow"/><feMergeNode in="edge"/><feMergeNode in="SourceGraphic"/></feMerge>
</filter>`

// The figure is drawn in a padded viewBox so the edge and the plume are not clipped.
const VB = '-6 -6 76 96'
const fig = (body, style, filter) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${VB}" width="76" height="96">
<defs>${EDGE}<style>${style}</style></defs>
<g ${filter ? 'filter="url(#edge)"' : ''}>${body}</g></svg>`

// Masks: everything black (so later shapes hide what is under them), the part white.
const maskCSS = part => `* { fill: #000 !important; stroke: #000 !important; }
${part.map(c => `.${c}`).join(', ')} { fill: #fff !important; stroke: none !important; }
.umbrella, .slit, .brace, .limb, .ink-line, .beakish { fill: none !important; }`

for (const [kind, body] of Object.entries(groups)) {
  const full = body + face(kind)
  const Kind = kind[0].toUpperCase() + kind.slice(1)
  imageset(`CircusFigure${Kind}`, `figure-${kind}`, fig(full, css, true), 228)
  imageset(`CircusFigure${Kind}Coat`, `figure-${kind}-coat`, fig(full, maskCSS(['coat']), false), 228)
  imageset(`CircusFigure${Kind}Accent`, `figure-${kind}-accent`, fig(full, maskCSS(['accent', 'plume']), false), 228)
}
console.log('ok', Object.keys(groups))
