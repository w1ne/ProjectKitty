package memory

import (
	"path/filepath"
	"testing"
)

func TestStorePersistsFacts(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	sessionID, err := store.StartSession("task", dir)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	if err := store.RecordSessionEvent(sessionID, "plan", "demo"); err != nil {
		t.Fatalf("record session event: %v", err)
	}

	if err := store.SaveFact(Fact{Category: "test", Summary: "fact"}); err != nil {
		t.Fatalf("save fact: %v", err)
	}

	facts, err := store.RecentFacts(10)
	if err != nil {
		t.Fatalf("recent facts: %v", err)
	}
	if len(facts) != 1 || facts[0].Summary != "fact" {
		t.Fatalf("unexpected facts: %#v", facts)
	}

	if _, err := filepath.Abs(filepath.Join(dir, ".projectkitty", "sessions", sessionID+".jsonl")); err != nil {
		t.Fatalf("expected session log path to be valid: %v", err)
	}
}
