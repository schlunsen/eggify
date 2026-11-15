import { ref } from 'vue'
import { hasUnseenChanges, markChangelogSeen, readSeenVersion } from '~/utils/changelog'

// Shared by the header badge, Settings and the command palette; ChangelogModal
// (mounted once in the default layout) shows it.
const open = ref(false)
const unseen = ref(false)
// The last seen version from before the dialog opened: releases above it are "new".
const seenBefore = ref<string | null>(null)
let currentVersion = ''

export function useChangelog() {
  /** Call once the running version is known. Returns true when it is newer than the last seen one. */
  function checkForUpdate(version: string): boolean {
    currentVersion = version
    unseen.value = hasUnseenChanges(version)
    return unseen.value
  }
  function openChangelog() {
    seenBefore.value = readSeenVersion()
    open.value = true
    markChangelogSeen(currentVersion)
    unseen.value = false
  }
  function closeChangelog() {
    open.value = false
  }
  return { open, unseen, seenBefore, checkForUpdate, openChangelog, closeChangelog }
}
