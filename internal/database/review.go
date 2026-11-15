package database

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Review statuses for memories and skills (migration 50). Text an agent
// writes for future sessions starts pending and is never put into a prompt
// until a user approves it, so a prompt injection cannot persist silently.
const (
	ReviewStatusPending  = "pending"
	ReviewStatusApproved = "approved"
	ReviewStatusRejected = "rejected"

	// CreatedByUser marks content a user wrote (or that predates provenance).
	CreatedByUser = "user"
	// CreatedByAPIKey marks content written over REST with only the API key,
	// i.e. without a logged-in user. Agents can read the key, so it is
	// treated like agent content and needs review.
	CreatedByAPIKey = "api_key"
	// createdByAgentPrefix starts CreatedBy for agent content: "agent:<session_id>".
	createdByAgentPrefix = "agent:"

	// MaxPendingAgentMemoryImportance caps the importance an agent can give a
	// memory. A reviewer can raise it when approving.
	MaxPendingAgentMemoryImportance = 5
	// MaxPendingMemoriesPerProject stops an agent flooding the review queue.
	MaxPendingMemoriesPerProject = 50
)

// ErrSkillExists is returned by CreateAgentSkill when the name is taken. An
// agent may never overwrite an existing skill.
var ErrSkillExists = errors.New("a skill with this name already exists")

// ErrNotPending is returned when a review targets an item that is not pending.
var ErrNotPending = errors.New("item is not pending review")

// CreatedByAgent returns the CreatedBy value for content an agent session wrote.
func CreatedByAgent(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "unknown"
	}
	return createdByAgentPrefix + sessionID
}

// IsAgentAuthored reports whether createdBy names an agent (or the API key,
// which agents can read) rather than a user.
func IsAgentAuthored(createdBy string) bool {
	return strings.HasPrefix(createdBy, createdByAgentPrefix) || createdBy == CreatedByAPIKey
}

// AgentSessionOf returns the session id in an "agent:<id>" CreatedBy value.
func AgentSessionOf(createdBy string) string {
	if !strings.HasPrefix(createdBy, createdByAgentPrefix) {
		return ""
	}
	id := strings.TrimPrefix(createdBy, createdByAgentPrefix)
	if id == "unknown" {
		return ""
	}
	return id
}

// ClampAgentImportance limits the importance an agent picks for a memory.
func ClampAgentImportance(importance int) int {
	if importance < 1 {
		return 1
	}
	if importance > MaxPendingAgentMemoryImportance {
		return MaxPendingAgentMemoryImportance
	}
	return importance
}

// MemoryReview carries a reviewer's decision and optional edits.
type MemoryReview struct {
	Status     string // approved or rejected
	ReviewedBy string
	Title      *string
	Content    *string
	MemoryType *string
	Tags       *string
	Importance *int
}

// ListPendingMemories returns memories awaiting review, newest first. An
// empty projectID lists every project.
func (r *MemoryRepository) ListPendingMemories(projectID string, limit int) ([]*Memory, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT ` + memoryColumns + ` FROM memories WHERE status = 'pending' AND is_archived = 0`
	args := []interface{}{}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending memories: %w", err)
	}
	defer rows.Close()
	return scanMemoryRows(rows)
}

// CountPendingMemories counts memories awaiting review, per project.
func (r *MemoryRepository) CountPendingMemories() (map[string]int, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	rows, err := r.db.db.Query(`SELECT project_id, COUNT(*) FROM memories
		WHERE status = 'pending' AND is_archived = 0 GROUP BY project_id`)
	if err != nil {
		return nil, fmt.Errorf("failed to count pending memories: %w", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var projectID string
		var n int
		if err := rows.Scan(&projectID, &n); err != nil {
			return nil, err
		}
		counts[projectID] = n
	}
	return counts, rows.Err()
}

// CountPendingMemoriesForProject counts one project's memories awaiting review.
func (r *MemoryRepository) CountPendingMemoriesForProject(projectID string) (int, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	var n int
	err := r.db.db.QueryRow(`SELECT COUNT(*) FROM memories
		WHERE project_id = ? AND status = 'pending' AND is_archived = 0`, projectID).Scan(&n)
	return n, err
}

// ReviewMemory approves or rejects a pending memory, applying any edits.
func (r *MemoryRepository) ReviewMemory(id string, review MemoryReview) (*Memory, error) {
	if review.Status != ReviewStatusApproved && review.Status != ReviewStatusRejected {
		return nil, fmt.Errorf("invalid review status %q", review.Status)
	}
	m, err := r.GetMemory(id)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, sql.ErrNoRows
	}
	if m.Status != ReviewStatusPending {
		return nil, ErrNotPending
	}
	if review.Title != nil && *review.Title != "" {
		m.Title = *review.Title
	}
	if review.Content != nil && *review.Content != "" {
		m.Content = *review.Content
	}
	if review.MemoryType != nil && *review.MemoryType != "" {
		m.MemoryType = *review.MemoryType
	}
	if review.Tags != nil {
		m.Tags = *review.Tags
	}
	if review.Importance != nil && *review.Importance >= 1 && *review.Importance <= 10 {
		m.Importance = *review.Importance
	}

	now := time.Now()
	reviewer := review.ReviewedBy
	m.Status = review.Status
	m.ReviewedBy = &reviewer
	m.ReviewedAt = &now
	m.UpdatedAt = now

	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	res, err := r.db.db.Exec(`UPDATE memories SET
			title = ?, content = ?, memory_type = ?, tags = ?, importance = ?,
			status = ?, reviewed_by = ?, reviewed_at = ?, updated_at = ?
		WHERE id = ? AND status = 'pending'`,
		m.Title, m.Content, m.MemoryType, m.Tags, m.Importance,
		m.Status, reviewer, now, now, id)
	if err != nil {
		return nil, fmt.Errorf("failed to review memory: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotPending
	}
	return m, nil
}

// CreateAgentSkill stores a skill an agent wrote. It is pending review and
// never replaces an existing skill (SaveSkill would upsert by name, letting
// an agent rewrite a skill the user trusts).
func (r *SkillRepository) CreateAgentSkill(skill *Skill, sessionID string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	var exists int
	if err := r.db.db.QueryRow(`SELECT COUNT(*) FROM skills WHERE name = ?`, skill.Name).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check skill name: %w", err)
	}
	if exists > 0 {
		return ErrSkillExists
	}
	if skill.ID == "" {
		skill.ID = uuid.New().String()
	}
	now := time.Now()
	skill.CreatedAt = now
	skill.UpdatedAt = now
	skill.CreatedBy = CreatedByAgent(sessionID)
	skill.Status = ReviewStatusPending

	_, err := r.db.db.Exec(`INSERT INTO skills (
			id, name, description, scope, path, frontmatter_json, body,
			user_invocable, disable_model_invocation, allowed_tools,
			model, agent, argument_hint, created_at, updated_at, created_by, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		skill.ID, skill.Name, skill.Description, skill.Scope, skill.Path,
		skill.FrontmatterJSON, skill.Body,
		skill.UserInvocable, skill.DisableModelInvocation, skill.AllowedTools,
		skill.Model, skill.Agent, skill.ArgumentHint, now, now, skill.CreatedBy, skill.Status)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrSkillExists
		}
		return fmt.Errorf("failed to create skill: %w", err)
	}
	return nil
}

// ListPendingSkills returns skills awaiting review, newest first.
func (r *SkillRepository) ListPendingSkills() ([]*Skill, error) {
	all, err := r.GetAllSkills("")
	if err != nil {
		return nil, err
	}
	pending := make([]*Skill, 0)
	for _, s := range all {
		if s.Status == ReviewStatusPending {
			pending = append(pending, s)
		}
	}
	sort.SliceStable(pending, func(i, j int) bool {
		return pending[i].CreatedAt.After(pending[j].CreatedAt)
	})
	return pending, nil
}

// CountPendingSkills counts skills awaiting review.
func (r *SkillRepository) CountPendingSkills() (int, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()
	var n int
	err := r.db.db.QueryRow(`SELECT COUNT(*) FROM skills WHERE status = 'pending'`).Scan(&n)
	return n, err
}

// SkillReview carries a reviewer's decision and optional edits.
type SkillReview struct {
	Status      string // approved or rejected
	ReviewedBy  string
	Description *string
	Body        *string
}

// ReviewSkill approves or rejects a pending skill, applying any edits.
func (r *SkillRepository) ReviewSkill(id string, review SkillReview) (*Skill, error) {
	if review.Status != ReviewStatusApproved && review.Status != ReviewStatusRejected {
		return nil, fmt.Errorf("invalid review status %q", review.Status)
	}
	s, err := r.GetSkillByID(id)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, sql.ErrNoRows
	}
	if s.Status != ReviewStatusPending {
		return nil, ErrNotPending
	}
	if review.Description != nil && *review.Description != "" {
		s.Description = *review.Description
	}
	if review.Body != nil && *review.Body != "" {
		s.Body = *review.Body
	}
	now := time.Now()
	reviewer := review.ReviewedBy
	s.Status = review.Status
	s.ReviewedBy = &reviewer
	s.ReviewedAt = &now
	s.UpdatedAt = now

	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	res, err := r.db.db.Exec(`UPDATE skills SET
			description = ?, body = ?, status = ?, reviewed_by = ?, reviewed_at = ?, updated_at = ?
		WHERE id = ? AND status = 'pending'`,
		s.Description, s.Body, s.Status, reviewer, now, now, id)
	if err != nil {
		return nil, fmt.Errorf("failed to review skill: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotPending
	}
	return s, nil
}
