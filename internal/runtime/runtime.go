package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/w1ne/projectkitty/internal/intelligence"
)

type Tool string

const (
	ToolShell      Tool = "shell"
	ToolReadFile   Tool = "read_file"
	ToolReadSymbol Tool = "read_symbol"
	ToolListFiles  Tool = "list_files"
)

// StreamFn receives output lines as they arrive from a running command.
// execID ties each line to the specific execution that produced it.
type StreamFn func(execID string, line string)

// maxOutputBytes caps the buffered output returned to the agent.
// Output beyond this limit is truncated with a marker so the model knows.
const maxOutputBytes = 100 * 1024 // 100 KB

// scannerMaxToken is the maximum single-line size the scanner will accept.
// Default bufio.Scanner is 64 KB which is too small for minified JS and build logs.
const scannerMaxToken = 256 * 1024 // 256 KB

type Policy struct {
	ApprovalMode      string
	AllowedCommands   []string
	AllowDestructive  bool
	InactivityTimeout time.Duration
}

type Call struct {
	Tool      Tool
	Workspace string
	Command   string
	Path      string
	Symbol    string
	Limit     int
	Stream    StreamFn
}

type Result struct {
	Tool      Tool
	ExecID    string
	Summary   string
	Output    string
	ExitCode  int
	Truncated bool
	StartedAt time.Time
	EndedAt   time.Time
}

type Runtime struct {
	policy Policy
}

func New(policy Policy) *Runtime {
	return &Runtime{policy: policy}
}

func (r *Runtime) Execute(ctx context.Context, call Call) (Result, error) {
	switch call.Tool {
	case ToolShell:
		return r.runShell(ctx, call)
	case ToolReadFile:
		return r.readFile(call)
	case ToolReadSymbol:
		return r.readSymbol(call)
	case ToolListFiles:
		return r.listFiles(call)
	default:
		return Result{}, fmt.Errorf("unknown tool: %s", call.Tool)
	}
}

func (r *Runtime) runShell(ctx context.Context, call Call) (Result, error) {
	if err := r.checkPolicy(call.Command); err != nil {
		return Result{}, err
	}

	timeout := r.policy.InactivityTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	cmd := exec.Command("bash", "-lc", call.Command)
	cmd.Dir = call.Workspace
	cmd.Env = sterilizeEnv(os.Environ())
	cmd.Env = append(cmd.Env, "KITTY_SHELL=1", "TERM_PROGRAM=projectkitty")
	// New process group so we can kill all descendants, not just the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	execID := newExecID()
	started := time.Now().UTC()
	pt, err := pty.Start(cmd)
	if err != nil {
		// PTY unavailable (sandboxed or restricted environment) — fall back to
		// buffered exec. We lose interactive terminal emulation but keep the
		// sterilized environment, process group, and streaming.
		return r.runShellBuffered(ctx, call, execID, timeout, started)
	}
	defer pt.Close()

	_ = pty.Setsize(pt, &pty.Winsize{Rows: 40, Cols: 220})
	var buf bytes.Buffer
	done := make(chan error, 1)
	outputArrived := make(chan struct{}, 1)

	go func() {
		scanner := bufio.NewScanner(pt)
		scanner.Buffer(make([]byte, scannerMaxToken), scannerMaxToken)
		for scanner.Scan() {
			line := scanner.Text()
			if buf.Len() < maxOutputBytes {
				buf.WriteString(line + "\n")
			}
			if call.Stream != nil {
				call.Stream(execID, line)
			}
			// Non-blocking signal: inactivity timer resets on each line.
			select {
			case outputArrived <- struct{}{}:
			default:
			}
		}
		done <- scanner.Err()
	}()

	inactivity := time.NewTimer(timeout)
	defer inactivity.Stop()

loop:
	for {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return Result{}, ctx.Err()
		case <-inactivity.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return Result{}, fmt.Errorf("command timed out after %s with no output", timeout)
		case <-done:
			break loop
		case <-outputArrived:
			// Stop before Reset to avoid the race where the timer fires
			// while we're trying to reset it (Go timer docs recommend this).
			inactivity.Stop()
			inactivity = time.NewTimer(timeout)
		}
	}

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	return r.buildResult(call.Command, execID, buf.String(), exitCode, started)
}

// runShellBuffered is used when pty.Start fails (sandboxed environments).
// It preserves sterilized env, process group kill, streaming, and inactivity
// timeout — just without a real PTY attached.
// A fresh cmd is built here because pty.Start may have called cmd.Start()
// internally before failing, leaving the original cmd unusable.
func (r *Runtime) runShellBuffered(
	ctx context.Context,
	call Call,
	execID string,
	timeout time.Duration,
	started time.Time,
) (Result, error) {
	cmd := exec.Command("bash", "-lc", call.Command)
	cmd.Dir = call.Workspace
	cmd.Env = sterilizeEnv(os.Environ())
	cmd.Env = append(cmd.Env, "KITTY_SHELL=1", "TERM_PROGRAM=projectkitty")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	pr, pw, err := os.Pipe()
	if err != nil {
		return Result{}, fmt.Errorf("pipe: %w", err)
	}
	defer pr.Close()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		return Result{}, fmt.Errorf("start: %w", err)
	}
	pw.Close() // parent doesn't write

	var buf bytes.Buffer
	done := make(chan error, 1)
	outputArrived := make(chan struct{}, 1)

	go func() {
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, scannerMaxToken), scannerMaxToken)
		for scanner.Scan() {
			line := scanner.Text()
			if buf.Len() < maxOutputBytes {
				buf.WriteString(line + "\n")
			}
			if call.Stream != nil {
				call.Stream(execID, line)
			}
			select {
			case outputArrived <- struct{}{}:
			default:
			}
		}
		done <- scanner.Err()
	}()

	inactivity := time.NewTimer(timeout)
	defer inactivity.Stop()

loop:
	for {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return Result{}, ctx.Err()
		case <-inactivity.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return Result{}, fmt.Errorf("command timed out after %s with no output", timeout)
		case <-done:
			break loop
		case <-outputArrived:
			inactivity.Stop()
			inactivity = time.NewTimer(timeout)
		}
	}

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	return r.buildResult(call.Command, execID, buf.String(), exitCode, started)
}

// buildResult constructs a Result from raw buffered output, applying ANSI
// stripping, output truncation, and summary generation consistently for both
// PTY and buffered execution paths.
func (r *Runtime) buildResult(command, execID, raw string, exitCode int, started time.Time) (Result, error) {
	output := stripANSI(strings.TrimSpace(raw))
	truncated := false
	if len(output) > maxOutputBytes {
		output = output[:maxOutputBytes] + "\n[output truncated]"
		truncated = true
	}

	summary := fmt.Sprintf("Command `%s` finished with exit code %d.", command, exitCode)
	if output != "" {
		// Include first non-empty line in summary for quick scan.
		for _, line := range strings.Split(output, "\n") {
			if strings.TrimSpace(line) != "" {
				summary += " " + line
				break
			}
		}
	}
	if truncated {
		summary += " [output truncated]"
	}

	return Result{
		Tool:      ToolShell,
		ExecID:    execID,
		Summary:   summary,
		Output:    output,
		ExitCode:  exitCode,
		Truncated: truncated,
		StartedAt: started,
		EndedAt:   time.Now().UTC(),
	}, nil
}

func (r *Runtime) readFile(call Call) (Result, error) {
	path, err := r.safePath(call.Workspace, call.Path)
	if err != nil {
		return Result{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Tool:    ToolReadFile,
		Summary: fmt.Sprintf("Read %d bytes from %s.", len(content), call.Path),
		Output:  string(content),
	}, nil
}

func (r *Runtime) readSymbol(call Call) (Result, error) {
	path, err := r.safePath(call.Workspace, call.Path)
	if err != nil {
		return Result{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	symbol := intelligence.FindSymbol(string(content), call.Path, call.Symbol)
	if symbol == nil {
		return Result{}, fmt.Errorf("symbol %q not found in %s", call.Symbol, call.Path)
	}
	return Result{
		Tool:    ToolReadSymbol,
		Summary: fmt.Sprintf("Read symbol %s from %s.", call.Symbol, call.Path),
		Output:  symbol.Snippet,
	}, nil
}

// safePath resolves a workspace-relative path and rejects path traversal.
func (r *Runtime) safePath(workspace, rel string) (string, error) {
	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("workspace path: %w", err)
	}
	joined := filepath.Join(wsAbs, rel)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if !strings.HasPrefix(abs, wsAbs+string(filepath.Separator)) && abs != wsAbs {
		return "", fmt.Errorf("path traversal rejected: %s is outside workspace", rel)
	}
	return abs, nil
}

func (r *Runtime) listFiles(call Call) (Result, error) {
	limit := call.Limit
	if limit <= 0 {
		limit = 20
	}
	files := make([]string, 0, limit)
	err := filepath.WalkDir(call.Workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if len(files) >= limit {
			return errors.New("limit reached")
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".projectkitty" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(call.Workspace, path)
		if relErr == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil && err.Error() != "limit reached" {
		return Result{}, err
	}
	return Result{
		Tool:    ToolListFiles,
		Summary: fmt.Sprintf("Listed %d files.", len(files)),
		Output:  strings.Join(files, "\n"),
	}, nil
}

// checkPolicy splits the command on shell operators and evaluates each segment.
func (r *Runtime) checkPolicy(command string) error {
	normalized := strings.TrimSpace(command)
	if normalized == "" {
		return errors.New("empty command")
	}
	// Injection check runs on the full command before splitting — catches
	// patterns the model may have absorbed from repository content.
	if err := checkInjection(normalized); err != nil {
		return err
	}
	for _, segment := range splitCommands(normalized) {
		if err := r.checkSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

// checkInjection detects shell injection patterns that suggest the model was
// manipulated by repository content. Mirrors Claude Code's
// isBashSecurityCheckForMisparsing approach.
func checkInjection(command string) error {
	if strings.Contains(command, "$(") {
		return fmt.Errorf("command blocked: command substitution $(...) detected — possible injection")
	}
	if strings.Contains(command, "`") {
		return fmt.Errorf("command blocked: backtick substitution detected — possible injection")
	}
	return nil
}

// checkSegment enforces three layers: redirection detection, destructive
// pattern matching, and allowlist / approval mode.
func (r *Runtime) checkSegment(segment string) error {
	// Layer 1: redirection and pipe-to-shell blocked in all non-yolo modes.
	if hasRedirection(segment) && r.policy.ApprovalMode != "yolo" {
		return fmt.Errorf("command requires approval: output redirection detected in %q", segment)
	}
	// Layer 2: destructive fragments blocked unless explicitly permitted.
	if !r.policy.AllowDestructive && isDestructive(segment) {
		return fmt.Errorf("command blocked by runtime policy: %s", segment)
	}
	// Layer 3: yolo and auto allow anything that passed layers 1–2.
	if r.policy.ApprovalMode == "yolo" || r.policy.ApprovalMode == "auto" {
		return nil
	}
	// manual (and any unknown mode): require explicit allowlist entry.
	for _, allowed := range r.policy.AllowedCommands {
		if strings.TrimSpace(segment) == allowed {
			return nil
		}
	}
	return fmt.Errorf("command requires approval in %s mode: %s", r.policy.ApprovalMode, segment)
}

// isDestructive returns true if the segment matches a known destructive pattern.
// Uses word-boundary matching to avoid false positives (e.g. "format" in "go fmt").
var destructiveRE = regexp.MustCompile(
	`(?i)\b(rm|sudo|dd|mkfs|truncate|shred|wipefs)\b|` +
		`git\s+(reset|clean|push\s+--force)|` +
		`\b(chmod|chown)\b`,
)

func isDestructive(segment string) bool {
	return destructiveRE.MatchString(segment)
}

func hasRedirection(segment string) bool {
	// Catch >, >>, 2>, &>, but not => or -> (common in source code strings).
	return redirectRE.MatchString(segment)
}

var redirectRE = regexp.MustCompile(`[^=\-]>[>]?|^>`)

// splitCommands breaks a shell command string on &&, ||, and ; operators.
func splitCommands(cmd string) []string {
	var segments []string
	var cur strings.Builder
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '&' && i+1 < len(cmd) && cmd[i+1] == '&':
			if s := strings.TrimSpace(cur.String()); s != "" {
				segments = append(segments, s)
			}
			cur.Reset()
			i++
		case c == '|' && i+1 < len(cmd) && cmd[i+1] == '|':
			if s := strings.TrimSpace(cur.String()); s != "" {
				segments = append(segments, s)
			}
			cur.Reset()
			i++
		case c == ';':
			if s := strings.TrimSpace(cur.String()); s != "" {
				segments = append(segments, s)
			}
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		segments = append(segments, s)
	}
	return segments
}

// sterilizeEnv strips agent-internal variables from the subprocess environment.
var internalEnvPrefixes = []string{
	"KITTY_",
	"GEMINI_API_KEY",
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
}

func sterilizeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		blocked := false
		for _, prefix := range internalEnvPrefixes {
			if strings.HasPrefix(e, prefix) {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, e)
		}
	}
	return out
}

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiEscapeRE.ReplaceAllString(s, "")
}

func newExecID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
