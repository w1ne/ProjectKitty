package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
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

func TestSplitCommands(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{"go test ./...", []string{"go test ./..."}},
		{"go test ./... && git push", []string{"go test ./...", "git push"}},
		{"echo a || echo b ; echo c", []string{"echo a", "echo b", "echo c"}},
		{"  spaces  &&  trimmed  ", []string{"spaces", "trimmed"}},
	}
	for _, c := range cases {
		got := splitCommands(c.input)
		if len(got) != len(c.expected) {
			t.Errorf("splitCommands(%q): got %v, want %v", c.input, got, c.expected)
			continue
		}
		for i := range got {
			if got[i] != c.expected[i] {
				t.Errorf("splitCommands(%q)[%d]: got %q, want %q", c.input, i, got[i], c.expected[i])
			}
		}
	}
}

func TestPolicyBlocksRedirection(t *testing.T) {
	rt := New(Policy{ApprovalMode: "auto"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:    ToolShell,
		Command: "echo hello > /tmp/out",
	})
	if err == nil || !strings.Contains(err.Error(), "redirection") {
		t.Fatalf("expected redirection error, got: %v", err)
	}
}

func TestPolicyChainedCommandBlocked(t *testing.T) {
	rt := New(Policy{ApprovalMode: "auto"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:    ToolShell,
		Command: "go test ./... && git reset --hard",
	})
	if err == nil || !strings.Contains(err.Error(), "blocked by runtime policy") {
		t.Fatalf("expected policy block on chained destructive command, got: %v", err)
	}
}

// requiresPTY skips the test if a PTY-backed process can't be started
// (e.g. sandboxed CI environments that block fork/exec via pty).
func requiresPTY(t *testing.T) {
	t.Helper()
	cmd := exec.Command("bash", "-lc", "true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pt, err := pty.Start(cmd)
	if err != nil {
		_ = cmd.Wait()
		t.Skipf("PTY unavailable in this environment: %v", err)
	}
	pt.Close()
	_ = cmd.Wait()
}

func TestPTYRunsCommandAndStreams(t *testing.T) {
	requiresPTY(t)
	rt := New(Policy{
		ApprovalMode:      "auto",
		InactivityTimeout: 10 * time.Second,
	})

	var lines []string
	result, err := rt.Execute(context.Background(), Call{
		Tool:      ToolShell,
		Workspace: t.TempDir(),
		Command:   "echo hello && echo world",
		Stream: func(execID, line string) {
			lines = append(lines, line)
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.ExecID == "" {
		t.Fatal("expected non-empty ExecID")
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Output, "hello") || !strings.Contains(result.Output, "world") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 streamed lines, got %d: %v", len(lines), lines)
	}
}

func TestPTYInactivityTimeout(t *testing.T) {
	requiresPTY(t)
	rt := New(Policy{
		ApprovalMode:      "auto",
		InactivityTimeout: 500 * time.Millisecond,
	})
	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolShell,
		Workspace: t.TempDir(),
		Command:   "sleep 30",
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected inactivity timeout error, got: %v", err)
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
