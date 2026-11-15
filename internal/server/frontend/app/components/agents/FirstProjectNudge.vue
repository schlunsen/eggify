<template>
  <section class="fpn" aria-labelledby="fpn-title" data-test="first-project-nudge">
    <img class="fpn__egg" :src="circusEggArt('gent')" alt="" draggable="false">
    <h2 id="fpn-title">Start with a project</h2>
    <p>Agents work in a project folder, so pick the one you want to start in.</p>
    <div class="fpn__options">
      <button
        v-for="opt in PROJECT_START_OPTIONS"
        :key="opt.mode"
        type="button"
        class="fpn__option"
        :data-mode="opt.mode"
        @click="emit('choose', opt.mode)"
      >
        <span class="fpn__icon"><ProjectsGlyph :name="opt.icon" :size="20" /></span>
        <span class="fpn__title">{{ opt.title }}</span>
        <span class="fpn__desc">{{ opt.desc }}</span>
      </button>
    </div>
    <button type="button" class="fpn__later" data-test="fpn-dismiss" @click="emit('dismiss')">Not now</button>
  </section>
</template>

<script setup lang="ts">
import ProjectsGlyph from '~/components/projects/ProjectsGlyph.vue'
import { circusEggArt } from '~/utils/agents/circusCharacters'
import { PROJECT_START_OPTIONS, type ProjectSetupMode } from '~/utils/projects/projectSetup'

// First-run card for the agents page's empty state (see useFirstProjectNudge).
const emit = defineEmits<{
  choose: [mode: ProjectSetupMode]
  dismiss: []
}>()
</script>

<style scoped>
.fpn { width: 100%; margin: auto; max-width: 720px; padding: 32px 24px 24px; text-align: center; background: var(--card-bg); border: 1px solid var(--border-color); border-radius: 14px; }
.fpn__egg { display: block; height: 72px; width: auto; margin: 0 auto 10px; }
.fpn h2 { margin: 0; font-size: 1.2rem; font-weight: 650; color: var(--text-primary); }
.fpn > p { margin: 6px auto 22px; max-width: 420px; font-size: 0.9rem; color: var(--text-secondary); }
.fpn__options { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.fpn__option { display: flex; flex-direction: column; align-items: flex-start; gap: 4px; padding: 16px; font: inherit; text-align: left; color: inherit; background: var(--bg-primary); border: 1px solid var(--border-color); border-radius: 12px; cursor: pointer; transition: border-color 0.12s, box-shadow 0.12s; }
.fpn__option:hover { border-color: var(--primary-border-hover, var(--accent-purple)); }
.fpn__option:focus-visible, .fpn__later:focus-visible { outline: none; border-color: var(--accent-purple); box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.2); }
.fpn__icon { display: inline-flex; align-items: center; justify-content: center; width: 38px; height: 38px; margin-bottom: 8px; border-radius: 10px; color: var(--accent-purple); background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.1); }
.fpn__title { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); }
.fpn__desc { font-size: 0.8rem; color: var(--text-secondary); }
.fpn__later { margin-top: 16px; padding: 6px 12px; font: inherit; font-size: 0.82rem; color: var(--text-secondary); background: none; border: 1px solid transparent; border-radius: 8px; cursor: pointer; }
.fpn__later:hover { color: var(--text-primary); }

@media (max-width: 720px) {
  .fpn { padding: 16px 14px 12px; }
  .fpn__egg { display: none; }
  .fpn > p { margin-bottom: 14px; }
  .fpn__options { grid-template-columns: 1fr; gap: 8px; }
  .fpn__option { display: grid; grid-template-columns: auto 1fr; column-gap: 12px; row-gap: 0; padding: 10px 12px; align-items: center; }
  .fpn__icon { grid-row: 1 / span 2; margin-bottom: 0; }
  .fpn__later { margin-top: 10px; }
}
@media (prefers-reduced-motion: reduce) {
  .fpn__option { transition: none; }
}
</style>
