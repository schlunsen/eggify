package database

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/schlunsen/eggify/internal/workspacetrust"
)

//go:embed providers_seed.json
var providersJSONData []byte

//go:embed connectors_seed.json
var connectorsSeedData []byte

// Migration represents a database migration
type Migration struct {
	Version     int
	Description string
	Up          func(*sql.Tx) error
}

// migrations is the list of all database migrations
// Add new migrations to the end of this slice
var migrations = []Migration{
	{
		Version:     1,
		Description: "Add avatar_id column to users table",
		Up: func(tx *sql.Tx) error {
			// Check if column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0
				FROM pragma_table_info('users')
				WHERE name='avatar_id'
			`).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if avatar_id column exists: %w", err)
			}

			if exists {
				return nil // Column already exists, skip
			}

			// Add avatar_id column
			_, err = tx.Exec(`
				ALTER TABLE users
				ADD COLUMN avatar_id INTEGER REFERENCES avatars(id) ON DELETE SET NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to add avatar_id column: %w", err)
			}

			// Create index for avatar_id
			_, err = tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_users_avatar_id
				ON users(avatar_id) WHERE avatar_id IS NOT NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to create index on avatar_id: %w", err)
			}

			return nil
		},
	},
	// Add future migrations here
	// Example:
	// {
	// 	Version:     2,
	// 	Description: "Add new column to some table",
	// 	Up: func(db *sql.DB) error {
	// 		// Migration code here
	// 		return nil
	// 	},
	// },
	{
		Version:     2,
		Description: "Add user_id column to agent_messages table for multi-user support",
		Up: func(tx *sql.Tx) error {
			// Check if column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0
				FROM pragma_table_info('agent_messages')
				WHERE name='user_id'
			`).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if user_id column exists: %w", err)
			}

			if exists {
				return nil // Column already exists, skip
			}

			// Add user_id column (nullable, for assistant/system messages without user attribution)
			_, err = tx.Exec(`
				ALTER TABLE agent_messages
				ADD COLUMN user_id TEXT REFERENCES users(username) ON DELETE SET NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to add user_id column: %w", err)
			}

			// Create index for efficient user-specific message queries
			_, err = tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_agent_messages_user
				ON agent_messages(session_id, user_id) WHERE user_id IS NOT NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to create index on user_id: %w", err)
			}

			return nil
		},
	},
	{
		Version:     3,
		Description: "Add owner_user_id to agent_sessions for multi-user session ownership tracking",
		Up: func(tx *sql.Tx) error {
			// Check if column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0
				FROM pragma_table_info('agent_sessions')
				WHERE name='owner_user_id'
			`).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if owner_user_id column exists: %w", err)
			}

			if exists {
				return nil // Column already exists, skip
			}

			// Add owner_user_id column (nullable for backward compatibility)
			_, err = tx.Exec(`
				ALTER TABLE agent_sessions
				ADD COLUMN owner_user_id TEXT REFERENCES users(username) ON DELETE SET NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to add owner_user_id column: %w", err)
			}

			// Create index for efficient owner-specific session queries
			_, err = tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_agent_sessions_owner
				ON agent_sessions(owner_user_id, created_at DESC) WHERE owner_user_id IS NOT NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to create index on owner_user_id: %w", err)
			}

			return nil
		},
	},
	{
		Version:     7,
		Description: "Add UUID id column to users table for proper user identification",
		Up: func(tx *sql.Tx) error {
			// Check if id column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0
				FROM pragma_table_info('users')
				WHERE name='id'
			`).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if id column exists: %w", err)
			}

			if exists {
				return nil // Column already exists, skip
			}

			// Add id column (UUID) - nullable initially, will be populated below
			_, err = tx.Exec(`
				ALTER TABLE users
				ADD COLUMN id TEXT
			`)
			if err != nil {
				return fmt.Errorf("failed to add id column: %w", err)
			}

			// Populate existing users with UUIDs (using a deterministic UUID based on username)
			// This ensures consistent UUIDs across migrations
			_, err = tx.Exec(`
				UPDATE users
				SET id = lower(
					printf('%08x-%04x-%04x-%04x-%012x',
						abs(random()) % 4294967296,
						abs(random()) % 65536,
						(abs(random()) % 65536) | 0x4000,
						(abs(random()) % 65536) | 0x8000,
						abs(random()) % 281474976710656
					)
				)
				WHERE id IS NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to populate id column with UUIDs: %w", err)
			}

			// Add UNIQUE constraint to id column
			_, err = tx.Exec(`
				CREATE UNIQUE INDEX IF NOT EXISTS idx_users_id
				ON users(id)
			`)
			if err != nil {
				return fmt.Errorf("failed to create unique index on id: %w", err)
			}

			return nil
		},
	},
	{
		Version:     5,
		Description: "Add user_uuid column to agent_messages for UUID-based user references",
		Up: func(tx *sql.Tx) error {
			// Check if user_uuid column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0
				FROM pragma_table_info('agent_messages')
				WHERE name='user_uuid'
			`).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if user_uuid column exists: %w", err)
			}

			if exists {
				return nil // Column already exists, skip
			}

			// Add user_uuid column
			_, err = tx.Exec(`
				ALTER TABLE agent_messages
				ADD COLUMN user_uuid TEXT REFERENCES users(id) ON DELETE SET NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to add user_uuid column: %w", err)
			}

			// Populate user_uuid from user_id (username) by joining with users table
			_, err = tx.Exec(`
				UPDATE agent_messages
				SET user_uuid = (
					SELECT u.id FROM users u WHERE u.username = agent_messages.user_id
				)
				WHERE user_id IS NOT NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to populate user_uuid from user_id: %w", err)
			}

			// Create index for efficient queries
			_, err = tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_agent_messages_user_uuid
				ON agent_messages(session_id, user_uuid) WHERE user_uuid IS NOT NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to create index on user_uuid: %w", err)
			}

			return nil
		},
	},
	{
		Version:     6,
		Description: "Add owner_uuid column to agent_sessions for UUID-based owner references",
		Up: func(tx *sql.Tx) error {
			// Check if owner_uuid column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0
				FROM pragma_table_info('agent_sessions')
				WHERE name='owner_uuid'
			`).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if owner_uuid column exists: %w", err)
			}

			if exists {
				return nil // Column already exists, skip
			}

			// Add owner_uuid column
			_, err = tx.Exec(`
				ALTER TABLE agent_sessions
				ADD COLUMN owner_uuid TEXT REFERENCES users(id) ON DELETE SET NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to add owner_uuid column: %w", err)
			}

			// Populate owner_uuid from owner_user_id (username) by joining with users table
			_, err = tx.Exec(`
				UPDATE agent_sessions
				SET owner_uuid = (
					SELECT u.id FROM users u WHERE u.username = agent_sessions.owner_user_id
				)
				WHERE owner_user_id IS NOT NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to populate owner_uuid from owner_user_id: %w", err)
			}

			// Create index for efficient owner-specific session queries
			_, err = tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_agent_sessions_owner_uuid
				ON agent_sessions(owner_uuid, created_at DESC) WHERE owner_uuid IS NOT NULL
			`)
			if err != nil {
				return fmt.Errorf("failed to create index on owner_uuid: %w", err)
			}

			return nil
		},
	},
	{
		Version:     8,
		Description: "Add provider metadata columns and seed from providers.json",
		Up: func(tx *sql.Tx) error {
			// Try to add metadata columns if they don't exist (they may have been created by schema.sql)
			alterStatements := []string{
				"ALTER TABLE providers ADD COLUMN name TEXT",
				"ALTER TABLE providers ADD COLUMN icon TEXT",
				"ALTER TABLE providers ADD COLUMN base_url TEXT",
				"ALTER TABLE providers ADD COLUMN models TEXT",
				"ALTER TABLE providers ADD COLUMN default_model TEXT",
				"ALTER TABLE providers ADD COLUMN description TEXT",
				"ALTER TABLE providers ADD COLUMN enabled BOOLEAN DEFAULT 1",
				"ALTER TABLE providers ADD COLUMN is_configured BOOLEAN DEFAULT 0",
			}

			for _, stmt := range alterStatements {
				if _, err := tx.Exec(stmt); err != nil {
					// Column may already exist, which is OK - schema.sql may have created it
				}
			}

			// Update is_configured flag for existing provider configurations
			_, err := tx.Exec(`
				UPDATE providers
				SET is_configured = 1
				WHERE api_key IS NOT NULL AND api_key != ''
			`)
			if err != nil {
				return fmt.Errorf("failed to update is_configured flag: %w", err)
			}

			// Check if providers table already has data
			var providerCount int
			err = tx.QueryRow("SELECT COUNT(*) FROM providers").Scan(&providerCount)
			if err != nil {
				return fmt.Errorf("failed to count providers: %w", err)
			}

			// Only seed if table is empty
			if providerCount > 0 {
				return nil
			}

			// Parse providers.json to seed provider metadata
			var providersConfig struct {
				Providers []struct {
					ID           string   `json:"id"`
					Name         string   `json:"name"`
					Icon         string   `json:"icon"`
					BaseURL      string   `json:"base_url"`
					Models       []string `json:"models"`
					DefaultModel string   `json:"default_model"`
					Description  string   `json:"description"`
				} `json:"providers"`
			}

			if err := json.Unmarshal(providersJSONData, &providersConfig); err != nil {
				return fmt.Errorf("failed to parse providers.json: %w", err)
			}

			// Seed provider metadata from providers.json
			for _, provider := range providersConfig.Providers {
				// Convert models array to JSON string
				modelsJSON, err := json.Marshal(provider.Models)
				if err != nil {
					return fmt.Errorf("failed to marshal models for provider %s: %w", provider.ID, err)
				}

				// Insert or update provider metadata
				_, err = tx.Exec(`
					INSERT INTO providers (provider_id, name, icon, base_url, models, default_model, description, enabled, is_configured)
					VALUES (?, ?, ?, ?, ?, ?, ?, 1, 0)
					ON CONFLICT(provider_id) DO UPDATE SET
						name = excluded.name,
						icon = excluded.icon,
						base_url = excluded.base_url,
						models = excluded.models,
						default_model = excluded.default_model,
						description = excluded.description,
						enabled = excluded.enabled,
						updated_at = CURRENT_TIMESTAMP
				`, provider.ID, provider.Name, provider.Icon, provider.BaseURL, string(modelsJSON), provider.DefaultModel, provider.Description)

				if err != nil {
					return fmt.Errorf("failed to seed provider %s: %w", provider.ID, err)
				}
			}

			return nil
		},
	},
	{
		Version:     9,
		Description: "Update DeepSeek models to include V3.2-Speciale",
		Up: func(tx *sql.Tx) error {
			// Update DeepSeek provider's models list to include V3.2-Speciale and reorder
			newModels := []string{
				"deepseek-chat",
				"deepseek-reasoner",
				"DeepSeek-V3.2-Speciale",
				"DeepSeek-V3.2-Exp",
				"DeepSeek-V3.1",
				"DeepSeek-V3.1-Terminus",
				"DeepSeek-V3-Base",
				"DeepSeek-V3",
				"DeepSeek-R1",
				"DeepSeek-R1-Zero",
				"DeepSeek-R1-Lite",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal DeepSeek models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'deepseek'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update DeepSeek models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     10,
		Description: "Add Kimi K2.5 model with vision support",
		Up: func(tx *sql.Tx) error {
			// Update Kimi provider's models list to include K2.5
			newModels := []string{
				"kimi-k2.5",
				"kimi-k2",
				"kimi-k2-thinking",
				"kimi-k2-0905",
				"kimi-k2-turbo-preview",
				"moonshot-v1-128k",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal Kimi models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'kimi'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update Kimi models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     11,
		Description: "Create project_areas table",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS project_areas (
					id TEXT PRIMARY KEY,
					project_id TEXT NOT NULL,
					name TEXT NOT NULL,
					relative_path TEXT NOT NULL,
					icon TEXT,
					color TEXT,
					description TEXT,
					context_prompt TEXT,
					include_patterns TEXT,
					exclude_patterns TEXT,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create project_areas table: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_project_areas_project ON project_areas(project_id, created_at DESC)`)
			if err != nil {
				return fmt.Errorf("failed to create project_areas index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_project_areas_name ON project_areas(project_id, name)`)
			if err != nil {
				return fmt.Errorf("failed to create project_areas name index: %w", err)
			}

			return nil
		},
	},
	{
		Version:     13,
		Description: "Create worktrees table for git worktree management",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS worktrees (
					id TEXT PRIMARY KEY,
					project_id TEXT NOT NULL,
					session_id TEXT,
					worktree_path TEXT NOT NULL UNIQUE,
					branch_name TEXT NOT NULL,
					source_branch TEXT,
					is_auto_created BOOLEAN DEFAULT 0,
					auto_cleanup BOOLEAN DEFAULT 1,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					removed_at TIMESTAMP,
					FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create worktrees table: %w", err)
			}

			// Index for looking up worktrees by project
			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_worktrees_project ON worktrees(project_id, removed_at)`)
			if err != nil {
				return fmt.Errorf("failed to create worktrees project index: %w", err)
			}

			// Index for looking up worktrees by session
			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_worktrees_session ON worktrees(session_id) WHERE session_id IS NOT NULL`)
			if err != nil {
				return fmt.Errorf("failed to create worktrees session index: %w", err)
			}

			// Index for finding orphaned worktrees during cleanup
			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_worktrees_cleanup ON worktrees(auto_cleanup, removed_at, created_at) WHERE removed_at IS NULL`)
			if err != nil {
				return fmt.Errorf("failed to create worktrees cleanup index: %w", err)
			}

			return nil
		},
	},
	{
		Version:     14,
		Description: "Create background_agents table for persisting subagent state",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS background_agents (
					agent_id TEXT PRIMARY KEY,
					parent_session_id TEXT NOT NULL,
					subagent_type TEXT NOT NULL DEFAULT '',
					description TEXT NOT NULL DEFAULT '',
					status TEXT NOT NULL DEFAULT 'running',
					progress REAL NOT NULL DEFAULT 0.0,
					last_output TEXT,
					output_lines INTEGER NOT NULL DEFAULT 0,
					error_message TEXT,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					completed_at TIMESTAMP,
					FOREIGN KEY (parent_session_id) REFERENCES agent_sessions(id) ON DELETE CASCADE,
					CONSTRAINT bg_agent_status_check CHECK (status IN ('running', 'completed', 'failed', 'waiting'))
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create background_agents table: %w", err)
			}

			_, err = tx.Exec(`
				CREATE INDEX IF NOT EXISTS idx_background_agents_session
				ON background_agents(parent_session_id, status)
			`)
			if err != nil {
				return fmt.Errorf("failed to create index on background_agents: %w", err)
			}

			return nil
		},
	},
	{
		Version:     12,
		Description: "Fix agent message sequences (one-time)",
		Up: func(tx *sql.Tx) error {
			// Check if agent_messages table has any rows
			var count int
			err := tx.QueryRow("SELECT COUNT(*) FROM agent_messages").Scan(&count)
			if err != nil {
				// Table might not exist yet, skip
				return nil
			}
			if count == 0 {
				return nil
			}

			// Create temp table with correct sequences
			_, err = tx.Exec(`
				CREATE TEMP TABLE IF NOT EXISTS temp_sequences AS
				SELECT
					id,
					ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY timestamp ASC, sequence ASC) as new_sequence
				FROM agent_messages
			`)
			if err != nil {
				return fmt.Errorf("failed to create temp sequences table: %w", err)
			}

			// Update all messages with correct sequence numbers
			_, err = tx.Exec(`
				UPDATE agent_messages
				SET sequence = (
					SELECT new_sequence
					FROM temp_sequences
					WHERE temp_sequences.id = agent_messages.id
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to update message sequences: %w", err)
			}

			// Update session message counts
			_, err = tx.Exec(`
				UPDATE agent_sessions
				SET message_count = (
					SELECT MAX(sequence)
					FROM agent_messages
					WHERE agent_messages.session_id = agent_sessions.id
				)
				WHERE EXISTS (
					SELECT 1
					FROM agent_messages
					WHERE agent_messages.session_id = agent_sessions.id
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to update session message counts: %w", err)
			}

			// Clean up
			_, err = tx.Exec("DROP TABLE IF EXISTS temp_sequences")
			if err != nil {
				return fmt.Errorf("failed to drop temp sequences table: %w", err)
			}

			return nil
		},
	},
	{
		Version:     15,
		Description: "Add color column to projects table",
		Up: func(tx *sql.Tx) error {
			// Check if column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0 FROM pragma_table_info('projects') WHERE name = 'color'
			`).Scan(&exists)
			if err != nil || exists {
				return nil
			}

			_, err = tx.Exec(`ALTER TABLE projects ADD COLUMN color TEXT DEFAULT ''`)
			if err != nil {
				return fmt.Errorf("failed to add color column to projects: %w", err)
			}

			return nil
		},
	},
	{
		Version:     16,
		Description: "Add view_mode column to agent_sessions table",
		Up: func(tx *sql.Tx) error {
			// Check if column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0
				FROM pragma_table_info('agent_sessions')
				WHERE name='view_mode'
			`).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check if view_mode column exists: %w", err)
			}

			if exists {
				return nil // Column already exists, skip
			}

			_, err = tx.Exec(`
				ALTER TABLE agent_sessions
				ADD COLUMN view_mode TEXT DEFAULT 'live'
			`)
			if err != nil {
				return fmt.Errorf("failed to add view_mode column: %w", err)
			}

			return nil
		},
	},
	{
		Version:     17,
		Description: "Create connector_definitions and user_connections tables",
		Up: func(tx *sql.Tx) error {
			// Create connector definitions table
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS connector_definitions (
					slug TEXT PRIMARY KEY,
					name TEXT NOT NULL,
					description TEXT,
					category TEXT NOT NULL,
					auth_type TEXT NOT NULL DEFAULT 'api_key',
					icon TEXT,
					bg_class TEXT,
					sort_order INTEGER DEFAULT 0,
					is_active BOOLEAN DEFAULT 1,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					CONSTRAINT category_check CHECK (category IN ('ai_models', 'productivity', 'communication', 'tools')),
					CONSTRAINT auth_type_check CHECK (auth_type IN ('api_key', 'oauth2', 'webhook'))
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create connector_definitions table: %w", err)
			}

			// Create user connections table
			_, err = tx.Exec(`
				CREATE TABLE IF NOT EXISTS user_connections (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					connector_slug TEXT NOT NULL,
					api_key TEXT,
					extra_config TEXT,
					status TEXT NOT NULL DEFAULT 'active',
					status_message TEXT,
					external_account_id TEXT,
					external_account_name TEXT,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					last_used_at TIMESTAMP,
					FOREIGN KEY (connector_slug) REFERENCES connector_definitions(slug) ON DELETE CASCADE,
					CONSTRAINT status_check CHECK (status IN ('active', 'error', 'revoked')),
					CONSTRAINT unique_connector UNIQUE (connector_slug)
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create user_connections table: %w", err)
			}

			// Create indexes
			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_user_connections_slug ON user_connections(connector_slug)`)
			if err != nil {
				return fmt.Errorf("failed to create connector slug index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_user_connections_status ON user_connections(status)`)
			if err != nil {
				return fmt.Errorf("failed to create connection status index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_connector_definitions_category ON connector_definitions(category, sort_order)`)
			if err != nil {
				return fmt.Errorf("failed to create connector category index: %w", err)
			}

			// Seed connector definitions from embedded JSON
			var seedData struct {
				Connectors []struct {
					Slug        string `json:"slug"`
					Name        string `json:"name"`
					Description string `json:"description"`
					Category    string `json:"category"`
					AuthType    string `json:"auth_type"`
					Icon        string `json:"icon"`
					BgClass     string `json:"bg_class"`
					SortOrder   int    `json:"sort_order"`
				} `json:"connectors"`
			}

			if err := json.Unmarshal(connectorsSeedData, &seedData); err != nil {
				return fmt.Errorf("failed to parse connectors_seed.json: %w", err)
			}

			for _, c := range seedData.Connectors {
				_, err = tx.Exec(`
					INSERT OR IGNORE INTO connector_definitions (slug, name, description, category, auth_type, icon, bg_class, sort_order, is_active)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)
				`, c.Slug, c.Name, c.Description, c.Category, c.AuthType, c.Icon, c.BgClass, c.SortOrder)
				if err != nil {
					return fmt.Errorf("failed to seed connector %s: %w", c.Slug, err)
				}
			}

			return nil
		},
	},
	{
		Version:     18,
		Description: "Add Hugging Face connector",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				INSERT OR IGNORE INTO connector_definitions (slug, name, description, category, auth_type, icon, bg_class, sort_order, is_active)
				VALUES ('huggingface', 'Hugging Face', 'Hugging Face API for models, datasets, and inference', 'ai_models', 'api_key', 'smile', 'bg-yellow', 7, 1)
			`)
			if err != nil {
				return fmt.Errorf("failed to insert huggingface connector: %w", err)
			}
			return nil
		},
	},
	{
		Version:     19,
		Description: "Create session_connectors junction table for hot-pluggable connector access",
		Up: func(tx *sql.Tx) error {
			// Junction table linking agent sessions to connectors
			// Connectors can be toggled on/off during a live session
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS session_connectors (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					session_id TEXT NOT NULL,
					connector_slug TEXT NOT NULL,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (session_id) REFERENCES agent_sessions(id) ON DELETE CASCADE,
					FOREIGN KEY (connector_slug) REFERENCES connector_definitions(slug) ON DELETE CASCADE,
					UNIQUE(session_id, connector_slug)
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create session_connectors table: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_connectors_session ON session_connectors(session_id)`)
			if err != nil {
				return fmt.Errorf("failed to create session_connectors session index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_connectors_slug ON session_connectors(connector_slug)`)
			if err != nil {
				return fmt.Errorf("failed to create session_connectors slug index: %w", err)
			}

			return nil
		},
	},
	{
		Version:     20,
		Description: "Create skills and hook_executions tables for skills & hooks system",
		Up: func(tx *sql.Tx) error {
			// Create skills metadata table (source of truth remains SKILL.md files)
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS skills (
					id TEXT PRIMARY KEY,
					name TEXT NOT NULL UNIQUE,
					description TEXT,
					scope TEXT NOT NULL,
					path TEXT NOT NULL,
					frontmatter_json TEXT,
					body TEXT,
					user_invocable BOOLEAN DEFAULT 1,
					disable_model_invocation BOOLEAN DEFAULT 0,
					allowed_tools TEXT,
					model TEXT,
					effort TEXT,
					context TEXT,
					agent TEXT,
					argument_hint TEXT,
					shell TEXT,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					CONSTRAINT scope_check CHECK (scope IN ('personal', 'project', 'plugin')),
					CONSTRAINT effort_check CHECK (effort IS NULL OR effort IN ('low', 'medium', 'high', 'max')),
					CONSTRAINT shell_check CHECK (shell IS NULL OR shell IN ('bash', 'powershell'))
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create skills table: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_skills_scope ON skills(scope)`)
			if err != nil {
				return fmt.Errorf("failed to create skills scope index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_skills_name ON skills(name)`)
			if err != nil {
				return fmt.Errorf("failed to create skills name index: %w", err)
			}

			// Create hook execution audit trail table
			_, err = tx.Exec(`
				CREATE TABLE IF NOT EXISTS hook_executions (
					id TEXT PRIMARY KEY,
					session_id TEXT,
					event_name TEXT NOT NULL,
					matcher TEXT,
					hook_type TEXT NOT NULL,
					command TEXT,
					input_json TEXT,
					stdout TEXT,
					stderr TEXT,
					exit_code INTEGER,
					duration_ms INTEGER,
					blocked BOOLEAN DEFAULT 0,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (session_id) REFERENCES agent_sessions(id) ON DELETE SET NULL,
					CONSTRAINT hook_type_check CHECK (hook_type IN ('command', 'http', 'prompt', 'agent'))
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create hook_executions table: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_hook_executions_session ON hook_executions(session_id)`)
			if err != nil {
				return fmt.Errorf("failed to create hook_executions session index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_hook_executions_event ON hook_executions(event_name)`)
			if err != nil {
				return fmt.Errorf("failed to create hook_executions event index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_hook_executions_created ON hook_executions(created_at DESC)`)
			if err != nil {
				return fmt.Errorf("failed to create hook_executions created index: %w", err)
			}

			return nil
		},
	},
	{
		Version:     21,
		Description: "Add disabled column to avatar_themes for hiding built-in themes",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE avatar_themes ADD COLUMN disabled BOOLEAN DEFAULT 0`)
			if err != nil {
				// Column may already exist from schema.sql
			}
			return nil
		},
	},
	{
		Version:     22,
		Description: "Add GLM-5 and GLM-5.1 models",
		Up: func(tx *sql.Tx) error {
			// Update GLM provider's models list to include GLM-5 and GLM-5.1
			newModels := []string{
				"glm-4.6",
				"glm-4.5-air",
				"glm-5",
				"glm-5.1",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal GLM models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'glm'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update GLM models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     23,
		Description: "Add PKCE code_verifier column to oauth_state_tokens",
		Up: func(tx *sql.Tx) error {
			// SECURITY: Add code_verifier column for PKCE support (AUTH-VULN-05)
			_, err := tx.Exec(`
				ALTER TABLE oauth_state_tokens
				ADD COLUMN code_verifier TEXT DEFAULT ''
			`)
			if err != nil {
				// Column might already exist
				return nil
			}
			return nil
		},
	},
	{
		Version:     25,
		Description: "Update GLM models to include glm-5-turbo, glm-4.7, glm-4.7-flash, glm-4.5",
		Up: func(tx *sql.Tx) error {
			newModels := []string{
				"glm-5",
				"glm-5.1",
				"glm-5-turbo",
				"glm-4.7",
				"glm-4.7-flash",
				"glm-4.6",
				"glm-4.5",
				"glm-4.5-air",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal GLM models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, default_model = 'glm-5', updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'glm'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update GLM models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     24,
		Description: "Add default_skill_ids column to projects table",
		Up: func(tx *sql.Tx) error {
			// Check if column already exists
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0 FROM pragma_table_info('projects') WHERE name = 'default_skill_ids'
			`).Scan(&exists)
			if err != nil || exists {
				return nil
			}

			_, err = tx.Exec(`ALTER TABLE projects ADD COLUMN default_skill_ids TEXT DEFAULT '[]'`)
			if err != nil {
				return fmt.Errorf("failed to add default_skill_ids column to projects: %w", err)
			}

			return nil
		},
	},
	{
		Version:     26,
		Description: "Add unique constraint on agent_messages(session_id, sequence, role) to prevent duplicates",
		Up: func(tx *sql.Tx) error {
			// Step 1: Remove duplicate messages, keeping the one with the most content
			// For each (session_id, sequence, role) group, keep the row with the longest
			// combined content (content + thinking_content + tool_uses)
			_, err := tx.Exec(`
				DELETE FROM agent_messages
				WHERE id NOT IN (
					SELECT id FROM (
						SELECT id,
							ROW_NUMBER() OVER (
								PARTITION BY session_id, sequence, role
								ORDER BY
									length(COALESCE(content, '')) + length(COALESCE(thinking_content, '')) + length(COALESCE(tool_uses, '')) DESC,
									timestamp ASC
							) as rn
						FROM agent_messages
					) ranked
					WHERE rn = 1
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to deduplicate agent_messages: %w", err)
			}

			// Step 2: Create unique index to prevent future duplicates
			// This enables the ON CONFLICT clause in SaveMessage upserts
			_, err = tx.Exec(`
				CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_messages_unique_seq
					ON agent_messages(session_id, sequence, role)
			`)
			if err != nil {
				return fmt.Errorf("failed to create unique index on agent_messages: %w", err)
			}

			return nil
		},
	},
	{
		Version:     27,
		Description: "Add Gitea connector definition",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				INSERT OR IGNORE INTO connector_definitions (slug, name, description, category, auth_type, icon, bg_class, sort_order, is_active)
				VALUES ('gitea', 'Gitea', 'Gitea API for self-hosted Git repositories and issue tracking', 'productivity', 'api_key', 'git-branch', 'bg-green', 13, 1)
			`)
			if err != nil {
				return fmt.Errorf("failed to insert gitea connector: %w", err)
			}
			return nil
		},
	},
	{
		Version:     28,
		Description: "Update Claude models with Opus 4.7, Sonnet 4.6, and Opus 4.6",
		Up: func(tx *sql.Tx) error {
			newModels := []string{
				"sonnet",
				"opus",
				"haiku",
				"claude-opus-4-7",
				"claude-sonnet-4-6",
				"claude-opus-4-6",
				"claude-haiku-4-5-20251001",
				"claude-opus-4-5-20251101",
				"claude-sonnet-4-5-20250929",
				"claude-3-5-sonnet-20241022",
				"claude-3-5-haiku-20241022",
				"claude-3-haiku-20240307",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal Claude models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'claude'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update Claude models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     29,
		Description: "Add Kimi K2.6 model",
		Up: func(tx *sql.Tx) error {
			newModels := []string{
				"kimi-k2.6",
				"kimi-k2.5",
				"kimi-k2",
				"kimi-k2-thinking",
				"kimi-k2-0905",
				"kimi-k2-turbo-preview",
				"moonshot-v1-128k",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal Kimi models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'kimi'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update Kimi models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     30,
		Description: "Add Sentry connector",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				INSERT OR IGNORE INTO connector_definitions (slug, name, description, category, auth_type, icon, bg_class, sort_order, is_active)
				VALUES ('sentry', 'Sentry', 'Sentry API for error tracking and performance monitoring', 'tools', 'api_key', 'alert-triangle', 'bg-purple', 34, 1)
			`)
			if err != nil {
				return fmt.Errorf("failed to insert sentry connector: %w", err)
			}
			return nil
		},
	},
	{
		Version:     32,
		Description: "Add system_prompt column to projects table",
		Up: func(tx *sql.Tx) error {
			var exists bool
			err := tx.QueryRow(`
				SELECT COUNT(*) > 0 FROM pragma_table_info('projects') WHERE name = 'system_prompt'
			`).Scan(&exists)
			if err != nil || exists {
				return nil
			}

			_, err = tx.Exec(`ALTER TABLE projects ADD COLUMN system_prompt TEXT DEFAULT ''`)
			if err != nil {
				return fmt.Errorf("failed to add system_prompt column to projects: %w", err)
			}

			return nil
		},
	},
	{
		Version:     31,
		Description: "Create memories table for Memory Palace",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS memories (
					id TEXT PRIMARY KEY,
					project_id TEXT NOT NULL,
					area_id TEXT,
					session_id TEXT,
					memory_type TEXT NOT NULL,
					title TEXT NOT NULL,
					content TEXT NOT NULL,
					tags TEXT,
					importance INTEGER NOT NULL DEFAULT 5,
					is_pinned BOOLEAN DEFAULT 0,
					is_archived BOOLEAN DEFAULT 0,
					source TEXT NOT NULL DEFAULT 'user',
					access_count INTEGER DEFAULT 0,
					last_accessed_at TIMESTAMP,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
					FOREIGN KEY (area_id) REFERENCES project_areas(id) ON DELETE SET NULL,
					FOREIGN KEY (session_id) REFERENCES agent_sessions(id) ON DELETE SET NULL,
					CONSTRAINT memory_type_check CHECK (memory_type IN ('decision', 'pattern', 'gotcha', 'preference', 'architecture', 'convention', 'note')),
					CONSTRAINT source_check CHECK (source IN ('agent', 'user', 'auto'))
				)
			`)
			if err != nil {
				return fmt.Errorf("failed to create memories table: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_project ON memories(project_id, is_archived, importance DESC)`)
			if err != nil {
				return fmt.Errorf("failed to create memories project index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_area ON memories(area_id, is_archived) WHERE area_id IS NOT NULL`)
			if err != nil {
				return fmt.Errorf("failed to create memories area index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(project_id, memory_type, is_archived)`)
			if err != nil {
				return fmt.Errorf("failed to create memories type index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_pinned ON memories(project_id, is_pinned) WHERE is_pinned = 1`)
			if err != nil {
				return fmt.Errorf("failed to create memories pinned index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_session ON memories(session_id) WHERE session_id IS NOT NULL`)
			if err != nil {
				return fmt.Errorf("failed to create memories session index: %w", err)
			}

			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_created ON memories(created_at DESC)`)
			if err != nil {
				return fmt.Errorf("failed to create memories created index: %w", err)
			}

			return nil
		},
	},
	{
		Version:     33,
		Description: "Add claude-opus-4-8 to Anthropic models",
		Up: func(tx *sql.Tx) error {
			newModels := []string{
				"sonnet",
				"opus",
				"haiku",
				"claude-opus-4-8",
				"claude-opus-4-7",
				"claude-sonnet-4-6",
				"claude-opus-4-6",
				"claude-haiku-4-5-20251001",
				"claude-opus-4-5-20251101",
				"claude-sonnet-4-5-20250929",
				"claude-3-5-sonnet-20241022",
				"claude-3-5-haiku-20241022",
				"claude-3-haiku-20240307",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal Claude models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'claude'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update Claude models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     34,
		Description: "Add claude-fable-5 to Anthropic models",
		Up: func(tx *sql.Tx) error {
			newModels := []string{
				"sonnet",
				"opus",
				"haiku",
				"fable",
				"claude-fable-5",
				"claude-opus-4-8",
				"claude-opus-4-7",
				"claude-sonnet-4-6",
				"claude-opus-4-6",
				"claude-haiku-4-5-20251001",
				"claude-opus-4-5-20251101",
				"claude-sonnet-4-5-20250929",
				"claude-3-5-sonnet-20241022",
				"claude-3-5-haiku-20241022",
				"claude-3-haiku-20240307",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal Claude models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'claude'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update Claude models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     35,
		Description: "Rebrand nzero connector to n0",
		Up: func(tx *sql.Tx) error {
			// Insert the n0 connector definition (idempotent)
			_, err := tx.Exec(`
				INSERT OR IGNORE INTO connector_definitions
				(slug, name, description, category, auth_type, icon, bg_class, sort_order, is_active)
				VALUES ('n0', 'n0', 'n0 (nZero) platform API for privacy-first team communication and AI agents',
					'tools', 'api_key', 'shield', 'bg-teal', 35, 1)
			`)
			if err != nil {
				return fmt.Errorf("failed to insert n0 connector definition: %w", err)
			}

			// Re-point existing connections and session links from nzero to n0.
			// OR IGNORE handles the (unlikely) case where both slugs already have rows,
			// which would otherwise violate UNIQUE constraints.
			_, err = tx.Exec(`UPDATE OR IGNORE user_connections SET connector_slug = 'n0', updated_at = CURRENT_TIMESTAMP WHERE connector_slug = 'nzero'`)
			if err != nil {
				return fmt.Errorf("failed to migrate user_connections to n0: %w", err)
			}

			_, err = tx.Exec(`UPDATE OR IGNORE session_connectors SET connector_slug = 'n0' WHERE connector_slug = 'nzero'`)
			if err != nil {
				return fmt.Errorf("failed to migrate session_connectors to n0: %w", err)
			}

			// Remove the old nzero definition (cascades any leftover duplicate rows)
			_, err = tx.Exec(`DELETE FROM connector_definitions WHERE slug = 'nzero'`)
			if err != nil {
				return fmt.Errorf("failed to remove nzero connector definition: %w", err)
			}

			return nil
		},
	},
	{
		Version:     36,
		Description: "Add claude-opus-5 to Anthropic models",
		Up: func(tx *sql.Tx) error {
			newModels := []string{
				"sonnet",
				"opus",
				"haiku",
				"fable",
				"claude-fable-5",
				"claude-opus-5",
				"claude-opus-4-8",
				"claude-opus-4-7",
				"claude-sonnet-4-6",
				"claude-opus-4-6",
				"claude-haiku-4-5-20251001",
				"claude-opus-4-5-20251101",
				"claude-sonnet-4-5-20250929",
				"claude-3-5-sonnet-20241022",
				"claude-3-5-haiku-20241022",
				"claude-3-haiku-20240307",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal Claude models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'claude'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update Claude models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     37,
		Description: "Add claude-fable-5-1 and claude-sonnet-5 to Anthropic models",
		Up: func(tx *sql.Tx) error {
			// Claude Fable 5.1 (released 2026-09-01) is the current frontier
			// model, and Claude Sonnet 5 was missing from the registry entirely
			// even though it is part of the current lineup. Both are listed
			// newest-first, after the bare CLI aliases.
			newModels := []string{
				"sonnet",
				"opus",
				"haiku",
				"fable",
				"claude-fable-5-1",
				"claude-fable-5",
				"claude-opus-5",
				"claude-sonnet-5",
				"claude-opus-4-8",
				"claude-opus-4-7",
				"claude-sonnet-4-6",
				"claude-opus-4-6",
				"claude-haiku-4-5-20251001",
				"claude-opus-4-5-20251101",
				"claude-sonnet-4-5-20250929",
				"claude-3-5-sonnet-20241022",
				"claude-3-5-haiku-20241022",
				"claude-3-haiku-20240307",
			}

			modelsJSON, err := json.Marshal(newModels)
			if err != nil {
				return fmt.Errorf("failed to marshal Claude models: %w", err)
			}

			_, err = tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'claude'
			`, string(modelsJSON))

			if err != nil {
				return fmt.Errorf("failed to update Claude models: %w", err)
			}

			return nil
		},
	},
	{
		Version:     38,
		Description: "Add OpenAI Codex provider",
		Up: func(tx *sql.Tx) error {
			// The Codex provider runs sessions through the codex CLI
			// (codex-sdk-go) rather than an HTTP API, so it has no base URL.
			// An API key is optional: the CLI can authenticate via `codex login`.
			// Model slugs verified against codex-cli 0.153.4 (2026-09-06); the
			// CLI's own default is gpt-6-astra.
			models := []string{
				"gpt-6-astra",
				"gpt-5.6-sol",
				"gpt-5.6-terra",
				"gpt-5.6-luna",
				"gpt-5.6-pro",
				"gpt-5.5",
				"gpt-5.3-codex-spark",
				"gpt-5.3-codex",
				"gpt-5.2-codex",
				"gpt-5.2",
			}
			modelsJSON, err := json.Marshal(models)
			if err != nil {
				return fmt.Errorf("failed to marshal Codex models: %w", err)
			}

			_, err = tx.Exec(`
				INSERT INTO providers (provider_id, name, icon, base_url, models, default_model, description, enabled, is_configured)
				VALUES ('codex', 'OpenAI Codex', '🟢', '', ?, 'gpt-6-astra', ?, 1, 0)
				ON CONFLICT(provider_id) DO UPDATE SET
					name = excluded.name,
					icon = excluded.icon,
					models = excluded.models,
					default_model = excluded.default_model,
					description = excluded.description,
					updated_at = CURRENT_TIMESTAMP
			`, string(modelsJSON), "OpenAI Codex coding agent via the codex CLI — sign in with `codex login` (ChatGPT) or set an API key")
			if err != nil {
				return fmt.Errorf("failed to insert Codex provider: %w", err)
			}
			return nil
		},
	},
	{
		Version:     39,
		Description: "Sync Kimi and GLM models with the providers' live model lists",
		Up: func(tx *sql.Tx) error {
			// Model ids verified on 2026-09-08 against the providers' own
			// /v1/models endpoints, which are the authority on what each API
			// will actually accept:
			//   GET https://api.moonshot.ai/v1/models
			//   GET https://api.z.ai/api/anthropic/v1/models
			//
			// Moonshot has retired the whole kimi-k2* line except k2.6, so the
			// stale ids are dropped rather than left in the picker to fail at
			// request time. That includes kimi-k2, which was the stored default
			// and is no longer served. glm-4.7-flash goes for the same reason:
			// migration 25 introduced it, but Z.ai has never listed it.
			updates := []struct {
				providerID   string
				models       []string
				defaultModel string
			}{
				{
					providerID:   "kimi",
					models:       []string{"kimi-k3", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6"},
					defaultModel: "kimi-k3",
				},
				{
					providerID: "glm",
					models: []string{
						"glm-5.3", "glm-5.3-flash", "glm-5.2", "glm-5.1", "glm-5-turbo",
						"glm-5", "glm-4.7", "glm-4.6", "glm-4.5", "glm-4.5-air",
					},
					defaultModel: "glm-5.3",
				},
			}

			for _, u := range updates {
				modelsJSON, err := json.Marshal(u.models)
				if err != nil {
					return fmt.Errorf("failed to marshal %s models: %w", u.providerID, err)
				}
				if _, err := tx.Exec(`
					UPDATE providers
					SET models = ?, default_model = ?, updated_at = CURRENT_TIMESTAMP
					WHERE provider_id = ?
				`, string(modelsJSON), u.defaultModel, u.providerID); err != nil {
					return fmt.Errorf("failed to update %s models: %w", u.providerID, err)
				}
			}

			// Sessions and provider rows pinned to a retired model would fail on
			// their next request, so move them onto the current flagship.
			retired := map[string][]any{
				"kimi": {"kimi-k3", "kimi-k2", "kimi-k2.5", "kimi-k2-thinking", "kimi-k2-0905", "kimi-k2-turbo-preview", "moonshot-v1-128k"},
				"glm":  {"glm-5.3", "glm-4.7-flash"},
			}
			for providerID, args := range retired {
				placeholders := ""
				for i := 1; i < len(args); i++ {
					if i > 1 {
						placeholders += ", "
					}
					placeholders += "?"
				}
				if _, err := tx.Exec(fmt.Sprintf(`
					UPDATE providers
					SET model_name = ?, updated_at = CURRENT_TIMESTAMP
					WHERE provider_id = '%s' AND model_name IN (%s)
				`, providerID, placeholders), args...); err != nil {
					return fmt.Errorf("failed to repoint %s provider model: %w", providerID, err)
				}
				if _, err := tx.Exec(fmt.Sprintf(`
					UPDATE agent_sessions
					SET model_name = ?
					WHERE provider = '%s' AND model_name IN (%s)
				`, providerID, placeholders), args...); err != nil {
					return fmt.Errorf("failed to repoint %s sessions: %w", providerID, err)
				}
			}

			return nil
		},
	},
	{
		Version:     40,
		Description: "Sync DeepSeek models with the provider's live model list",
		Up: func(tx *sql.Tx) error {
			// Verified on 2026-09-12 against https://api.deepseek.com/models and the
			// Anthropic-compatible endpoint eggify actually calls. The V3 and R1
			// generations are gone: DeepSeek rejects them with "The supported API
			// model names are deepseek-flash, deepseek-v4-pro", so nine of the
			// eleven entries in the picker were dead options. deepseek-chat and
			// deepseek-reasoner still work as aliases (both served by
			// deepseek-v4-flash) and are kept.
			models := []string{"deepseek-v4-pro", "deepseek-flash", "deepseek-chat", "deepseek-reasoner"}
			modelsJSON, err := json.Marshal(models)
			if err != nil {
				return fmt.Errorf("failed to marshal DeepSeek models: %w", err)
			}
			if _, err := tx.Exec(`
				UPDATE providers
				SET models = ?, default_model = 'deepseek-v4-pro', updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'deepseek'
			`, string(modelsJSON)); err != nil {
				return fmt.Errorf("failed to update DeepSeek models: %w", err)
			}

			// Anything still pinned to a retired id would fail on its next request.
			retired := []any{"deepseek-v4-pro", "DeepSeek-V3.2-Speciale", "DeepSeek-V3.2-Exp",
				"DeepSeek-V3.1", "DeepSeek-V3.1-Terminus", "DeepSeek-V3-Base", "DeepSeek-V3",
				"DeepSeek-R1", "DeepSeek-R1-Zero", "DeepSeek-R1-Lite"}
			placeholders := ""
			for i := 1; i < len(retired); i++ {
				if i > 1 {
					placeholders += ", "
				}
				placeholders += "?"
			}
			if _, err := tx.Exec(fmt.Sprintf(`
				UPDATE providers
				SET model_name = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'deepseek' AND model_name IN (%s)
			`, placeholders), retired...); err != nil {
				return fmt.Errorf("failed to repoint DeepSeek provider model: %w", err)
			}
			if _, err := tx.Exec(fmt.Sprintf(`
				UPDATE agent_sessions
				SET model_name = ?
				WHERE provider = 'deepseek' AND model_name IN (%s)
			`, placeholders), retired...); err != nil {
				return fmt.Errorf("failed to repoint DeepSeek sessions: %w", err)
			}

			return nil
		},
	},
	{
		Version:     41,
		Description: "Preserve message attribution and model switch context",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE agent_messages ADD COLUMN provider TEXT;
				ALTER TABLE agent_messages ADD COLUMN model_name TEXT;
				CREATE TABLE agent_model_switches (
					message_id TEXT PRIMARY KEY REFERENCES agent_messages(id) ON DELETE CASCADE,
					session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
					history_mode TEXT NOT NULL,
					context TEXT,
					format TEXT NOT NULL DEFAULT 'text'
				);
				CREATE INDEX idx_agent_model_switches_session ON agent_model_switches(session_id);`)
			return err
		},
	},
	{
		Version:     42,
		Description: "Allow a provisional model switch context preview before the first request",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE agent_model_switches ADD COLUMN preview INTEGER NOT NULL DEFAULT 0;`)
			return err
		},
	},
	{
		Version:     43,
		Description: "Add Claude Opus 5.5 and GPT-6 Sol/Luna; retire gpt-5.2-codex",
		Up: func(tx *sql.Tx) error {
			// Claude Opus 5.5 (claude-opus-5-5, released 2026-09-22) replaces
			// Opus 5 as the recommended default-tier model. Claude Code >= 2.1.280
			// is required to run it; the bare "opus" alias resolves to it there.
			claudeModels := []string{
				"sonnet",
				"opus",
				"haiku",
				"fable",
				"claude-opus-5-5",
				"claude-fable-5-1",
				"claude-fable-5",
				"claude-opus-5",
				"claude-sonnet-5",
				"claude-opus-4-8",
				"claude-opus-4-7",
				"claude-sonnet-4-6",
				"claude-opus-4-6",
				"claude-haiku-4-5-20251001",
				"claude-opus-4-5-20251101",
				"claude-sonnet-4-5-20250929",
				"claude-3-5-sonnet-20241022",
				"claude-3-5-haiku-20241022",
				"claude-3-haiku-20240307",
			}
			claudeJSON, err := json.Marshal(claudeModels)
			if err != nil {
				return fmt.Errorf("failed to marshal claude models: %w", err)
			}
			if _, err := tx.Exec(`
				UPDATE providers
				SET models = ?, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = 'claude'
			`, string(claudeJSON)); err != nil {
				return fmt.Errorf("failed to update claude models: %w", err)
			}

			// GPT-6 Sol and GPT-6 Luna (released 2026-09-22) join GPT-6 Astra.
			// gpt-5.2-codex was shut down on the OpenAI API on 2026-07-23 and is
			// removed; anything still pinned to it is repointed to gpt-6-sol.
			codexModels := []string{
				"gpt-6-astra",
				"gpt-6-sol",
				"gpt-6-luna",
				"gpt-5.6-sol",
				"gpt-5.6-terra",
				"gpt-5.6-luna",
				"gpt-5.6-pro",
				"gpt-5.5",
				"gpt-5.3-codex-spark",
				"gpt-5.3-codex",
				"gpt-5.2",
			}
			codexJSON, err := json.Marshal(codexModels)
			if err != nil {
				return fmt.Errorf("failed to marshal codex models: %w", err)
			}
			if _, err := tx.Exec(`
				INSERT INTO providers (provider_id, name, icon, base_url, models, default_model, description, enabled, is_configured)
				VALUES ('codex', 'OpenAI Codex', '🟢', '', ?, 'gpt-6-astra', ?, 1, 0)
				ON CONFLICT(provider_id) DO UPDATE SET
					models = excluded.models,
					updated_at = CURRENT_TIMESTAMP
			`, string(codexJSON), "OpenAI Codex coding agent via the codex CLI — sign in with `codex login` (ChatGPT) or set an API key"); err != nil {
				return fmt.Errorf("failed to update codex models: %w", err)
			}

			for _, stmt := range []string{
				`UPDATE providers SET model_name = 'gpt-6-sol', updated_at = CURRENT_TIMESTAMP WHERE provider_id = 'codex' AND model_name = 'gpt-5.2-codex'`,
				`UPDATE agent_sessions SET model_name = 'gpt-6-sol' WHERE provider = 'codex' AND model_name = 'gpt-5.2-codex'`,
			} {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("failed to repoint retired codex model: %w", err)
				}
			}
			return nil
		},
	},
	{
		Version:     44,
		Description: "Keep subagent output, nesting and launch mode on background_agents",
		Up: func(tx *sql.Tx) error {
			// recent_output holds the last output lines as a JSON array, so the
			// subagent modal and the agent overview can show them after a reload.
			columns := []struct{ name, definition string }{
				{"recent_output", "TEXT"},
				{"parent_agent_id", "TEXT NOT NULL DEFAULT ''"},
				{"background", "INTEGER NOT NULL DEFAULT 0"},
			}
			for _, column := range columns {
				var exists bool
				if err := tx.QueryRow(
					`SELECT COUNT(*) > 0 FROM pragma_table_info('background_agents') WHERE name = ?`, column.name,
				).Scan(&exists); err != nil {
					return fmt.Errorf("failed to check background_agents.%s: %w", column.name, err)
				}
				if exists {
					continue
				}
				if _, err := tx.Exec("ALTER TABLE background_agents ADD COLUMN " + column.name + " " + column.definition); err != nil {
					return fmt.Errorf("failed to add background_agents.%s: %w", column.name, err)
				}
			}
			return nil
		},
	},
	{
		// Version 44 is reserved for background_agents.recent_output (PR #46).
		Version:     45,
		Description: "Add n0 OAuth connections (MCP over OAuth, no API keys)",
		Up:          migrateN0OAuth,
	},
	{
		Version:     46,
		Description: "Cache AI summaries and tags of agent sessions for the overview",
		Up: func(tx *sql.Tx) error {
			// source_version records what the session looked like when the
			// digest was written (message count and status), so it is only
			// regenerated once the session has moved on.
			_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_digests (
				session_id     TEXT PRIMARY KEY,
				summary        TEXT NOT NULL DEFAULT '',
				tags           TEXT NOT NULL DEFAULT '[]',
				source_version TEXT NOT NULL DEFAULT '',
				updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`)
			return err
		},
	},
	{
		Version:     47,
		Description: "Remove rows orphaned by deleted agent sessions; drop redundant agent_messages indexes",
		Up:          migrateRemoveSessionOrphans,
	},
	{
		Version:     48,
		Description: "Index agent_sessions by updated_at for the unfiltered session list",
		Up: func(tx *sql.Tx) error {
			// The session list orders all sessions by updated_at; the existing
			// (status, updated_at) index only serves status-filtered lists.
			_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_sessions_updated
				ON agent_sessions(updated_at DESC)`)
			return err
		},
	},
	{
		Version:     49,
		Description: "Store bearer tokens (user sessions, MFA login tokens, handover tokens) as SHA-256 hashes",
		Up:          migrateHashBearerTokens,
	},
	{
		// 50 is the memory/skill review queue.
		Version:     50,
		Description: "Track who wrote memories and skills; agent-written ones wait for user review",
		Up:          migrateReviewProvenance,
	},
	{
		Version:     51,
		Description: "Add trusted_workspaces; grandfather folders that already ran sessions",
		Up:          migrateWorkspaceTrust,
	},
	{
		Version:     52,
		Description: "Add Claude Fable 5.5 to Anthropic models",
		Up:          migrateAddClaudeFable55,
	},
	{
		// 53: skills discovered from SKILL.md files go through review.
		Version:     53,
		Description: "Remember the content hash of skill files so discovered skills go through review",
		Up:          migrateSkillSourceHash,
	},
	{
		// 54: QR-code pairing of the iOS app, and device info on user sessions
		// so a user can see and sign out their devices.
		Version:     54,
		Description: "Add pairing_codes (hashed, single-use) and device metadata on user_sessions",
		Up:          migratePairing,
	},
	{
		// 55: Claude Sonnet 5.5 (claude-sonnet-5-5, announced 2026-09-28,
		// needs Claude Code >= 2.1.284).
		Version:     55,
		Description: "Add Claude Sonnet 5.5 to Anthropic models",
		Up:          migrateAddClaudeSonnet55,
	},
	{
		// 56: native skills. A discovered skill's review covers its whole
		// folder (scripts, templates), not only SKILL.md.
		Version:     56,
		Description: "Remember the fingerprint of a skill's folder so the review covers its scripts",
		Up:          migrateSkillFolderHash,
	},
	{
		// 57: a resumed subagent (SendMessage) reports its completion by task
		// id only, so the binding must outlive a restart.
		Version:     57,
		Description: "Remember a background agent's CLI task id",
		Up:          migrateBackgroundAgentTaskID,
	},
	{
		// 58: the Subagents view shows what each agent was asked to do.
		Version:     58,
		Description: "Remember a background agent's prompt",
		Up:          migrateBackgroundAgentPrompt,
	},
	{
		// 59: a session's own tags (sidebar "Add tag", ⌘S) are stored on the
		// server instead of only in the browser, so the iOS app shows them.
		Version:     59,
		Description: "Store user session tags on the server",
		Up:          migrateSessionTags,
	},
	{
		// 60: Claude Haiku 5.5 (claude-haiku-5-5, released 2026-10-07).
		Version:     60,
		Description: "Add Claude Haiku 5.5 to Anthropic models",
		Up:          migrateAddClaudeHaiku55,
	},
	{
		// 61: when each kept output line of a subagent was produced, so the
		// Subagents view can show a time and duration per step.
		Version:     61,
		Description: "Remember when a background agent's recent output lines were produced",
		Up:          migrateBackgroundAgentOutputTimes,
	},
	{
		// 62: the model each subagent runs on, so viewers can show it
		// (Haiku 4.5, Sonnet 5.5, ...) after a reload or restart.
		Version:     62,
		Description: "Remember the model a background agent runs on",
		Up:          migrateBackgroundAgentModel,
	},
	{
		// 63: Claude Fable 5.2 (claude-fable-5-2, released 2026-11-09, the
		// default Fable model; needs Claude Code >= 2.1.321).
		Version:     63,
		Description: "Add Claude Fable 5.2 to Anthropic models",
		Up:          migrateAddClaudeFable52,
	},
	{
		// 64: Claude Mythos 5.2 and 5.1 (claude-mythos-5-2, claude-mythos-5-1),
		// listed after the Fable models like the embedded list.
		Version:     64,
		Description: "Add Claude Mythos 5.2 and 5.1 to Anthropic models",
		Up:          migrateAddClaudeMythos,
	},
	{
		// 65: which session minted a pairing-created session, so revoking
		// the minter revokes it too.
		Version:     65,
		Description: "Record the session that minted a pairing-created session",
		Up:          migrateMintedSessions,
	},
	{
		// 66: persisted MFA attempt locks and last accepted TOTP steps.
		Version:     66,
		Description: "Persist the MFA throttle (attempt locks, last accepted TOTP step)",
		Up:          migrateMFAThrottle,
	},
}

// migrateBackgroundAgentModel adds background_agents.model and model_source
// ("actual" from the agent's own messages, "requested" from its launch).
func migrateBackgroundAgentModel(tx *sql.Tx) error {
	for _, col := range []string{"model", "model_source"} {
		var exists bool
		if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('background_agents') WHERE name = ?`, col).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check background_agents.%s: %w", col, err)
		}
		if exists {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE background_agents ADD COLUMN ` + col + ` TEXT`); err != nil {
			return fmt.Errorf("failed to add background_agents.%s: %w", col, err)
		}
	}
	return nil
}

// migrateBackgroundAgentOutputTimes adds background_agents.recent_output_at:
// a JSON array of unix-millisecond times, parallel to recent_output. Older
// rows keep NULL (no times known).
func migrateBackgroundAgentOutputTimes(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('background_agents') WHERE name = 'recent_output_at'`).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check background_agents.recent_output_at: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE background_agents ADD COLUMN recent_output_at TEXT`); err != nil {
		return fmt.Errorf("failed to add background_agents.recent_output_at: %w", err)
	}
	return nil
}

// migrateSessionTags creates session_tags: one row per tagged session, the
// tags as a JSON array. Rows go with their session (ON DELETE CASCADE, and
// sessionChildTables for orphans).
func migrateSessionTags(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_tags (
		session_id TEXT PRIMARY KEY REFERENCES agent_sessions(id) ON DELETE CASCADE,
		tags       TEXT NOT NULL DEFAULT '[]',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("failed to create session_tags: %w", err)
	}
	return nil
}

// migrateBackgroundAgentPrompt adds background_agents.prompt.
func migrateBackgroundAgentPrompt(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('background_agents') WHERE name = 'prompt'`).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check background_agents.prompt: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE background_agents ADD COLUMN prompt TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("failed to add background_agents.prompt: %w", err)
	}
	return nil
}

// migrateBackgroundAgentTaskID adds background_agents.task_id.
func migrateBackgroundAgentTaskID(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('background_agents') WHERE name = 'task_id'`).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check background_agents.task_id: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE background_agents ADD COLUMN task_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("failed to add background_agents.task_id: %w", err)
	}
	return nil
}

// migrateSkillFolderHash adds skills.folder_hash: the fingerprint of the files
// next to SKILL.md that belong to the row's current (pending or approved)
// content. Existing rows start empty; discovery re-fingerprints an approved
// skill whose SKILL.md still matches once (see SyncDiscoveredSkill).
func migrateSkillFolderHash(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('skills') WHERE name = 'folder_hash'`).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check skills.folder_hash: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE skills ADD COLUMN folder_hash TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("failed to add skills.folder_hash: %w", err)
	}
	return nil
}

// migrateAddClaudeFable55 adds claude-fable-5-5 to the Claude provider's model
// list, just ahead of claude-opus-5-5.
func migrateAddClaudeFable55(tx *sql.Tx) error {
	return insertClaudeModel(tx, "claude-fable-5-5", "claude-opus-5-5")
}

// migrateAddClaudeSonnet55 adds claude-sonnet-5-5 to the Claude provider's
// model list just after claude-opus-5-5 (ahead of claude-fable-5-1), where
// the seed file has it.
func migrateAddClaudeSonnet55(tx *sql.Tx) error {
	return insertClaudeModel(tx, "claude-sonnet-5-5", "claude-fable-5-1")
}

// migrateAddClaudeHaiku55 adds claude-haiku-5-5 to the Claude provider's
// model list just after claude-sonnet-5-5 (ahead of claude-fable-5-1), where
// the seed file has it.
func migrateAddClaudeHaiku55(tx *sql.Tx) error {
	return insertClaudeModel(tx, "claude-haiku-5-5", "claude-fable-5-1")
}

// migrateAddClaudeFable52 adds claude-fable-5-2 ahead of the older Fable
// models (or Opus 5.5 when the list has none), like the embedded list.
func migrateAddClaudeFable52(tx *sql.Tx) error {
	var raw sql.NullString
	err := tx.QueryRow(`SELECT models FROM providers WHERE provider_id = 'claude'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read claude models: %w", err)
	}
	var models []string
	if raw.Valid && raw.String != "" {
		if err := json.Unmarshal([]byte(raw.String), &models); err != nil {
			return fmt.Errorf("failed to parse claude models: %w", err)
		}
	}
	have := make(map[string]bool, len(models))
	for _, m := range models {
		have[m] = true
	}
	before := "claude-opus-5-5"
	for _, m := range []string{"claude-fable-5-5", "claude-fable-5-1", "claude-fable-5"} {
		if have[m] {
			before = m
			break
		}
	}
	return insertClaudeModel(tx, "claude-fable-5-2", before)
}

// migrateAddClaudeMythos adds claude-mythos-5-2 and claude-mythos-5-1 to the
// Claude provider's model list ahead of claude-opus-5 (after the Fable
// models), where the seed file has them; at the end if the list has no Opus 5.
func migrateAddClaudeMythos(tx *sql.Tx) error {
	for _, model := range []string{"claude-mythos-5-2", "claude-mythos-5-1"} {
		if err := insertClaudeModel(tx, model, "claude-opus-5"); err != nil {
			return err
		}
	}
	return nil
}

// insertClaudeModel inserts newModel into the Claude provider's stored model
// list just before `before` (or at the end if `before` is missing). It edits
// the stored list rather than replacing it, so other edits to that list
// survive, and does nothing if the model is already there or there is no
// Claude provider row.
func insertClaudeModel(tx *sql.Tx, newModel, before string) error {
	var raw sql.NullString
	err := tx.QueryRow(`SELECT models FROM providers WHERE provider_id = 'claude'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read claude models: %w", err)
	}
	var models []string
	if raw.Valid && raw.String != "" {
		if err := json.Unmarshal([]byte(raw.String), &models); err != nil {
			return fmt.Errorf("failed to parse claude models: %w", err)
		}
	}
	for _, m := range models {
		if m == newModel {
			return nil
		}
	}
	at := len(models)
	for i, m := range models {
		if m == before {
			at = i
			break
		}
	}
	models = append(models[:at], append([]string{newModel}, models[at:]...)...)
	out, err := json.Marshal(models)
	if err != nil {
		return fmt.Errorf("failed to marshal claude models: %w", err)
	}
	if _, err := tx.Exec(`UPDATE providers SET models = ?, updated_at = CURRENT_TIMESTAMP WHERE provider_id = 'claude'`, string(out)); err != nil {
		return fmt.Errorf("failed to update claude models: %w", err)
	}
	return nil
}

// migrateWorkspaceTrust creates trusted_workspaces and trusts, with their
// current config fingerprint, the folders existing sessions already run in.
// Those sessions have been loading that config all along, so cutting it off
// at upgrade would break them without making anything safer; only config that
// is new or changed after the upgrade asks the user.
func migrateWorkspaceTrust(tx *sql.Tx) error {
	if _, err := tx.Exec(workspacetrust.CreateTableSQL); err != nil {
		return fmt.Errorf("failed to create trusted_workspaces: %w", err)
	}
	rows, err := tx.Query(`SELECT DISTINCT json_extract(options, '$.working_directory') FROM agent_sessions
		WHERE options IS NOT NULL AND json_valid(options)
		AND COALESCE(json_extract(options, '$.working_directory'), '') != ''`)
	if err != nil {
		return fmt.Errorf("failed to list session folders: %w", err)
	}
	var dirs []string
	for rows.Next() {
		var dir sql.NullString
		if err := rows.Scan(&dir); err != nil {
			rows.Close()
			return err
		}
		if dir.Valid {
			dirs = append(dirs, dir.String)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	opts := workspacetrust.Options{EggifyBinaries: workspacetrust.DefaultEggifyBinaries()}
	seen := map[string]bool{}
	for _, dir := range dirs {
		inv, err := workspacetrust.Scan(dir, opts)
		if err != nil || !inv.HasConfig() || seen[inv.Path] {
			continue // missing/unreadable folders and folders without config need no row
		}
		seen[inv.Path] = true
		if err := workspacetrust.Upsert(tx, inv.Path, inv.Fingerprint, workspacetrust.TrustedByGrandfathered, inv); err != nil {
			return fmt.Errorf("failed to grandfather %s: %w", inv.Path, err)
		}
	}
	if len(seen) > 0 {
		fmt.Printf("  trusted %d existing session folder(s) with their current Claude config\n", len(seen))
	}
	return nil
}

// AgentSkillMarker is the sentence create_skill_from_handover writes into
// every skill body. Migration 50 uses it to find skills an agent created
// before provenance was recorded.
const AgentSkillMarker = "This skill was created from a session workflow."

// migrateReviewProvenance adds created_by / status / reviewed_by / reviewed_at
// to memories and skills. Rows that predate it are user-approved, except the
// ones an agent demonstrably wrote: memories with source 'agent' and skills
// whose body carries AgentSkillMarker. Those go to the review queue as
// pending, so text an agent saved (possibly steered by a prompt injection) is
// not put into prompts again until the user has looked at it. The backfill
// only runs for a table whose status column this call added, so re-running
// the migration never resets a review.
func migrateReviewProvenance(tx *sql.Tx) error {
	columns := []struct{ name, definition string }{
		{"created_by", "TEXT NOT NULL DEFAULT 'user'"},
		{"status", "TEXT NOT NULL DEFAULT 'approved'"},
		{"reviewed_by", "TEXT"},
		{"reviewed_at", "TIMESTAMP"},
	}
	for _, table := range []string{"memories", "skills"} {
		addedStatus := false
		for _, column := range columns {
			var exists bool
			if err := tx.QueryRow(
				`SELECT COUNT(*) > 0 FROM pragma_table_info(?) WHERE name = ?`, table, column.name,
			).Scan(&exists); err != nil {
				return fmt.Errorf("failed to check %s.%s: %w", table, column.name, err)
			}
			if exists {
				continue
			}
			if _, err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return fmt.Errorf("failed to add %s.%s: %w", table, column.name, err)
			}
			if column.name == "status" {
				addedStatus = true
			}
		}
		if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_" + table + "_status ON " + table + "(status)"); err != nil {
			return fmt.Errorf("failed to index %s.status: %w", table, err)
		}
		if !addedStatus {
			continue
		}
		var backfill string
		switch table {
		case "memories":
			backfill = `UPDATE memories
				SET created_by = 'agent:' || COALESCE(NULLIF(session_id, ''), 'unknown'),
				    status = 'pending'
				WHERE source = 'agent'`
		case "skills":
			backfill = `UPDATE skills
				SET created_by = 'agent:unknown', status = 'pending'
				WHERE COALESCE(path, '') = '' AND body LIKE '%` + AgentSkillMarker + `%'`
		}
		if _, err := tx.Exec(backfill); err != nil {
			return fmt.Errorf("failed to backfill %s provenance: %w", table, err)
		}
	}
	return nil
}

// orphanRowsDeleted counts the rows removed by migrateRemoveSessionOrphans in
// this process, so Initialize can vacuum afterwards (VACUUM cannot run inside
// the migration transaction).
var orphanRowsDeleted int64

// sessionChildTables lists, in delete order, the tables whose rows belong to
// an agent session and are removed with it. foreign_keys used to be ON for
// only one pooled connection, so ON DELETE CASCADE was skipped on the others.
var sessionChildTables = []struct{ table, column string }{
	{"agent_model_switches", "session_id"},
	{"agent_messages", "session_id"},
	{"background_agents", "parent_session_id"},
	{"session_connectors", "session_id"},
	{"agent_handovers", "source_session_id"},
	{"terminal_sessions", "agent_session_id"},
	{"session_digests", "session_id"},
	{"session_tags", "session_id"},
}

// migrateRemoveSessionOrphans deletes rows whose agent session no longer
// exists and drops agent_messages indexes that other indexes already cover:
// idx_agent_messages_session(session_id, sequence) is a prefix of
// idx_agent_messages_sequence and idx_agent_messages_unique_seq, and nothing
// filters or orders agent_messages by timestamp alone.
func migrateRemoveSessionOrphans(tx *sql.Tx) error {
	deleted := func(table string, res sql.Result) {
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			orphanRowsDeleted += n
			fmt.Printf("  removed %d orphaned %s rows\n", n, table)
		}
	}

	// terminal_history hangs off terminal_sessions; clear it first so the
	// count does not depend on whether the cascade fires.
	res, err := tx.Exec(`DELETE FROM terminal_history WHERE terminal_session_id NOT IN (
		SELECT t.id FROM terminal_sessions t JOIN agent_sessions s ON s.id = t.agent_session_id)`)
	if err != nil {
		return fmt.Errorf("failed to delete orphaned terminal_history rows: %w", err)
	}
	deleted("terminal_history", res)

	for _, child := range sessionChildTables {
		// Tables added by later migrations (session_tags) do not exist yet
		// when this runs on an older database.
		var exists bool
		if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = ?`, child.table).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check table %s: %w", child.table, err)
		}
		if !exists {
			continue
		}
		res, err := tx.Exec(fmt.Sprintf(
			`DELETE FROM %s WHERE NOT EXISTS (SELECT 1 FROM agent_sessions s WHERE s.id = %s.%s)`,
			child.table, child.table, child.column))
		if err != nil {
			return fmt.Errorf("failed to delete orphaned %s rows: %w", child.table, err)
		}
		deleted(child.table, res)
	}
	for _, idx := range []string{"idx_agent_messages_session", "idx_agent_messages_timestamp"} {
		if _, err := tx.Exec("DROP INDEX IF EXISTS " + idx); err != nil {
			return fmt.Errorf("failed to drop %s: %w", idx, err)
		}
	}
	return nil
}

// n0OAuthDescription is the connector catalog text for n0 once it moved to OAuth.
const n0OAuthDescription = "Build, commit, deploy and debug your n0 apps from any agent session. Connects over MCP with OAuth — no API keys."

// migrateN0OAuth creates the tables that back n0 OAuth connections and flips
// the n0 catalog entry to auth_type "oauth". Idempotent: every statement is
// IF NOT EXISTS / an UPDATE, so re-running it is harmless.
func migrateN0OAuth(tx *sql.Tx) error {
	stmts := []string{
		// One dynamic client registration per n0 host and redirect target.
		// n0 rate-limits registration, so the client_id is reused for every
		// later authorization against that host.
		`CREATE TABLE IF NOT EXISTS n0_oauth_clients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host TEXT NOT NULL,
			redirect_key TEXT NOT NULL,
			redirect_uri TEXT NOT NULL,
			client_id TEXT NOT NULL,
			issuer TEXT NOT NULL,
			resource TEXT NOT NULL,
			authorization_endpoint TEXT NOT NULL,
			token_endpoint TEXT NOT NULL,
			revocation_endpoint TEXT NOT NULL DEFAULT '',
			registration_endpoint TEXT NOT NULL DEFAULT '',
			scopes_supported TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			UNIQUE(host, redirect_key)
		)`,
		// One row per authorized n0 connection (n0 binds a connection to one app).
		// access_token / refresh_token are encrypted with the connector key.
		`CREATE TABLE IF NOT EXISTS n0_connections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host TEXT NOT NULL,
			issuer TEXT NOT NULL,
			resource TEXT NOT NULL,
			client_id TEXT NOT NULL,
			redirect_uri TEXT NOT NULL,
			token_endpoint TEXT NOT NULL,
			revocation_endpoint TEXT NOT NULL DEFAULT '',
			access_token TEXT NOT NULL DEFAULT '',
			refresh_token TEXT NOT NULL DEFAULT '',
			access_expires_at INTEGER NOT NULL DEFAULT 0,
			scopes TEXT NOT NULL DEFAULT '',
			workspace_name TEXT NOT NULL DEFAULT '',
			app_subdomain TEXT NOT NULL DEFAULT '',
			app_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'needs_reauth', 'revoked')),
			status_message TEXT NOT NULL DEFAULT '',
			refresh_lease_owner TEXT NOT NULL DEFAULT '',
			refresh_lease_until INTEGER NOT NULL DEFAULT 0,
			last_used_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_n0_connections_status ON n0_connections(status)`,
		// Which n0 connection (app) a session uses.
		`CREATE TABLE IF NOT EXISTS n0_session_bindings (
			session_id TEXT PRIMARY KEY,
			connection_id INTEGER NOT NULL REFERENCES n0_connections(id) ON DELETE CASCADE,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_n0_session_bindings_connection ON n0_session_bindings(connection_id)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("n0 oauth migration: %w", err)
		}
	}
	// auth_type_check only allows api_key / oauth2 / webhook.
	if _, err := tx.Exec(`UPDATE connector_definitions SET auth_type = 'oauth2', description = ?, updated_at = CURRENT_TIMESTAMP WHERE slug = 'n0'`, n0OAuthDescription); err != nil {
		return fmt.Errorf("failed to switch n0 connector to oauth2: %w", err)
	}
	return nil
}

// runMigrations runs all pending database migrations
func runMigrations(db *sql.DB) error {
	// Create migrations tracking table if it doesn't exist
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			description TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Get list of applied migrations
	appliedMigrations := make(map[int]bool)
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("failed to query applied migrations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("failed to scan migration version: %w", err)
		}
		appliedMigrations[version] = true
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating migration rows: %w", err)
	}

	// NOTE: Migrations are run in slice order (not sorted by version) because some
	// migrations have cross-dependencies (e.g., migration 5 depends on migration 7's
	// schema changes). The slice order preserves these dependency requirements.
	// New migrations should always be appended at the end with incrementing version numbers.

	// Run pending migrations in order
	for _, migration := range migrations {
		if appliedMigrations[migration.Version] {
			continue // Migration already applied
		}

		// Start transaction
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to start transaction for migration %d: %w", migration.Version, err)
		}

		// Run migration
		if err := migration.Up(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to run migration %d (%s): %w", migration.Version, migration.Description, err)
		}

		// Record migration as applied
		_, err = tx.Exec(
			"INSERT INTO schema_migrations (version, description, applied_at) VALUES (?, ?, ?)",
			migration.Version,
			migration.Description,
			time.Now(),
		)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %d: %w", migration.Version, err)
		}

		// Commit transaction
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", migration.Version, err)
		}

		fmt.Printf("✓ Applied migration %d: %s\n", migration.Version, migration.Description)
	}

	return nil
}

// GetAppliedMigrations returns a list of applied migration versions
func GetAppliedMigrations(db *sql.DB) ([]int, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version ASC")
	if err != nil {
		return nil, fmt.Errorf("failed to query applied migrations: %w", err)
	}
	defer rows.Close()

	var versions []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("failed to scan migration version: %w", err)
		}
		versions = append(versions, version)
	}

	return versions, rows.Err()
}

// migrateSkillSourceHash adds skills.source_hash: the content hash of the
// SKILL.md file a skill was last synced from. Filesystem discovery uses it to
// tell an unchanged file (keep the review decision) from an edited one (back
// to review). Existing rows start empty; the first discovery adopts the hash
// when the file still matches the approved content (SyncDiscoveredSkill).
func migrateSkillSourceHash(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('skills') WHERE name = 'source_hash'`).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check skills.source_hash: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE skills ADD COLUMN source_hash TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("failed to add skills.source_hash: %w", err)
	}
	return nil
}
