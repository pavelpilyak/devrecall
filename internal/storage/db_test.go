package storage

import (
	"testing"
)

func TestOpenPathMemory(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath(:memory:) error: %v", err)
	}
	defer db.Close()
}

func TestMigrateCreatesAllTables(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath error: %v", err)
	}
	defer db.Close()

	tables := []string{"identities", "identity_links", "activities", "sync_state", "summaries"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestMigrateCreatesFTSTable(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath error: %v", err)
	}
	defer db.Close()

	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='activities_fts'").Scan(&name)
	if err != nil {
		t.Errorf("FTS table activities_fts not found: %v", err)
	}
}

func TestMigrateCreatesIndexes(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath error: %v", err)
	}
	defer db.Close()

	indexes := []string{
		"idx_activities_source",
		"idx_activities_timestamp",
		"idx_activities_type",
		"idx_activities_identity",
		"idx_summaries_period",
	}
	for _, idx := range indexes {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&name)
		if err != nil {
			t.Errorf("index %q not found: %v", idx, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath error: %v", err)
	}
	defer db.Close()

	// Running migrate again should not fail (all CREATE IF NOT EXISTS)
	if err := db.migrate(); err != nil {
		t.Fatalf("second migrate() should be idempotent: %v", err)
	}
}

func TestInsertAndQueryActivity(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath error: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO activities (source, source_id, type, title, content, timestamp)
		VALUES ('git', 'abc123', 'commit', 'Fix auth bug', 'Fixed token refresh logic', '2026-03-27T10:00:00Z')`)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}

	var title string
	err = db.QueryRow("SELECT title FROM activities WHERE source_id = 'abc123'").Scan(&title)
	if err != nil {
		t.Fatalf("query activity: %v", err)
	}
	if title != "Fix auth bug" {
		t.Errorf("title = %q, want %q", title, "Fix auth bug")
	}
}

func TestFTSIndexSyncsOnInsert(t *testing.T) {
	db, err := OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath error: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO activities (source, source_id, type, title, content, timestamp)
		VALUES ('git', 'abc123', 'commit', 'Fix auth bug', 'Fixed token refresh logic', '2026-03-27T10:00:00Z')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var title string
	err = db.QueryRow("SELECT title FROM activities_fts WHERE activities_fts MATCH 'auth'").Scan(&title)
	if err != nil {
		t.Fatalf("FTS search: %v", err)
	}
	if title != "Fix auth bug" {
		t.Errorf("FTS title = %q, want %q", title, "Fix auth bug")
	}
}
