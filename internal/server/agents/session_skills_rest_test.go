package agents

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/skills"
	"github.com/stretchr/testify/require"
)

// A running session lists every approved skill with why it applies, and a
// project's skill from another folder can be added to it (and removed again).
func TestSessionSkillsListAndAdd(t *testing.T) {
	sm, repo, home := newNativeSkillsManager(t)
	h := &AgentHandler{SessionManager: sm}
	personal := approvedUISkill(t, repo, "notes", "Take notes", "Write notes.")

	other := filepath.Join(home, "projects", "film", ".claude", "skills", "render-tips", "SKILL.md")
	require.NoError(t, repo.Skill.SaveSkill(&database.Skill{Name: "render-tips", Scope: "project", Path: other, Description: "Render tips", Body: "Use ffmpeg."}))
	project, err := repo.Skill.GetSkillByName("render-tips")
	require.NoError(t, err)
	if project.Status != database.ReviewStatusApproved {
		_, err = repo.Skill.ReviewSkill(project.ID, database.SkillReview{Status: database.ReviewStatusApproved, ReviewedBy: "user:alice"})
		require.NoError(t, err)
	}

	wd := filepath.Join(home, "projects", "app")
	s := seedSwitchSession(t, sm, "claude", "sonnet", func(x *Session) { x.Options.WorkingDirectory = &wd })

	reachOf := func(st *SessionSkillsState) map[string]string {
		out := map[string]string{}
		for _, sk := range st.Skills {
			out[sk.Name] = sk.Reach
		}
		return out
	}
	st, err := h.SessionSkills(s.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"notes": skills.ReachEverywhere, "render-tips": ""}, reachOf(st))
	require.Equal(t, "claude-sdk", st.Runtime)

	// Unknown ids are dropped; names resolve to ids; each once.
	st, err = h.SetSessionAddedSkills(s.ID, []string{"render-tips", project.ID, "no-such-skill"})
	require.NoError(t, err)
	require.Equal(t, skills.ReachAdded, reachOf(st)["render-tips"])
	require.Equal(t, []string{project.ID}, s.Options.EnabledSkillIDs)
	require.Contains(t, pluginSkillFiles(t, sm, &s.Options), "render-tips/SKILL.md")

	// Persisted, so a restart keeps it.
	meta, err := sm.Storage.GetSession(s.ID)
	require.NoError(t, err)
	var stored SessionOptions
	require.NoError(t, json.Unmarshal([]byte(meta.OptionsJSON), &stored))
	require.Equal(t, []string{project.ID}, stored.EnabledSkillIDs)

	// The live CLI was started with the old set: the change waits for the next prompt.
	_, oldKey := sm.sessionSkillPlugin(&SessionOptions{WorkingDirectory: &wd})
	s.client = fakeClient(t)
	recordClientSkills(s, oldKey)
	st, err = h.SessionSkills(s.ID)
	require.NoError(t, err)
	require.True(t, st.RestartPending)

	st, err = h.SetSessionAddedSkills(s.ID, nil)
	require.NoError(t, err)
	require.Equal(t, "", reachOf(st)["render-tips"])
	require.Empty(t, s.Options.EnabledSkillIDs)
	_ = personal
}
