package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/memory"
	"github.com/w1ne/projectkitty/internal/runtime"
)

func TestAgentRunPersistsSessionAndFacts(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/projectkittytest\n\ngo 1.24.0\n")
	writeTestFile(t, filepath.Join(dir, "main_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestSmoke(t *testing.T) {}\n")
	writeTestFile(t, filepath.Join(dir, "internal", "auth", "middleware.go"), "package auth\n\nfunc AuthMiddleware() {}\n")

	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	app := New(
		NewPlanner(),
		intelligence.New(),
		runtime.New(runtime.Policy{
			ApprovalMode:    "test",
			AllowedCommands: []string{"go test ./...", "git status --short"},
		}),
		store,
	)

	var events []Event
	for event := range app.Run(context.Background(), RunInput{
		Task:      "inspect auth middleware and validate",
		Workspace: dir,
	}) {
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Fatal("expected event stream")
	}
	if events[len(events)-1].Kind != EventFinished {
		t.Fatalf("expected finished event, got %#v", events[len(events)-1])
	}

	var sawMemory bool
	var sawFocusedRead bool
	var sawSearch bool
	var sawOutline bool
	for _, event := range events {
		if event.Kind == EventSearchObserved && event.Title == "Search results" {
			sawSearch = true
		}
		if event.Kind == EventOutlineObserved && event.Title == "Outline results" {
			sawOutline = true
		}
		if event.Kind == EventSymbolObserved && event.Title == "Focused symbol" {
			sawFocusedRead = true
		}
		if event.Kind == EventMemory {
			sawMemory = true
		}
	}
	if !sawSearch {
		t.Fatal("expected search event")
	}
	if !sawOutline {
		t.Fatal("expected outline event")
	}
	if !sawFocusedRead {
		t.Fatal("expected focused symbol inspection event")
	}
	if !sawMemory {
		t.Fatal("expected durable memory update event")
	}

	facts, err := store.RecentFacts(10)
	if err != nil {
		t.Fatalf("recent facts: %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("expected persisted fact")
	}
	if !strings.Contains(facts[len(facts)-1].Summary, "go test ./...") || !strings.Contains(facts[len(facts)-1].Summary, "Read symbol AuthMiddleware") {
		t.Fatalf("unexpected fact summary: %q", facts[len(facts)-1].Summary)
	}

	sessionDir := filepath.Join(dir, ".projectkitty", "sessions")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("read session dir: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected session metadata and log, got %d files", len(entries))
	}
}

func TestAgentRunSkipsFocusedReadWhenNoStrongMatch(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/projectkittytest\n\ngo 1.24.0\n")
	writeTestFile(t, filepath.Join(dir, "README.md"), "# generic project\n")
	writeTestFile(t, filepath.Join(dir, "internal", "app", "server.go"), "package app\n\nfunc Serve() {}\n")

	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	app := New(
		NewPlanner(),
		intelligence.New(),
		runtime.New(runtime.Policy{
			ApprovalMode:    "test",
			AllowedCommands: []string{"go test ./...", "git status --short"},
		}),
		store,
	)

	var events []Event
	for event := range app.Run(context.Background(), RunInput{
		Task:      "inspect auth middleware and validate",
		Workspace: dir,
	}) {
		events = append(events, event)
	}

	var sawOutline bool
	var sawFocusedRead bool
	for _, event := range events {
		if event.Kind == EventOutlineObserved && event.Title == "Outline results" {
			sawOutline = true
			if !strings.Contains(event.Detail, "No strong symbol match yet") {
				t.Fatalf("unexpected outline detail: %q", event.Detail)
			}
		}
		if event.Kind == EventSymbolObserved && event.Title == "Focused symbol" {
			sawFocusedRead = true
		}
	}
	if !sawOutline {
		t.Fatal("expected outline event")
	}
	if sawFocusedRead {
		t.Fatal("did not expect focused symbol event")
	}
}

func TestAgentRunFallsBackToGitStatusWithoutGoModule(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "README.md"), "# generic project\n")
	writeTestFile(t, filepath.Join(dir, "internal", "auth", "middleware.go"), "package auth\n\nfunc AuthMiddleware() {}\n")

	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	app := New(
		NewPlanner(),
		intelligence.New(),
		runtime.New(runtime.Policy{
			ApprovalMode:    "test",
			AllowedCommands: []string{"go test ./...", "git status --short"},
		}),
		store,
	)

	var sawGitStatus bool
	for event := range app.Run(context.Background(), RunInput{
		Task:      "inspect auth middleware",
		Workspace: dir,
	}) {
		if event.Kind == EventAction && event.Detail == "git status --short" {
			sawGitStatus = true
		}
	}

	if !sawGitStatus {
		t.Fatal("expected git status fallback without go.mod")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
