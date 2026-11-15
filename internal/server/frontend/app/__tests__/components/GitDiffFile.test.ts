import { mount } from '@vue/test-utils'
import GitDiffFile from '../../components/git/GitDiffFile.vue'

const base = { path: 'src/app.ts', status: 'modified' as const, additions: 1, deletions: 1 }
const diff = 'diff --git a/src/app.ts b/src/app.ts\n--- a/src/app.ts\n+++ b/src/app.ts\n@@ -1 +1 @@\n-old\n+new'

describe('GitDiffFile', () => {
  it('renders numbered lines when expanded and nothing when collapsed', async () => {
    const w = mount(GitDiffFile, { props: { file: { ...base, diff }, collapsed: false, viewed: false } })
    expect(w.findAll('.gdf-line')).toHaveLength(2)
    expect(w.find('.gdf-dir').text()).toBe('src/')
    await w.setProps({ collapsed: true })
    expect(w.find('.gdf-body').exists()).toBe(false)
  })

  it('never renders a binary file as text', () => {
    const w = mount(GitDiffFile, { props: { file: { ...base, path: 'pic.png', binary: true, size: 2048, diff: '\u0089PNG' }, collapsed: false, viewed: false } })
    expect(w.text()).toContain('Binary file · 2.0 KB')
    expect(w.find('.gdf-code').exists()).toBe(false)
  })

  it('offers to load an omitted patch and emits viewed', async () => {
    const w = mount(GitDiffFile, { props: { file: { ...base, omitted: true }, collapsed: false, viewed: false } })
    await w.find('.gdf-link').trigger('click')
    expect(w.emitted('load')).toHaveLength(1)
    await w.find('.gdf-viewed input').setValue(true)
    expect(w.emitted('viewed')![0]).toEqual([true])
  })
})
