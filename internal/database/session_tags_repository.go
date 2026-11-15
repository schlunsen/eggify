package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits for a session's own tags (the sidebar's "Add tag", ⌘S, iOS).
const (
	MaxSessionTags      = 8
	MaxSessionTagLength = 40
)

// NormalizeSessionTags cleans a session's tags the way the web's splitTags
// does: each value is split on commas, trimmed, inner whitespace collapsed,
// blanks and case-insensitive duplicates dropped. At most MaxSessionTags
// tags of at most MaxSessionTagLength characters are kept. Never nil.
func NormalizeSessionTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, value := range tags {
		for _, part := range strings.Split(value, ",") {
			t := strings.Join(strings.Fields(part), " ")
			if utf8.RuneCountInString(t) > MaxSessionTagLength {
				t = strings.TrimSpace(string([]rune(t)[:MaxSessionTagLength]))
			}
			key := strings.ToLower(t)
			if t == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, t)
			if len(out) == MaxSessionTags {
				return out
			}
		}
	}
	return out
}

// LoadSessionTags returns the tags of each given session that has any.
// Sessions without tags are left out of the map.
func LoadSessionTags(db *sql.DB, sessionIDs []string) (map[string][]string, error) {
	result := make(map[string][]string, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return result, nil
	}
	// SQLite limits bound parameters; query in chunks.
	const chunk = 500
	for start := 0; start < len(sessionIDs); start += chunk {
		end := start + chunk
		if end > len(sessionIDs) {
			end = len(sessionIDs)
		}
		ids := sessionIDs[start:end]
		args := make([]interface{}, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		rows, err := db.Query(`SELECT session_id, tags FROM session_tags WHERE session_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to load session tags: %w", err)
		}
		if err := scanSessionTags(rows, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// LoadAllSessionTags returns the tags of every existing session that has any.
func LoadAllSessionTags(db *sql.DB) (map[string][]string, error) {
	rows, err := db.Query(`SELECT t.session_id, t.tags FROM session_tags t
		JOIN agent_sessions s ON s.id = t.session_id`)
	if err != nil {
		return nil, fmt.Errorf("failed to load session tags: %w", err)
	}
	result := make(map[string][]string)
	if err := scanSessionTags(rows, result); err != nil {
		return nil, err
	}
	return result, nil
}

func scanSessionTags(rows *sql.Rows, into map[string][]string) error {
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("failed to scan session tags: %w", err)
		}
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			continue // a broken row reads as untagged
		}
		if tags = NormalizeSessionTags(tags); len(tags) > 0 {
			into[id] = tags
		}
	}
	return rows.Err()
}

// SaveSessionTags normalises and stores a session's tags and returns what
// was stored. An empty list deletes the row. The session must exist (the
// foreign key refuses tags for unknown sessions).
func SaveSessionTags(db *sql.DB, sessionID string, tags []string) ([]string, error) {
	tags = NormalizeSessionTags(tags)
	if len(tags) == 0 {
		if _, err := db.Exec(`DELETE FROM session_tags WHERE session_id = ?`, sessionID); err != nil {
			return nil, fmt.Errorf("failed to delete session tags: %w", err)
		}
		return tags, nil
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`INSERT INTO session_tags (session_id, tags, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(session_id) DO UPDATE SET tags = excluded.tags, updated_at = excluded.updated_at`,
		sessionID, string(raw))
	if err != nil {
		return nil, fmt.Errorf("failed to save session tags: %w", err)
	}
	return tags, nil
}

// GetSessionTags returns the tags of each given session that has any.
func (r *Repository) GetSessionTags(sessionIDs []string) (map[string][]string, error) {
	return LoadSessionTags(r.db.db, sessionIDs)
}

// GetAllSessionTags returns the tags of every existing session that has any.
func (r *Repository) GetAllSessionTags() (map[string][]string, error) {
	return LoadAllSessionTags(r.db.db)
}

// SetSessionTags stores a session's tags (an empty list removes them) and
// returns the normalised list.
func (r *Repository) SetSessionTags(sessionID string, tags []string) ([]string, error) {
	return SaveSessionTags(r.db.db, sessionID, tags)
}
