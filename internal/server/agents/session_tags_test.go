package agents

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/stretchr/testify/require"
)

// Session listings (REST for iOS, WebSocket for the dashboard) carry each
// session's own tags, loaded in one query; untagged sessions omit the field.
func TestSessionListsCarryTags(t *testing.T) {
	database.ResetInstance()
	db, err := database.Initialize(t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	storage, err := NewSQLiteSessionStorage(db.GetDB())
	require.NoError(t, err)

	tagged, plain := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{tagged, plain} {
		_, err = db.GetDB().Exec(`INSERT INTO agent_sessions (id) VALUES (?)`, id.String())
		require.NoError(t, err)
	}
	stored, err := storage.SetSessionTags(tagged, []string{"auth", " Bug  fix "})
	require.NoError(t, err)
	require.Equal(t, []string{"auth", "Bug fix"}, stored)

	h := &AgentHandler{SessionManager: &SessionManager{Storage: storage}}

	sessions := []Session{{ID: tagged}, {ID: plain}}
	h.AddSubagentCounts(sessions)
	require.Equal(t, []string{"auth", "Bug fix"}, sessions[0].Tags)
	require.Nil(t, sessions[1].Tags)

	raw, err := json.Marshal(sessions)
	require.NoError(t, err)
	var items []map[string]any
	require.NoError(t, json.Unmarshal(raw, &items))
	require.Equal(t, []any{"auth", "Bug fix"}, items[0]["tags"])
	require.NotContains(t, items[1], "tags")

	light := []LightSession{{ID: tagged}, {ID: plain}}
	h.addLightSubagentCounts(light)
	require.Equal(t, []string{"auth", "Bug fix"}, light[0].Tags)
	require.Nil(t, light[1].Tags)

	// Deleting the session deletes its tags.
	require.NoError(t, storage.DeleteSession(tagged))
	var n int
	require.NoError(t, db.GetDB().QueryRow(`SELECT COUNT(*) FROM session_tags`).Scan(&n))
	require.Zero(t, n)
}
