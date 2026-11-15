package database

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// skillColumns is the column list every skill SELECT scans, in order.
const skillColumns = `id, name, description, scope, path, frontmatter_json, body,
				user_invocable, disable_model_invocation, allowed_tools,
				model, effort, context, agent, argument_hint, shell,
				created_at, updated_at, created_by, status, reviewed_by, reviewed_at,
				source_hash, folder_hash`

// SkillRepository provides data access methods for skills
type SkillRepository struct {
	db *Database
}

// NewSkillRepository creates a new skill repository instance
func NewSkillRepository(db *Database) *SkillRepository {
	return &SkillRepository{db: db}
}

// scanSkill reads one row selected with skillColumns.
func scanSkill(row rowScanner) (*Skill, error) {
	s := &Skill{}
	var effort, context, shell sql.NullString
	err := row.Scan(
		&s.ID, &s.Name, &s.Description, &s.Scope, &s.Path,
		&s.FrontmatterJSON, &s.Body,
		&s.UserInvocable, &s.DisableModelInvocation, &s.AllowedTools,
		&s.Model, &effort, &context, &s.Agent, &s.ArgumentHint, &shell,
		&s.CreatedAt, &s.UpdatedAt,
		&s.CreatedBy, &s.Status, &s.ReviewedBy, &s.ReviewedAt,
		&s.SourceHash, &s.FolderHash,
	)
	if err != nil {
		return nil, err
	}
	s.Effort = effort.String
	s.Context = context.String
	s.Shell = shell.String
	return s, nil
}

// GetAllSkills retrieves all skills, optionally filtered by scope
func (r *SkillRepository) GetAllSkills(scope string) ([]*Skill, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	var query string
	var args []interface{}

	if scope != "" {
		query = `
			SELECT ` + skillColumns + `
			FROM skills
			WHERE scope = ?
			ORDER BY name ASC
		`
		args = append(args, scope)
	} else {
		query = `
			SELECT ` + skillColumns + `
			FROM skills
			ORDER BY scope ASC, name ASC
		`
	}

	rows, err := r.db.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query skills: %w", err)
	}
	defer rows.Close()

	var skills []*Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan skill: %w", err)
		}
		skills = append(skills, s)
	}

	return skills, rows.Err()
}

// GetSkillByName retrieves a single skill by name
func (r *SkillRepository) GetSkillByName(name string) (*Skill, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	s, err := scanSkill(r.db.db.QueryRow(`SELECT `+skillColumns+` FROM skills WHERE name = ?`, name))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get skill: %w", err)
	}
	return s, nil
}

// GetSkillByID retrieves a single skill by ID
func (r *SkillRepository) GetSkillByID(id string) (*Skill, error) {
	r.db.mu.RLock()
	defer r.db.mu.RUnlock()

	s, err := scanSkill(r.db.db.QueryRow(`SELECT `+skillColumns+` FROM skills WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get skill by ID: %w", err)
	}
	return s, nil
}

// SaveSkill creates or updates a skill in the database
func (r *SkillRepository) SaveSkill(skill *Skill) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	if skill.ID == "" {
		skill.ID = uuid.New().String()
	}

	now := time.Now()
	// A fresh struct (UI, filesystem discovery, packs) is user content. A
	// skill loaded with GetSkillBy* keeps its provenance and review status
	// when it is saved back. Agent-written skills go through CreateAgentSkill.
	if skill.CreatedBy == "" {
		skill.CreatedBy = CreatedByUser
	}
	if skill.Status == "" {
		skill.Status = ReviewStatusApproved
	}

	// Upsert by name
	query := `
		INSERT INTO skills (
			id, name, description, scope, path, frontmatter_json, body,
			user_invocable, disable_model_invocation, allowed_tools,
			model, effort, context, agent, argument_hint, shell,
			created_at, updated_at, created_by, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			scope = excluded.scope,
			path = excluded.path,
			frontmatter_json = excluded.frontmatter_json,
			body = excluded.body,
			user_invocable = excluded.user_invocable,
			disable_model_invocation = excluded.disable_model_invocation,
			allowed_tools = excluded.allowed_tools,
			model = excluded.model,
			effort = excluded.effort,
			context = excluded.context,
			agent = excluded.agent,
			argument_hint = excluded.argument_hint,
			shell = excluded.shell,
			updated_at = excluded.updated_at,
			created_by = excluded.created_by,
			status = excluded.status
	`

	// Convert empty strings to nil for columns with CHECK constraints
	// SQLite CHECK(col IS NULL OR col IN (...)) requires NULL, not empty string
	nilIfEmpty := func(s string) interface{} {
		if s == "" {
			return nil
		}
		return s
	}

	_, err := r.db.db.Exec(query,
		skill.ID, skill.Name, skill.Description, skill.Scope, skill.Path,
		skill.FrontmatterJSON, skill.Body,
		skill.UserInvocable, skill.DisableModelInvocation, skill.AllowedTools,
		skill.Model, nilIfEmpty(skill.Effort), nilIfEmpty(skill.Context),
		skill.Agent, skill.ArgumentHint, nilIfEmpty(skill.Shell),
		now, now, skill.CreatedBy, skill.Status,
	)
	if err != nil {
		return fmt.Errorf("failed to save skill: %w", err)
	}

	return nil
}

// DeleteSkill removes a skill by name
func (r *SkillRepository) DeleteSkill(name string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	result, err := r.db.db.Exec("DELETE FROM skills WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("failed to delete skill: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("skill not found: %s", name)
	}

	return nil
}

// DeleteSkillsByScope removes all skills for a given scope
func (r *SkillRepository) DeleteSkillsByScope(scope string) error {
	r.db.mu.Lock()
	defer r.db.mu.Unlock()

	_, err := r.db.db.Exec("DELETE FROM skills WHERE scope = ?", scope)
	if err != nil {
		return fmt.Errorf("failed to delete skills by scope: %w", err)
	}

	return nil
}
