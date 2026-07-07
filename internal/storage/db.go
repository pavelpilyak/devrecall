package storage

import (
	"database/sql"
	"fmt"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pavelpilyak/devrecall/internal/config"
)

func init() {
	sqlite_vec.Auto()
}

// DB wraps the SQLite connection.
type DB struct {
	*sql.DB
}

// Open connects to the SQLite database at the default path, creating it if needed.
func Open() (*DB, error) {
	path, err := config.DBPath()
	if err != nil {
		return nil, err
	}
	return OpenPath(path)
}

// OpenPath connects to the SQLite database at the given path, creating it if needed.
// Use ":memory:" for in-memory databases in tests.
func OpenPath(path string) (*DB, error) {
	dsn := path + "?_journal_mode=WAL&_foreign_keys=on"
	if path == ":memory:" {
		dsn = ":memory:?_foreign_keys=on"
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("cannot open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("cannot connect to database: %w", err)
	}

	store := &DB{db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return store, nil
}

func (db *DB) migrate() error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if _, err := db.Exec(vecSchema); err != nil {
		return fmt.Errorf("vec schema: %w", err)
	}
	// Add last_error column to sync_state for existing databases.
	db.Exec("ALTER TABLE sync_state ADD COLUMN last_error TEXT")
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS identities (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    email      TEXT    NOT NULL UNIQUE,
    is_self    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS identity_links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    identity_id INTEGER NOT NULL REFERENCES identities(id),
    source      TEXT    NOT NULL, -- 'git', 'slack', 'google', 'jira', 'linear'
    source_uid  TEXT    NOT NULL, -- vendor-specific user ID
    UNIQUE(source, source_uid)
);

CREATE TABLE IF NOT EXISTS activities (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source      TEXT    NOT NULL, -- 'git', 'slack', 'calendar', 'jira', 'linear'
    source_id   TEXT    NOT NULL, -- vendor-specific ID (commit SHA, message ts, etc.)
    identity_id INTEGER REFERENCES identities(id),
    type        TEXT    NOT NULL, -- 'commit', 'message', 'meeting', 'ticket', 'review'
    title       TEXT    NOT NULL,
    content     TEXT,
    metadata    TEXT,             -- JSON blob for source-specific data
    timestamp   TEXT    NOT NULL,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source, source_id)
);

CREATE TABLE IF NOT EXISTS sync_state (
    source     TEXT PRIMARY KEY,
    cursor     TEXT,              -- last sync cursor/token per source
    synced_at  TEXT NOT NULL DEFAULT (datetime('now')),
    last_error TEXT               -- last sync error message (NULL = success)
);

CREATE TABLE IF NOT EXISTS summaries (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    period_type    TEXT    NOT NULL, -- 'daily', 'weekly', 'monthly', 'quarterly'
    period_start   TEXT    NOT NULL,
    period_end     TEXT    NOT NULL,
    summary_text   TEXT    NOT NULL,
    highlights     TEXT,             -- JSON: key achievements, collaborators, metrics
    activity_count INTEGER,
    created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Full-text search on activities
CREATE VIRTUAL TABLE IF NOT EXISTS activities_fts USING fts5(
    title, content, content=activities, content_rowid=id
);

-- Triggers to keep FTS index in sync
CREATE TRIGGER IF NOT EXISTS activities_ai AFTER INSERT ON activities BEGIN
    INSERT INTO activities_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;
CREATE TRIGGER IF NOT EXISTS activities_ad AFTER DELETE ON activities BEGIN
    INSERT INTO activities_fts(activities_fts, rowid, title, content) VALUES ('delete', old.id, old.title, old.content);
END;
CREATE TRIGGER IF NOT EXISTS activities_au AFTER UPDATE ON activities BEGIN
    INSERT INTO activities_fts(activities_fts, rowid, title, content) VALUES ('delete', old.id, old.title, old.content);
    INSERT INTO activities_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;

CREATE INDEX IF NOT EXISTS idx_activities_source ON activities(source);
CREATE INDEX IF NOT EXISTS idx_activities_timestamp ON activities(timestamp);
CREATE INDEX IF NOT EXISTS idx_activities_type ON activities(type);
CREATE INDEX IF NOT EXISTS idx_activities_identity ON activities(identity_id);
CREATE INDEX IF NOT EXISTS idx_summaries_period ON summaries(period_type, period_start);

CREATE TABLE IF NOT EXISTS embeddings (
    activity_id INTEGER PRIMARY KEY REFERENCES activities(id) ON DELETE CASCADE,
    model       TEXT    NOT NULL, -- model that produced the embedding (e.g. "all-minilm")
    dimensions  INTEGER NOT NULL,
    vector      BLOB    NOT NULL, -- little-endian float32 array
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Work items: one row per unit of work, linking a ticket to its commits,
-- PRs, and discussions across sources. key is the identity: a ticket key
-- ("PROJ-123") when one exists, otherwise "pr:<source>:<source_id>" for
-- PRs with no ticket reference.
CREATE TABLE IF NOT EXISTS work_items (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    key               TEXT    NOT NULL UNIQUE,
    kind              TEXT    NOT NULL,            -- 'ticket' | 'pr'
    title             TEXT    NOT NULL DEFAULT '', -- ticket summary > PR title > first commit subject
    status            TEXT,                        -- latest known status ('Done', 'In Review', ...)
    status_changed_at TEXT,                        -- timestamp of the transition that set status
    url               TEXT,
    first_seen        TEXT    NOT NULL,            -- min(activity.timestamp) across linked activities
    last_seen         TEXT    NOT NULL,            -- max(activity.timestamp)
    updated_at        TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS activity_work_items (
    activity_id  INTEGER NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    work_item_id INTEGER NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
    link_kind    TEXT    NOT NULL DEFAULT 'issue_key', -- 'issue_key' | 'pr_sha' | 'self'
    PRIMARY KEY (activity_id, work_item_id)
);
CREATE INDEX IF NOT EXISTS idx_awi_work_item ON activity_work_items(work_item_id);
CREATE INDEX IF NOT EXISTS idx_awi_activity  ON activity_work_items(activity_id);

-- LLM-generated per-activity enrichment: one-line factual digest + tags.
-- Row presence marks an activity as enriched (same idempotency pattern as
-- embeddings). model records provenance: provider name, or 'deterministic'
-- (rule-based pre-fill) / 'fallback' (LLM output unusable).
CREATE TABLE IF NOT EXISTS enrichments (
    activity_id INTEGER PRIMARY KEY REFERENCES activities(id) ON DELETE CASCADE,
    digest      TEXT    NOT NULL,
    tags        TEXT    NOT NULL DEFAULT '[]',  -- JSON array of lowercase strings
    entities    TEXT,                           -- optional JSON: {"people":[],"systems":[]}
    model       TEXT    NOT NULL,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
`

// vecSchema creates the vec0 virtual table for KNN search.
// Separated from main schema because vec0 uses non-standard syntax
// that must run after sqlite-vec is loaded.
const vecSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS vec_activities USING vec0(
    activity_id INTEGER PRIMARY KEY,
    embedding FLOAT[384]
);
`
