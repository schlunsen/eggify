package agents

import (
	"github.com/google/uuid"
	"github.com/schlunsen/eggify/internal/database"
	"github.com/schlunsen/eggify/internal/logging"
)

// SessionTagStorage reads and writes a session's own tags (session_tags).
// SQLiteSessionStorage implements it; other storages (tests) may not.
type SessionTagStorage interface {
	SessionTags(ids []uuid.UUID) (map[uuid.UUID][]string, error)
	AllSessionTags() (map[uuid.UUID][]string, error)
	SetSessionTags(id uuid.UUID, tags []string) ([]string, error)
}

// SessionTags returns the tags of each given session that has any.
func (s *SQLiteSessionStorage) SessionTags(ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	raw, err := database.LoadSessionTags(s.db, strs)
	if err != nil {
		return nil, err
	}
	return byUUID(raw), nil
}

// AllSessionTags returns the tags of every existing session that has any.
func (s *SQLiteSessionStorage) AllSessionTags() (map[uuid.UUID][]string, error) {
	raw, err := database.LoadAllSessionTags(s.db)
	if err != nil {
		return nil, err
	}
	return byUUID(raw), nil
}

// SetSessionTags stores a session's tags (an empty list removes them) and
// returns the normalised list.
func (s *SQLiteSessionStorage) SetSessionTags(id uuid.UUID, tags []string) ([]string, error) {
	return database.SaveSessionTags(s.db, id.String(), tags)
}

func byUUID(raw map[string][]string) map[uuid.UUID][]string {
	out := make(map[uuid.UUID][]string, len(raw))
	for k, v := range raw {
		if id, err := uuid.Parse(k); err == nil {
			out[id] = v
		}
	}
	return out
}

// sessionTagsFor loads the tags of the given sessions in one query for a
// listing. Failures are logged and read as no tags.
func (sm *SessionManager) sessionTagsFor(ids []uuid.UUID) map[uuid.UUID][]string {
	store, ok := sm.Storage.(SessionTagStorage)
	if !ok || len(ids) == 0 {
		return nil
	}
	tags, err := store.SessionTags(ids)
	if err != nil {
		logging.Debug("Failed to load session tags: %v", err)
		return nil
	}
	return tags
}
