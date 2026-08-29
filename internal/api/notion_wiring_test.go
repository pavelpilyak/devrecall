package api

import (
	"testing"

	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/storage"
)

// A source that is enabled but never registered as a syncer is the quiet
// failure mode here: the UI shows it connected while nothing ever syncs.
func TestNotionSyncer_RegistrationRules(t *testing.T) {
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	tests := []struct {
		name string
		cfg  config.NotionConfig
		want bool
	}{
		{"enabled with a resolved user", config.NotionConfig{Enabled: true, UserID: "u1", Email: "a@b.c"}, true},
		{"disabled", config.NotionConfig{Enabled: false, UserID: "u1"}, false},
		// Without a user id the collector cannot tell your pages from anyone
		// else's, so registering it would index the whole shared workspace.
		{"enabled but unresolved", config.NotionConfig{Enabled: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Notion: tt.cfg}
			got := notionSyncer(cfg, db, &mockTokenStore{}) != nil
			if got != tt.want {
				t.Errorf("syncer registered = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildAllSyncers_IncludesNotion(t *testing.T) {
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{Notion: config.NotionConfig{Enabled: true, UserID: "u1", Email: "a@b.c"}}
	if _, ok := BuildAllSyncers(cfg, db, &mockTokenStore{})["notion"]; !ok {
		t.Error("notion missing from BuildAllSyncers; the Sync button would skip it")
	}
}
