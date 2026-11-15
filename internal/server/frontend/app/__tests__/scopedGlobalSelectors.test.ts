import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { expect, it } from 'vitest'

// In a scoped <style>, Vue compiles `:global(.a) .b { … }` to `.a { … }`: everything after
// `:global(...)` is dropped. `:global(html.desktop-mac) .sv-header { padding-left: 76px }`
// padded the whole Mac app that way. Put the full selector inside :global(), or use an
// unscoped <style> block.
function vueFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (name === 'node_modules' || name.startsWith('.')) return []
    if (statSync(path).isDirectory()) return vueFiles(path)
    return name.endsWith('.vue') ? [path] : []
  })
}

it('scoped styles never put a selector after :global(...)', () => {
  const offenders: string[] = []
  for (const file of vueFiles(join(__dirname, '..'))) {
    const source = readFileSync(file, 'utf8')
    for (const block of source.matchAll(/<style\b[^>]*\bscoped\b[^>]*>([\s\S]*?)<\/style>/g)) {
      const css = block[1]!.replace(/\/\*[\s\S]*?\*\//g, '')
      for (const m of css.matchAll(/:global\([^)]*\)[ \t]*[^\s{,][^{]*\{/g)) offenders.push(`${file}: ${m[0]}`)
    }
  }
  expect(offenders).toEqual([])
})
