package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w1ne/projectkitty/internal/agent"
	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/memory"
	"github.com/w1ne/projectkitty/internal/runtime"
)

func TestRunPlainExecutesAgentLoop(t *testing.T) {
	dir := t.TempDir()

	writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/plain\n\ngo 1.24.0\n")
	writeFixture(t, filepath.Join(dir, "internal", "auth", "middleware.go"), "package auth\n\nfunc AuthMiddleware() {\n\tvalidateSession()\n}\n")
	writeFixture(t, filepath.Join(dir, "main_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestSmoke(t *testing.T) {}\n")

	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	app := agent.New(
		agent.NewPlanner(),
		intelligence.New(),
		runtime.New(runtime.Policy{
			ApprovalMode:    "test",
			AllowedCommands: []string{"go test ./...", "git status --short"},
		}),
		store,
	)

	var out bytes.Buffer
	err = runPlain(context.Background(), &out, app, agent.RunInput{
		Task:      "inspect auth middleware and validate",
		Workspace: dir,
	})
	if err != nil {
		t.Fatalf("runPlain: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "[observed] Search results:") {
		t.Fatalf("expected search output, got:\n%s", text)
	}
	if !strings.Contains(text, "[observed] Outline results:") {
		t.Fatalf("expected outline output, got:\n%s", text)
	}
	if !strings.Contains(text, "[observed] Focused symbol: Read symbol AuthMiddleware") {
		t.Fatalf("expected focused symbol output, got:\n%s", text)
	}
	if !strings.Contains(text, "[finished] Loop finished") {
		t.Fatalf("expected finished output, got:\n%s", text)
	}
}

func TestRunPlainSkipsFocusedReadWithoutStrongMatch(t *testing.T) {
	dir := t.TempDir()

	writeFixture(t, filepath.Join(dir, "go.mod"), "module example.com/plain\n\ngo 1.24.0\n")
	writeFixture(t, filepath.Join(dir, "internal", "app", "server.go"), "package app\n\nfunc Serve() {}\n")
	writeFixture(t, filepath.Join(dir, "README.md"), "# generic project\n")

	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	app := agent.New(
		agent.NewPlanner(),
		intelligence.New(),
		runtime.New(runtime.Policy{
			ApprovalMode:    "test",
			AllowedCommands: []string{"go test ./...", "git status --short"},
		}),
		store,
	)

	var out bytes.Buffer
	err = runPlain(context.Background(), &out, app, agent.RunInput{
		Task:      "inspect auth middleware and validate",
		Workspace: dir,
	})
	if err != nil {
		t.Fatalf("runPlain: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "[observed] Outline results: No strong symbol match yet.") {
		t.Fatalf("expected no-match outline output, got:\n%s", text)
	}
	if strings.Contains(text, "[observed] Focused symbol:") {
		t.Fatalf("did not expect focused symbol output, got:\n%s", text)
	}
}

func TestRunPlainFallsBackToGitStatusWithoutGoModule(t *testing.T) {
	dir := t.TempDir()

	writeFixture(t, filepath.Join(dir, "internal", "auth", "middleware.go"), "package auth\n\nfunc AuthMiddleware() {}\n")
	writeFixture(t, filepath.Join(dir, "README.md"), "# generic project\n")

	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	app := agent.New(
		agent.NewPlanner(),
		intelligence.New(),
		runtime.New(runtime.Policy{
			ApprovalMode:    "test",
			AllowedCommands: []string{"go test ./...", "git status --short"},
		}),
		store,
	)

	var out bytes.Buffer
	err = runPlain(context.Background(), &out, app, agent.RunInput{
		Task:      "inspect auth middleware",
		Workspace: dir,
	})
	if err != nil {
		t.Fatalf("runPlain: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "[action] Runtime action: git status --short") {
		t.Fatalf("expected git status fallback, got:\n%s", text)
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
