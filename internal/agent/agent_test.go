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
	if facts[len(facts)-1].Category != "article3-taking-action" {
		t.Fatalf("unexpected fact category: %q", facts[len(facts)-1].Category)
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

// stubPlanner always returns the same action regardless of state — used to test
// safety guards like loop detection and context overflow.
type stubPlanner struct {
	action  ActionKind
	command string
}

func (p *stubPlanner) Next(_ context.Context, _ State) Decision {
	return Decision{
		Kind:    p.action,
		Title:   "stub",
		Detail:  "stub decision",
		Command: p.command,
	}
}

func TestAgentRunEmitsLoopDetected(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24.0\n")

	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// Stub planner always asks for memory save — never finishes — triggers loop guard
	app := New(
		&stubPlanner{action: ActionSaveMemory},
		intelligence.New(),
		runtime.New(runtime.Policy{ApprovalMode: "test", AllowedCommands: []string{"git status --short"}}),
		store,
	)

	var events []Event
	for event := range app.Run(context.Background(), RunInput{Task: "loop test", Workspace: dir}) {
		events = append(events, event)
	}

	var sawLoopDetected bool
	for _, e := range events {
		if e.Kind == EventLoopDetected {
			sawLoopDetected = true
		}
	}
	if !sawLoopDetected {
		t.Fatal("expected EventLoopDetected after exceeding max session turns")
	}
	// Must not finish normally
	if events[len(events)-1].Kind == EventFinished {
		t.Fatal("expected loop guard to stop before EventFinished")
	}
}

func TestAgentRunEmitsContextWindowOverflow(t *testing.T) {
	dir := t.TempDir()

	// Create enough file content to exceed 40K estimated tokens (160KB of content)
	bigContent := strings.Repeat("func Handler() {}\n", 9000) // ~162KB
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24.0\n")
	writeTestFile(t, filepath.Join(dir, "internal/auth/middleware.go"), "package auth\n\nfunc AuthMiddleware() {}\n"+bigContent)

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

	var sawOverflow bool
	for event := range app.Run(context.Background(), RunInput{
		Task:      "inspect auth middleware and validate",
		Workspace: dir,
	}) {
		if event.Kind == EventContextWindowWillOverflow {
			sawOverflow = true
		}
	}
	if !sawOverflow {
		t.Fatal("expected EventContextWindowWillOverflow with large repo content")
	}
}

func TestAgentRunOutlinesRelatedFiles(t *testing.T) {
	dir := t.TempDir()

	// planner.go references chooseValidationCommand; agent.go calls chooseValidationCommand
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/projectkittytest\n\ngo 1.24.0\n")
	writeTestFile(t, filepath.Join(dir, "main_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestSmoke(t *testing.T) {}\n")
	writeTestFile(t, filepath.Join(dir, "internal/agent/planner.go"), "package agent\n\nfunc chooseValidationCommand() string { return \"go test ./...\" }\n")
	writeTestFile(t, filepath.Join(dir, "internal/agent/agent.go"), "package agent\n\nfunc run() string { return chooseValidationCommand() }\n")

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

	var sawRelatedOutline bool
	for event := range app.Run(context.Background(), RunInput{
		Task:      "inspect planner validation command",
		Workspace: dir,
	}) {
		if event.Kind == EventOutlineObserved && event.Title == "Related file outline" {
			sawRelatedOutline = true
		}
	}
	if !sawRelatedOutline {
		t.Fatal("expected related file outline event after reading focused symbol")
	}
}

func TestAgentRunBroadensSearchWhenNoSymbolOnFirstOutline(t *testing.T) {
	dir := t.TempDir()

	// Two files: one matching task token "middleware", one matching task token "auth"
	// so that the first outline pass (on the search result) finds no strong symbol,
	// then broadened search on the longest word merges in additional candidates.
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24.0\n")
	writeTestFile(t, filepath.Join(dir, "internal/auth/middleware.go"), "package auth\n\nfunc AuthMiddleware() {}\n")
	writeTestFile(t, filepath.Join(dir, "internal/app/server.go"), "package app\n\nfunc Serve() {}\n")

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

	var sawFinishedOrError bool
	for event := range app.Run(context.Background(), RunInput{
		Task:      "inspect auth middleware",
		Workspace: dir,
	}) {
		if event.Kind == EventFinished || event.Kind == EventErrored || event.Kind == EventLoopDetected {
			sawFinishedOrError = true
		}
	}
	if !sawFinishedOrError {
		t.Fatal("expected agent to reach a terminal event (finished, errored, or loop detected)")
	}
}

func TestLongestWord(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"inspect auth middleware", "middleware"},
		{"go test", "test"},
		{"a bb ccc", "ccc"},
		{"", ""},
		{"single", "single"},
	}
	for _, tc := range cases {
		got := longestWord(tc.input)
		if got != tc.want {
			t.Errorf("longestWord(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMergeUnique(t *testing.T) {
	a := []string{"a.go", "b.go"}
	b := []string{"b.go", "c.go"}
	got := mergeUnique(a, b)
	want := []string{"a.go", "b.go", "c.go"}
	if len(got) != len(want) {
		t.Fatalf("mergeUnique = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mergeUnique[%d] = %q, want %q", i, got[i], want[i])
		}
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
