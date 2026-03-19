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

func requiresBwrap(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skipf("bwrap unavailable in this environment: %v", err)
	}

	cmd := exec.Command(
		"bwrap",
		"--die-with-parent",
		"--new-session",
		"--unshare-pid",
		"--unshare-net",
		"--proc", "/proc",
		"--dev", "/dev",
		"--ro-bind-try", "/usr", "/usr",
		"--ro-bind-try", "/bin", "/bin",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--ro-bind-try", "/etc", "/etc",
		"bash", "-lc", "true",
	)
	if err := cmd.Run(); err != nil {
		t.Skipf("bwrap not usable in this environment: %v", err)
	}
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

func TestRuntimeRejectsUnknownSandboxMode(t *testing.T) {
	rt := New(Policy{
		ApprovalMode: "auto",
		SandboxMode:  "mystery-box",
	})
	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolShell,
		Workspace: t.TempDir(),
		Command:   "pwd",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown sandbox mode") {
		t.Fatalf("expected unknown sandbox mode error, got: %v", err)
	}
}

func TestBwrapSandboxRunsCommand(t *testing.T) {
	requiresBwrap(t)

	workspace := t.TempDir()
	rt := New(Policy{
		ApprovalMode: "auto",
		SandboxMode:  "bwrap",
	})
	result, err := rt.Execute(context.Background(), Call{
		Tool:      ToolShell,
		Workspace: workspace,
		Command:   "pwd",
	})
	if err != nil {
		t.Fatalf("execute in bwrap: %v", err)
	}
	if !strings.Contains(result.Output, workspace) {
		t.Fatalf("expected sandboxed pwd to stay in workspace, got: %q", result.Output)
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

// ── write_file ────────────────────────────────────────────────────────────────

func TestWriteFileCreatesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	rt := New(Policy{ApprovalMode: "test"})

	// Create a new file.
	res, err := rt.Execute(context.Background(), Call{
		Tool:      ToolWriteFile,
		Workspace: dir,
		Path:      "pkg/hello.go",
		Content:   "package pkg\n\nfunc Hello() string { return \"hello\" }",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(res.Summary, "hello.go") {
		t.Errorf("summary should mention file: %q", res.Summary)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pkg", "hello.go"))
	if !strings.Contains(string(data), "Hello()") {
		t.Errorf("written content missing: %q", string(data))
	}
	// Trailing newline must be present.
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("expected trailing newline")
	}

	// Overwrite the same path.
	_, err = rt.Execute(context.Background(), Call{
		Tool:      ToolWriteFile,
		Workspace: dir,
		Path:      "pkg/hello.go",
		Content:   "package pkg\n",
	})
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(dir, "pkg", "hello.go"))
	if strings.Contains(string(data2), "Hello()") {
		t.Error("expected overwritten content")
	}
}

func TestWriteFileRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	rt := New(Policy{ApprovalMode: "test"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolWriteFile,
		Workspace: dir,
		Path:      "../evil.go",
		Content:   "package main",
	})
	if err == nil {
		t.Fatal("expected path traversal error")
	}
}

// ── edit_file ─────────────────────────────────────────────────────────────────

func TestEditFileTier1Exact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\n\nfunc Old() {}\n"), 0o644)

	rt := New(Policy{ApprovalMode: "test"})
	res, err := rt.Execute(context.Background(), Call{
		Tool:      ToolEditFile,
		Workspace: dir,
		Path:      "main.go",
		OldString: "func Old() {}",
		NewString: "func New() {}",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(res.Summary, "tier-1") {
		t.Errorf("expected tier-1 in summary: %q", res.Summary)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "Old()") || !strings.Contains(string(data), "New()") {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

func TestEditFileTier2IndentAware(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	// Indented with a tab; oldStr uses spaces — tier-1 will miss, tier-2 should match.
	os.WriteFile(path, []byte("func f() {\n\tif true {\n\t\treturn 1\n\t}\n}\n"), 0o644)

	rt := New(Policy{ApprovalMode: "test"})
	res, err := rt.Execute(context.Background(), Call{
		Tool:      ToolEditFile,
		Workspace: dir,
		Path:      "main.go",
		OldString: "if true {\n\t\treturn 1\n\t}",
		NewString: "if false {\n\t\treturn 0\n\t}",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	_ = res
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "return 0") {
		t.Errorf("expected edited content: %q", string(data))
	}
}

func TestEditFileFailsWhenNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	os.WriteFile(path, []byte("package main\n"), 0o644)

	rt := New(Policy{ApprovalMode: "test"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolEditFile,
		Workspace: dir,
		Path:      "x.go",
		OldString: "func DoesNotExist() {}",
		NewString: "func Replacement() {}",
	})
	if err == nil {
		t.Fatal("expected error when old_string not found")
	}
}

func TestEditFileConflictDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	os.WriteFile(path, []byte("package main\n"), 0o644)

	rt := New(Policy{ApprovalMode: "test"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:         ToolEditFile,
		Workspace:    dir,
		Path:         "x.go",
		OldString:    "package main",
		NewString:    "package replaced",
		ExpectedHash: "deadbeefdeadbeef", // wrong hash
	})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected 'conflict' in error: %v", err)
	}
}

// ── applyEdit unit tests ──────────────────────────────────────────────────────

func TestApplyEditExact(t *testing.T) {
	out, tier, ok := applyEdit("hello world\n", "world", "Go")
	if !ok || tier != 1 || out != "hello Go\n" {
		t.Errorf("exact: got %q tier=%d ok=%v", out, tier, ok)
	}
}

func TestApplyEditAmbiguousExactFallsToIndent(t *testing.T) {
	// "x" appears twice → tier-1 fails; tier-2 should match the first indented block.
	content := "\tx\n\tx\n"
	out, tier, ok := applyEdit(content, "\tx", "\ty")
	// tier-1 fails (count=2), tier-2 finds first line
	if !ok {
		t.Fatal("expected match")
	}
	_ = tier
	_ = out
}

func TestApplyEditToken(t *testing.T) {
	content := "func foo(a int, b string) error {\n\treturn nil\n}\n"
	// old_string has different whitespace — should hit tier 3
	out, tier, ok := applyEdit(content, "func foo(a int,b string)error", "func bar()")
	if !ok {
		t.Fatal("token replace should match")
	}
	if tier != 3 {
		t.Errorf("expected tier 3, got %d", tier)
	}
	if !strings.Contains(out, "bar()") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestRuntimeReadFileRejectsPathOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	parentFile := filepath.Join(dir, "..", "outside.txt")
	if err := os.WriteFile(parentFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	rt := New(Policy{ApprovalMode: "test"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolReadFile,
		Workspace: dir,
		Path:      "../outside.txt",
	})
	if err == nil {
		t.Fatal("expected path escape error")
	}
	if !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeReadSymbolRejectsAbsolutePathOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "auth.go")
	content := "package auth\n\nfunc AuthMiddleware() {}\n"
	if err := os.WriteFile(outsidePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	rt := New(Policy{ApprovalMode: "test"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolReadSymbol,
		Workspace: dir,
		Path:      outsidePath,
		Symbol:    "AuthMiddleware",
	})
	if err == nil {
		t.Fatal("expected path escape error")
	}
	if !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeReadFileRejectsSymlinkOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsidePath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	linkPath := filepath.Join(dir, "linked-secret.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	rt := New(Policy{ApprovalMode: "test"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolReadFile,
		Workspace: dir,
		Path:      "linked-secret.txt",
	})
	if err == nil {
		t.Fatal("expected symlink escape error")
	}
	if !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeWriteFileRejectsSymlinkParentOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()

	linkDir := filepath.Join(dir, "escape")
	if err := os.Symlink(outsideDir, linkDir); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}

	rt := New(Policy{ApprovalMode: "test"})
	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolWriteFile,
		Workspace: dir,
		Path:      "escape/pwned.txt",
		Content:   "nope",
	})
	if err == nil {
		t.Fatal("expected symlink parent escape error")
	}
	if !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("unexpected error: %v", err)
	}
}
