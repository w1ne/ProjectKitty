package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeBlocksUnknownCommand(t *testing.T) {
	rt := New(Policy{
		ApprovalMode:    "manual",
		AllowedCommands: []string{"pwd"},
	})

	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolShell,
		Workspace: ".",
		Command:   "rm -rf /tmp/whatever",
	})
	if err == nil {
		t.Fatal("expected policy error")
	}
	if !strings.Contains(err.Error(), "blocked by runtime policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeReadsFocusedSymbol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "internal", "auth", "middleware.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "package auth\n\nfunc AuthMiddleware() {\n\tvalidateSession()\n}\n\ntype SessionManager struct{}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rt := New(Policy{ApprovalMode: "test"})
	result, err := rt.Execute(context.Background(), Call{
		Tool:      ToolReadSymbol,
		Workspace: dir,
		Path:      "internal/auth/middleware.go",
		Symbol:    "AuthMiddleware",
	})
	if err != nil {
		t.Fatalf("read symbol: %v", err)
	}
	if !strings.Contains(result.Output, "validateSession") {
		t.Fatalf("unexpected symbol output: %q", result.Output)
	}
}

func TestRuntimeReadsFocusedSymbolFromJavaScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src", "auth.js")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "class AuthService {\n  middleware() {\n    return true\n  }\n}\n\nfunction createAuthMiddleware() {\n  return new AuthService()\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rt := New(Policy{ApprovalMode: "test"})
	result, err := rt.Execute(context.Background(), Call{
		Tool:      ToolReadSymbol,
		Workspace: dir,
		Path:      "src/auth.js",
		Symbol:    "createAuthMiddleware",
	})
	if err != nil {
		t.Fatalf("read symbol: %v", err)
	}
	if !strings.Contains(result.Output, "new AuthService") {
		t.Fatalf("unexpected symbol output: %q", result.Output)
	}
}
