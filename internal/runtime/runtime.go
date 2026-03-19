package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	ToolWriteFile  Tool = "write_file"
	ToolEditFile   Tool = "edit_file"
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
	Tool         Tool
	Workspace    string
	Command      string
	Path         string
	Symbol       string
	Limit        int
	Stream       StreamFn
	Content      string // write_file: full file content to write
	OldString    string // edit_file: text to replace
	NewString    string // edit_file: replacement text
	ExpectedHash string // edit_file: optional SHA256 of file before edit (conflict detection)
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
	case ToolWriteFile:
		return r.writeFile(call)
	case ToolEditFile:
		return r.editFile(call)
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

// safePath resolves a workspace-relative or absolute path and rejects anything
// outside the workspace root.
func (r *Runtime) safePath(workspace, target string) (string, error) {
	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("workspace path: %w", err)
	}

	candidate := target
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(wsAbs, candidate)
	}

	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if !strings.HasPrefix(abs, wsAbs+string(filepath.Separator)) && abs != wsAbs {
		return "", fmt.Errorf("path escapes workspace: %s", target)
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

// writeFile writes content atomically to a workspace-relative path.
// Directories are created as needed; trailing newline is ensured; the write
// is staged to a temp file and renamed so readers never see a partial file.
func (r *Runtime) writeFile(call Call) (Result, error) {
	path, err := r.safePath(call.Workspace, call.Path)
	if err != nil {
		return Result{}, err
	}
	content := call.Content
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kitty-write-*")
	if err != nil {
		return Result{}, fmt.Errorf("create temp: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return Result{}, fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return Result{}, fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return Result{}, fmt.Errorf("rename: %w", err)
	}
	return Result{
		Tool:    ToolWriteFile,
		Summary: fmt.Sprintf("Wrote %d bytes to %s.", len(content), call.Path),
		Output:  content,
	}, nil
}

// editFile applies an in-place string replacement using Gemini's 3-tier
// matching strategy: exact → indent-aware → token-flexible regex.
// An optional ExpectedHash guards against concurrent modifications.
func (r *Runtime) editFile(call Call) (Result, error) {
	path, err := r.safePath(call.Workspace, call.Path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	// Optional conflict detection: caller pins the SHA256 it read earlier.
	if call.ExpectedHash != "" {
		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != call.ExpectedHash {
			return Result{}, fmt.Errorf("edit conflict: %s modified since last read", call.Path)
		}
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	updated, tier, ok := applyEdit(content, call.OldString, call.NewString)
	if !ok {
		return Result{}, fmt.Errorf("edit failed: old_string not found in %s (tried 3 tiers)", call.Path)
	}
	// Preserve trailing newline of the original file.
	if strings.HasSuffix(content, "\n") && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kitty-edit-*")
	if err != nil {
		return Result{}, fmt.Errorf("create temp: %w", err)
	}
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return Result{}, fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return Result{}, fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return Result{}, fmt.Errorf("rename: %w", err)
	}
	return Result{
		Tool:    ToolEditFile,
		Summary: fmt.Sprintf("Edited %s (tier-%d match, %d→%d bytes).", call.Path, tier, len(content), len(updated)),
		Output:  updated,
	}, nil
}

// applyEdit tries 3 tiers in order and returns (result, tier, ok).
func applyEdit(content, oldStr, newStr string) (string, int, bool) {
	if result, ok := exactReplace(content, oldStr, newStr); ok {
		return result, 1, true
	}
	if result, ok := indentReplace(content, oldStr, newStr); ok {
		return result, 2, true
	}
	if result, ok := tokenReplace(content, oldStr, newStr); ok {
		return result, 3, true
	}
	return content, 0, false
}

// exactReplace requires exactly one occurrence (CRLF already normalised by caller).
func exactReplace(content, oldStr, newStr string) (string, bool) {
	if strings.Count(content, oldStr) == 1 {
		return strings.Replace(content, oldStr, newStr, 1), true
	}
	return content, false
}

// indentReplace strips leading whitespace from each line of oldStr, scans for
// a matching block in content, then re-indents newStr to match the actual
// indentation found in the file.
func indentReplace(content, oldStr, newStr string) (string, bool) {
	oldLines := strings.Split(oldStr, "\n")
	if len(oldLines) == 0 {
		return content, false
	}
	stripped := make([]string, len(oldLines))
	for i, l := range oldLines {
		stripped[i] = strings.TrimLeft(l, " \t")
	}
	contentLines := strings.Split(content, "\n")
	for i := 0; i <= len(contentLines)-len(oldLines); i++ {
		match := true
		for j, sl := range stripped {
			if strings.TrimLeft(contentLines[i+j], " \t") != sl {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		actualIndent := leadingWS(contentLines[i])
		origIndent := leadingWS(oldLines[0])
		newLines := strings.Split(newStr, "\n")
		for k, nl := range newLines {
			if strings.HasPrefix(nl, origIndent) {
				newLines[k] = actualIndent + nl[len(origIndent):]
			} else if nl != "" {
				newLines[k] = actualIndent + strings.TrimLeft(nl, " \t")
			}
		}
		var out []string
		out = append(out, contentLines[:i]...)
		out = append(out, newLines...)
		out = append(out, contentLines[i+len(oldLines):]...)
		return strings.Join(out, "\n"), true
	}
	return content, false
}

func leadingWS(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

// tokenReplace tokenises oldStr on punctuation/whitespace, joins tokens with
// \s* and matches against content. Mirrors Gemini CLI's fuzzy edit fallback.
var tokenSplitRE = regexp.MustCompile(`[\s()\[\]{}<>:=,;]+`)

func tokenReplace(content, oldStr, newStr string) (string, bool) {
	tokens := tokenSplitRE.Split(strings.TrimSpace(oldStr), -1)
	var parts []string
	for _, t := range tokens {
		if t != "" {
			parts = append(parts, regexp.QuoteMeta(t))
		}
	}
	if len(parts) == 0 {
		return content, false
	}
	re, err := regexp.Compile(strings.Join(parts, `[\s()\[\]{}<>:=,;]*`))
	if err != nil {
		return content, false
	}
	loc := re.FindStringIndex(content)
	if loc == nil {
		return content, false
	}
	return content[:loc[0]] + newStr + content[loc[1]:], true
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

func resolveWorkspacePath(workspace, target string) (string, error) {
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}

	candidate := target
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workspaceAbs, candidate)
	}

	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", target, err)
	}

	rel, err := filepath.Rel(workspaceAbs, candidateAbs)
	if err != nil {
		return "", fmt.Errorf("check path %q: %w", target, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", target)
	}

	return candidateAbs, nil
}
