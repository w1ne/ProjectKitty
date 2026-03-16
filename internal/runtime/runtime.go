package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/w1ne/projectkitty/internal/intelligence"
)

type Tool string

const (
	ToolShell      Tool = "shell"
	ToolReadFile   Tool = "read_file"
	ToolReadSymbol Tool = "read_symbol"
	ToolListFiles  Tool = "list_files"
)

type Policy struct {
	ApprovalMode     string
	AllowedCommands  []string
	AllowDestructive bool
}

type Call struct {
	Tool      Tool
	Workspace string
	Command   string
	Path      string
	Symbol    string
	Limit     int
}

type Result struct {
	Tool      Tool
	Summary   string
	Output    string
	ExitCode  int
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

	started := time.Now().UTC()
	cmd := exec.CommandContext(ctx, "bash", "-lc", call.Command)
	cmd.Dir = call.Workspace

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	ended := time.Now().UTC()

	output := strings.TrimSpace(stdout.String())
	errOutput := strings.TrimSpace(stderr.String())
	if errOutput != "" {
		if output != "" {
			output += "\n"
		}
		output += errOutput
	}

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, err
		}
	}

	summary := fmt.Sprintf("Command `%s` finished with exit code %d.", call.Command, exitCode)
	if output != "" {
		firstLine := strings.Split(output, "\n")[0]
		summary += " " + firstLine
	}

	return Result{
		Tool:      ToolShell,
		Summary:   summary,
		Output:    output,
		ExitCode:  exitCode,
		StartedAt: started,
		EndedAt:   ended,
	}, nil
}

func (r *Runtime) readFile(call Call) (Result, error) {
	content, err := os.ReadFile(filepath.Join(call.Workspace, call.Path))
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
	content, err := os.ReadFile(filepath.Join(call.Workspace, call.Path))
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

func (r *Runtime) checkPolicy(command string) error {
	normalized := strings.TrimSpace(command)
	if normalized == "" {
		return errors.New("empty command")
	}

	if !r.policy.AllowDestructive {
		blockedFragments := []string{" rm ", " rm-", "sudo ", " git reset", " git clean", "chmod ", "chown "}
		wrapped := " " + normalized
		for _, fragment := range blockedFragments {
			if strings.Contains(wrapped, fragment) {
				return fmt.Errorf("command blocked by runtime policy (%s mode): %s", r.policy.ApprovalMode, normalized)
			}
		}
	}

	for _, allowed := range r.policy.AllowedCommands {
		if normalized == allowed {
			return nil
		}
	}

	return fmt.Errorf("command requires approval in %s mode: %s", r.policy.ApprovalMode, normalized)
}
