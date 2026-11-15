<template>
  <Teleport :to="teleportTarget">
    <Transition name="skills-pop">
      <div v-if="open" class="skills-pop-overlay" @mousedown.self="close">
        <div
          ref="dialogEl"
          class="skills-pop"
          role="dialog"
          aria-modal="true"
          :aria-label="`Skills of ${sessionName || 'this session'}`"
          tabindex="-1"
        >
          <div class="skills-pop__head">
            <span class="skills-pop__title">Skills</span>
            <span v-if="sessionName" class="skills-pop__session">{{ sessionName }}</span>
            <button type="button" class="skills-pop__close" aria-label="Close" @click="close">×</button>
          </div>

          <input
            v-if="showSearch"
            ref="searchEl"
            v-model="query"
            class="skills-pop__search"
            type="search"
            placeholder="Search skills…"
            aria-label="Search skills"
            autocomplete="off"
            spellcheck="false"
          >

          <div class="skills-pop__body">
            <p v-if="!state?.loaded && state?.loading" class="skills-pop__status" role="status">Loading skills…</p>
            <p v-else-if="state?.error && !skills.length" class="skills-pop__status" role="alert">{{ state.error }}</p>

            <div v-else-if="state?.loaded && !skills.length" class="skills-pop__empty" data-testid="skills-empty">
              <p>No approved skills yet.</p>
              <NuxtLink to="/skills" class="skills-pop__link" @click="close">Open the Skills page</NuxtLink>
            </div>

            <template v-else>
              <section v-if="groups.active.length" class="skills-pop__group" data-testid="skills-active">
                <h3 class="skills-pop__group-title">In this session</h3>
                <ul class="skills-pop__list">
                  <li v-for="skill in groups.active" :key="skill.id" class="skills-pop__item" :data-skill="skill.name">
                    <div class="skills-pop__text">
                      <div class="skills-pop__name-row">
                        <span class="skills-pop__name">/{{ skill.name }}</span>
                        <span class="skills-pop__reach" :class="`reach-${skill.reach}`">{{ reachLabel(skill.reach) }}</span>
                      </div>
                      <p v-if="skill.description" class="skills-pop__desc" :title="skill.description">{{ skill.description }}</p>
                    </div>
                    <div class="skills-pop__actions">
                      <button
                        v-if="skill.user_invocable"
                        type="button"
                        class="skills-pop__btn skills-pop__btn--primary"
                        data-action="use"
                        :title="`Type /${skill.name} in the chat`"
                        @click="use(skill)"
                      >Use</button>
                      <button
                        v-if="canRemoveSkill(skill)"
                        type="button"
                        class="skills-pop__btn"
                        data-action="remove"
                        :disabled="state?.saving"
                        @click="change(skill, false)"
                      >Remove</button>
                    </div>
                  </li>
                </ul>
              </section>

              <section v-if="groups.other.length" class="skills-pop__group" data-testid="skills-other">
                <h3 class="skills-pop__group-title">Other projects' skills</h3>
                <ul class="skills-pop__list">
                  <li v-for="skill in groups.other" :key="skill.id" class="skills-pop__item" :data-skill="skill.name">
                    <div class="skills-pop__text">
                      <span class="skills-pop__name">/{{ skill.name }}</span>
                      <p v-if="skill.description" class="skills-pop__desc" :title="skill.description">{{ skill.description }}</p>
                    </div>
                    <div class="skills-pop__actions">
                      <button
                        type="button"
                        class="skills-pop__btn"
                        data-action="add"
                        :disabled="state?.saving"
                        @click="change(skill, true)"
                      >Add to this session</button>
                    </div>
                  </li>
                </ul>
              </section>

              <p v-if="query && !groups.active.length && !groups.other.length" class="skills-pop__status">No skills match “{{ query }}”.</p>
            </template>
          </div>

          <p v-if="saveError" class="skills-pop__error" role="alert">{{ saveError }}</p>
          <p v-if="note" class="skills-pop__note" data-testid="skills-note">{{ note }}</p>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useClosable } from '~/composables/useKeyboardShortcuts'
import { useSessionSkills } from '~/composables/agents/useSessionSkills'
import {
  SKILLS_SEARCH_THRESHOLD, canRemoveSkill, filterSessionSkills, groupSessionSkills, reachLabel,
  sessionSkillsNote, skillSlashText, type SessionSkill,
} from '~/utils/agents/sessionSkills'

/**
 * The open session's skills: which approved skills it gets and why, "Use"
 * (types /<name> in the chat), and adding another project's skill to it or
 * removing one that was added. Opened from the right sidebar's Skills row.
 */
const props = defineProps<{ sessionId: string | null; sessionName?: string }>()
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{
  /** "Use": the slash command text for the composer */
  (e: 'use', text: string): void
}>()

const sessionSkills = useSessionSkills()
const dialogEl = ref<HTMLElement | null>(null)
const searchEl = ref<HTMLInputElement | null>(null)
const query = ref('')
const saveError = ref('')
const teleportTarget = ref<string | Element>('body')
let returnFocus: HTMLElement | null = null

const state = computed(() => sessionSkills.stateFor(props.sessionId))
const skills = computed<SessionSkill[]>(() => state.value?.data?.skills || [])
const showSearch = computed(() => skills.value.length > SKILLS_SEARCH_THRESHOLD)
const groups = computed(() => groupSessionSkills(filterSessionSkills(skills.value, showSearch.value ? query.value : '')))
const note = computed(() => sessionSkillsNote(state.value?.data))

watch(open, (isOpen) => {
  if (isOpen) {
    if (!props.sessionId) { open.value = false; return }
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    // In Zen full screen only the full-screen element is visible
    teleportTarget.value = document.fullscreenElement || 'body'
    query.value = ''
    saveError.value = ''
    void sessionSkills.load(props.sessionId).then(focusFirst)
    void nextTick(focusFirst)
  } else {
    const el = returnFocus
    returnFocus = null
    if (el?.isConnected) void nextTick(() => el.focus())
  }
}, { immediate: true })

// The session changed under an open panel: close it rather than change the wrong one
watch(() => props.sessionId, () => { if (open.value) close() })

function focusFirst() {
  if (!open.value) return
  if (searchEl.value) searchEl.value.focus()
  else if (dialogEl.value && !dialogEl.value.contains(document.activeElement)) dialogEl.value.focus()
}

function close() {
  open.value = false
}

function use(skill: SessionSkill) {
  emit('use', skillSlashText(skill.name))
  // The page focuses the composer; don't hand focus back to the opener.
  returnFocus = null
  close()
}

async function change(skill: SessionSkill, add: boolean) {
  if (!props.sessionId) return
  saveError.value = ''
  const result = await sessionSkills.toggle(props.sessionId, skill.id, add)
  if (!result.ok) saveError.value = result.error || "Couldn't change this session's skills."
}

// Escape closes (the newest open thing first)
useClosable(open, close, { modal: true })

defineExpose({ close })
</script>

<style scoped>
.skills-pop-overlay {
  position: fixed;
  inset: 0;
  z-index: 1004;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 12vh 16px 16px;
  background: rgba(0, 0, 0, 0.25);
}
.skills-pop {
  display: flex;
  flex-direction: column;
  width: min(480px, 100%);
  max-height: 76vh;
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  box-shadow: 0 16px 40px rgba(0, 0, 0, 0.3);
  padding: 14px;
  outline: none;
}
.skills-pop__head { display: flex; align-items: baseline; gap: 8px; margin-bottom: 10px; min-width: 0; }
.skills-pop__title { font-weight: 600; font-size: 14px; }
.skills-pop__session { flex: 1; min-width: 0; font-size: 12px; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.skills-pop__close { margin-left: auto; background: none; border: none; color: var(--text-secondary); cursor: pointer; font-size: 18px; line-height: 1; padding: 0 2px; }
.skills-pop__close:hover { color: var(--text-primary); }
.skills-pop__search {
  width: 100%;
  margin-bottom: 10px;
  padding: 6px 8px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  background: var(--bg-primary);
  color: inherit;
  font: inherit;
  font-size: 13px;
  outline: none;
}
.skills-pop__search:focus { border-color: var(--accent-primary, var(--border-color)); }
.skills-pop__body { flex: 1; min-height: 0; overflow-y: auto; }
.skills-pop__status { margin: 8px 0; font-size: 13px; color: var(--text-secondary); }
.skills-pop__empty { padding: 8px 0; font-size: 13px; color: var(--text-secondary); }
.skills-pop__empty p { margin: 0 0 6px; }
.skills-pop__link { color: var(--accent-primary, var(--text-primary)); font-weight: 500; }
.skills-pop__group + .skills-pop__group { margin-top: 12px; }
.skills-pop__group-title { margin: 0 0 4px; font-size: 11px; font-weight: 600; letter-spacing: 0.3px; text-transform: uppercase; color: var(--text-secondary); }
.skills-pop__list { list-style: none; margin: 0; padding: 0; }
.skills-pop__item { display: flex; align-items: center; gap: 10px; padding: 7px 6px; border-radius: 8px; }
.skills-pop__item:hover { background: var(--overlay-bg-hover, var(--bg-primary)); }
.skills-pop__text { flex: 1; min-width: 0; }
.skills-pop__name-row { display: flex; align-items: center; gap: 6px; min-width: 0; }
.skills-pop__name { font-family: 'Monaco', 'Menlo', monospace; font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.skills-pop__reach {
  flex-shrink: 0;
  padding: 1px 6px;
  border-radius: 999px;
  font-size: 10px;
  color: var(--text-secondary);
  border: 1px solid var(--border-color);
}
.skills-pop__reach.reach-added { color: var(--accent-purple, var(--text-primary)); border-color: currentColor; }
.skills-pop__desc { margin: 2px 0 0; font-size: 12px; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.skills-pop__actions { display: flex; gap: 6px; flex-shrink: 0; }
.skills-pop__btn {
  padding: 3px 9px;
  border-radius: 6px;
  border: 1px solid var(--border-color);
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
}
.skills-pop__btn:hover:not(:disabled) { background: var(--bg-tertiary, var(--bg-primary)); }
.skills-pop__btn:disabled { opacity: 0.5; cursor: default; }
.skills-pop__btn:focus-visible { outline: 2px solid var(--accent-purple, var(--border-color)); outline-offset: 1px; }
.skills-pop__btn--primary { border-color: var(--accent-purple, var(--border-color)); color: var(--accent-purple, var(--text-primary)); font-weight: 600; }
.skills-pop__error { margin: 8px 0 0; font-size: 12px; color: var(--status-error, var(--text-primary)); }
.skills-pop__note { margin: 10px 0 0; padding-top: 8px; border-top: 1px solid var(--border-color); font-size: 12px; color: var(--text-secondary); }
.skills-pop-enter-active, .skills-pop-leave-active { transition: opacity 0.12s; }
.skills-pop-enter-from, .skills-pop-leave-to { opacity: 0; }
@media (prefers-reduced-motion: reduce) {
  .skills-pop-enter-active, .skills-pop-leave-active { transition: none; }
}
@media (max-width: 480px) {
  .skills-pop__item { flex-wrap: wrap; }
  .skills-pop__actions { width: 100%; justify-content: flex-end; }
}
</style>
