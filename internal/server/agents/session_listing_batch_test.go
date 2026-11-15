package agents

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestListAllSessionsJoinsAvatarsAndFiltersInSQL(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()

	res, err := sm.db.Exec(`INSERT INTO avatar_themes (name) VALUES ('listing-test')`)
	require.NoError(t, err)
	themeID, err := res.LastInsertId()
	require.NoError(t, err)
	res, err = sm.db.Exec(`INSERT INTO avatars (theme_id, name, type, image_path, color, seed)
		VALUES (?, 'Robo', 'preset', '/a/robo.png', '#fff', 'robo-seed')`, themeID)
	require.NoError(t, err)
	avatarID, err := res.LastInsertId()
	require.NoError(t, err)

	withAvatar := seedSwitchSession(t, sm, "claude", "sonnet", func(s *Session) {
		s.SelectedAvatarID = &avatarID
	})
	ended := seedSwitchSession(t, sm, "claude", "sonnet", func(s *Session) {
		s.Status = SessionStatusEnded
	})
	// Not running in this process: the stored status is the only one.
	sm.mu.Lock()
	delete(sm.sessions, ended.ID)
	sm.mu.Unlock()

	all, err := sm.ListAllSessions("all")
	require.NoError(t, err)
	require.Len(t, all, 2)
	var found bool
	for _, s := range all {
		if s.ID == withAvatar.ID {
			found = true
			require.NotNil(t, s.SelectedAvatar)
			require.Equal(t, avatarID, s.SelectedAvatar.ID)
			require.Equal(t, "Robo", s.SelectedAvatar.Name)
			require.Equal(t, "/a/robo.png", s.SelectedAvatar.ImagePath)
			require.Equal(t, "robo-seed", s.SelectedAvatar.Seed)
		} else {
			require.Nil(t, s.SelectedAvatar)
		}
	}
	require.True(t, found)

	active, err := sm.ListAllSessions("active")
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, withAvatar.ID, active[0].ID)

	endedOnly, err := sm.ListAllSessions("ended")
	require.NoError(t, err)
	require.Len(t, endedOnly, 1)
	require.Equal(t, ended.ID, endedOnly[0].ID)

	// A stale stored status is overridden by the live one in both directions.
	sm.mu.Lock()
	withAvatar.Status = SessionStatusError
	sm.mu.Unlock()
	errored, err := sm.ListAllSessions("error")
	require.NoError(t, err)
	require.Len(t, errored, 1)
	require.Equal(t, withAvatar.ID, errored[0].ID)
	idle, err := sm.ListAllSessions("idle")
	require.NoError(t, err)
	require.Empty(t, idle)
}

func TestSessionOwnersLoadsManyInOneCall(t *testing.T) {
	sm, cleanup := newModelSwitchTestManager(t)
	defer cleanup()
	alice := "alice"
	_, err := sm.db.Exec(`INSERT INTO users (username) VALUES (?)`, alice)
	require.NoError(t, err)
	owned := seedSwitchSession(t, sm, "claude", "sonnet", func(s *Session) { s.OwnerUserID = &alice })
	shared := seedSwitchSession(t, sm, "claude", "sonnet", nil)
	missing := uuid.New()

	storage, ok := sm.Storage.(*SQLiteSessionStorage)
	require.True(t, ok)
	owners, err := storage.SessionOwners([]uuid.UUID{owned.ID, shared.ID, missing})
	require.NoError(t, err)
	require.Len(t, owners, 2)
	require.NotNil(t, owners[owned.ID])
	require.Equal(t, "alice", *owners[owned.ID])
	sharedOwner, exists := owners[shared.ID]
	require.True(t, exists)
	require.Nil(t, sharedOwner)
	_, exists = owners[missing]
	require.False(t, exists)

	empty, err := storage.SessionOwners(nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestDigestsLoadsSeveralSessionsAtOnce(t *testing.T) {
	s, _, session := newDigestHarness(t)
	other := uuid.New()
	require.NoError(t, s.save(session.ID, "first", []string{"a"}, "3:idle"))
	require.NoError(t, s.save(other, "second", []string{"b"}, "5:idle"))

	got := s.Digests([]DigestRequest{
		{ID: session.ID.String(), Version: "3:idle"},
		{ID: other.String(), Version: "5:idle"},
		{ID: uuid.New().String(), Version: "1:idle"},
		{ID: "not-a-uuid"},
	}, false)
	require.Len(t, got, 2)
	require.Equal(t, "first", got[session.ID.String()].Summary)
	require.Equal(t, []string{"b"}, got[other.String()].Tags)
	require.Equal(t, "5:idle", got[other.String()].Version)
}
