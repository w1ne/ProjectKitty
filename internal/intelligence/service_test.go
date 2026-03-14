package intelligence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func TestScan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		task           string
		files          map[string]string
		wantFirstFile  string
		wantSymbol     string
		wantSymbolPath string
		wantSnippet    string
		wantNoMatch    bool
	}{
		{
			name: "finds source symbol and outline",
			task: "inspect auth middleware and validate",
			files: map[string]string{
				"go.mod":                      "module example.com/test\n\ngo 1.24.0\n",
				"internal/auth/middleware.go": "package auth\n\nfunc AuthMiddleware() {}\n\ntype SessionManager struct{}\n\nfunc (s *SessionManager) Validate() {}\n",
				"internal/http/router.go":     "package http\n\nfunc RegisterRoutes() {}\n",
			},
			wantFirstFile:  "internal/auth/middleware.go",
			wantSymbol:     "AuthMiddleware",
			wantSymbolPath: "internal/auth/middleware.go",
			wantSnippet:    "func AuthMiddleware() {}",
		},
		{
			name: "ignores stopword noise and prefers source over docs tests",
			task: "inspect auth middleware and validate",
			files: map[string]string{
				"go.mod":                       "module example.com/test\n\ngo 1.24.0\n",
				"internal/auth/middleware.go":  "package auth\n\nfunc AuthMiddleware() {\n\tvalidateSession()\n}\n",
				"internal/agent/planner.go":    "package agent\n\nfunc Plan() {\n\t// and then validate the repository state\n}\n",
				"docs/notes.md":                "auth middleware and validation notes\n",
				"internal/agent/agent_test.go": "package agent\n\nfunc TestAgent() {\n\t// auth middleware and validation test\n}\n",
			},
			wantFirstFile:  "internal/auth/middleware.go",
			wantSymbol:     "AuthMiddleware",
			wantSymbolPath: "internal/auth/middleware.go",
		},
		{
			name: "returns no strong match for unrelated repo",
			task: "inspect auth middleware",
			files: map[string]string{
				"go.mod":                "module example.com/test\n\ngo 1.24.0\n",
				"internal/app/main.go":  "package app\n\nfunc Boot() {}\n",
				"internal/app/http.go":  "package app\n\nfunc ServeHTTP() {}\n",
				"internal/app/store.go": "package app\n\ntype Store struct{}\n",
				"README.md":             "# project\n",
			},
			wantFirstFile: "go.mod",
			wantNoMatch:   true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFiles(t, dir, tc.files)

			snapshot, err := New().Scan(context.Background(), Request{
				Task:      tc.task,
				Workspace: dir,
			})
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(snapshot.CandidateFiles) == 0 {
				t.Fatal("expected candidate files")
			}
			if snapshot.CandidateFiles[0] != tc.wantFirstFile {
				t.Fatalf("expected first candidate %q, got %#v", tc.wantFirstFile, snapshot.CandidateFiles)
			}

			if tc.wantNoMatch {
				if snapshot.FocusedSymbol != nil {
					t.Fatalf("expected no focused symbol, got %#v", snapshot.FocusedSymbol)
				}
				if !strings.Contains(snapshot.Summary, "No strong symbol match yet") {
					t.Fatalf("unexpected summary: %q", snapshot.Summary)
				}
				return
			}

			if snapshot.FocusedSymbol == nil {
				t.Fatal("expected focused symbol")
			}
			if snapshot.FocusedSymbol.Name != tc.wantSymbol || snapshot.FocusedSymbol.Path != tc.wantSymbolPath {
				t.Fatalf("unexpected focused symbol: %#v", snapshot.FocusedSymbol)
			}
			if tc.wantSnippet != "" && !strings.Contains(snapshot.FocusedSymbol.Snippet, tc.wantSnippet) {
				t.Fatalf("unexpected focused snippet: %q", snapshot.FocusedSymbol.Snippet)
			}
			if !slices.Contains(snapshot.Symbols[tc.wantSymbolPath], tc.wantSymbol) {
				t.Fatalf("expected symbol list to contain %q, got %#v", tc.wantSymbol, snapshot.Symbols[tc.wantSymbolPath])
			}
		})
	}
}

func TestSearch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":                           "module example.com/test\n\ngo 1.24.0\n",
		"internal/auth/middleware.go":      "package auth\n\nfunc AuthMiddleware() {}\n",
		"docs/auth.md":                     "auth middleware implementation notes\n",
		"internal/auth/middleware_test.go": "package auth\n\nfunc TestAuthMiddleware() {}\n",
	})

	result, err := New().Search(context.Background(), Request{
		Task:      "auth middleware",
		Workspace: dir,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !result.HasGoModule {
		t.Fatal("expected Go module to be detected")
	}
	if result.Provider == "" {
		t.Fatal("expected search provider to be reported")
	}
	if len(result.Passes) == 0 {
		t.Fatal("expected search passes to be recorded")
	}
	if len(result.CandidateFiles) < 3 {
		t.Fatalf("expected ranked candidates, got %#v", result.CandidateFiles)
	}
	if result.CandidateFiles[0] != "internal/auth/middleware.go" {
		t.Fatalf("expected source file first, got %#v", result.CandidateFiles)
	}
}

func TestSearchUsesMultiplePasses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":                    "module example.com/test\n\ngo 1.24.0\n",
		"internal/agent/planner.go": "package agent\n\nfunc chooseValidationCommand() string { return \"go test ./...\" }\n",
		"internal/agent/agent.go":   "package agent\n\nfunc runValidation() string { return chooseValidationCommand() }\n",
		"cmd/projectkitty/main.go":  "package main\n\nfunc main() { _ = \"validation\" }\n",
	})

	result, err := New().Search(context.Background(), Request{
		Task:      "inspect planner validation command",
		Workspace: dir,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Passes) < 2 {
		t.Fatalf("expected multiple search passes, got %#v", result.Passes)
	}
	if result.Passes[1].Name != "refine_structural" {
		t.Fatalf("unexpected second pass: %#v", result.Passes[1])
	}
}

func TestScanWithGitRespectsIgnoreFiles(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".gitignore":                  ".projectkitty/\nnode_modules/\n",
		"go.mod":                      "module example.com/test\n\ngo 1.24.0\n",
		"internal/auth/middleware.go": "package auth\n\nfunc AuthMiddleware() {}\n",
		".projectkitty/session.md":    "auth middleware notes\n",
		"node_modules/auth/index.js":  "export function authMiddleware() {}\n",
	})

	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	scored, hasGoModule, used, err := New().scanWithGit(context.Background(), dir, []string{"auth", "middleware"})
	if err != nil {
		t.Fatalf("scanWithGit: %v", err)
	}
	if !used {
		t.Fatal("expected git scan to be used")
	}
	if !hasGoModule {
		t.Fatal("expected go.mod detection")
	}

	paths := make([]string, 0, len(scored))
	for _, candidate := range scored {
		paths = append(paths, candidate.Path)
	}
	if !slices.Contains(paths, "internal/auth/middleware.go") {
		t.Fatalf("expected source candidate, got %#v", paths)
	}
	if slices.Contains(paths, ".projectkitty/session.md") {
		t.Fatalf("expected ignored internal state to be excluded, got %#v", paths)
	}
	if slices.Contains(paths, "node_modules/auth/index.js") {
		t.Fatalf("expected ignored dependency file to be excluded, got %#v", paths)
	}
}

func TestSearchReportsProviderInSummary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":                "module example.com/test\n\ngo 1.24.0\n",
		"internal/auth/main.go": "package auth\n\nfunc AuthMiddleware() {}\n",
		"internal/auth/http.go": "package auth\n\nfunc ServeHTTP() {}\n",
		"docs/notes.md":         "auth middleware docs\n",
	})

	result, err := New().Search(context.Background(), Request{
		Task:      "auth middleware",
		Workspace: dir,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.Provider == "" {
		t.Fatal("expected provider")
	}
	if !strings.Contains(result.Summary, result.Provider) {
		t.Fatalf("expected summary to include provider %q, got %q", result.Provider, result.Summary)
	}
}

func TestOutline(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		task           string
		files          map[string]string
		candidates     []string
		wantSymbol     string
		wantSymbolPath string
		wantNoMatch    bool
		wantMethod     string
	}{
		{
			name: "finds methods and exact symbol",
			task: "auth middleware validate",
			files: map[string]string{
				"internal/auth/middleware.go": "package auth\n\ntype SessionManager struct{}\n\nfunc (s *SessionManager) Validate() {}\n\nfunc AuthMiddleware() {}\n",
				"internal/auth/manager.go":    "package auth\n\ntype AuthManager struct{}\n\nfunc (m *AuthManager) MiddlewareConfig() {}\n",
			},
			candidates:     []string{"internal/auth/middleware.go", "internal/auth/manager.go"},
			wantSymbol:     "AuthMiddleware",
			wantSymbolPath: "internal/auth/middleware.go",
			wantMethod:     "SessionManager.Validate",
		},
		{
			name: "returns no strong symbol for docs only",
			task: "auth middleware",
			files: map[string]string{
				"docs/auth.md": "auth middleware architecture overview\n",
				"README.md":    "auth middleware setup guide\n",
			},
			candidates:  []string{"docs/auth.md", "README.md"},
			wantNoMatch: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFiles(t, dir, tc.files)

			result, err := New().Outline(context.Background(), OutlineRequest{
				Task:      tc.task,
				Workspace: dir,
				Files:     tc.candidates,
			})
			if err != nil {
				t.Fatalf("outline: %v", err)
			}

			if tc.wantNoMatch {
				if result.FocusedSymbol != nil {
					t.Fatalf("expected no focused symbol, got %#v", result.FocusedSymbol)
				}
				if !strings.Contains(result.Summary, "No strong symbol match yet") {
					t.Fatalf("unexpected summary: %q", result.Summary)
				}
				return
			}

			if result.FocusedSymbol == nil {
				t.Fatal("expected focused symbol")
			}
			if result.FocusedSymbol.Name != tc.wantSymbol || result.FocusedSymbol.Path != tc.wantSymbolPath {
				t.Fatalf("unexpected focused symbol: %#v", result.FocusedSymbol)
			}
			if tc.wantMethod != "" && !slices.Contains(result.Symbols[tc.wantSymbolPath], tc.wantMethod) {
				t.Fatalf("expected method %q in outline, got %#v", tc.wantMethod, result.Symbols[tc.wantSymbolPath])
			}
		})
	}
}

func TestOutlineUsesTreeSitterForJavaScript(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"src/auth.js": "class AuthService {\n  middleware() {}\n}\n\nfunction createAuthMiddleware() {}\n",
	})

	result, err := New().Outline(context.Background(), OutlineRequest{
		Task:      "auth middleware",
		Workspace: dir,
		Files:     []string{"src/auth.js"},
	})
	if err != nil {
		t.Fatalf("outline: %v", err)
	}
	if result.FocusedSymbol == nil {
		t.Fatal("expected focused symbol")
	}
	if result.FocusedSymbol.Name != "createAuthMiddleware" {
		t.Fatalf("unexpected focused symbol: %#v", result.FocusedSymbol)
	}
	if !slices.Contains(result.Symbols["src/auth.js"], "AuthService.middleware") {
		t.Fatalf("expected class method in symbol list, got %#v", result.Symbols["src/auth.js"])
	}
}

func TestOutlineUsesTreeSitterForJava(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"src/AuthService.java": "class AuthService {\n  void middleware() {}\n}\n\nclass AuthFactory {\n  static void createAuthMiddleware() {}\n}\n",
	})

	result, err := New().Outline(context.Background(), OutlineRequest{
		Task:      "auth middleware",
		Workspace: dir,
		Files:     []string{"src/AuthService.java"},
	})
	if err != nil {
		t.Fatalf("outline: %v", err)
	}
	if result.FocusedSymbol == nil {
		t.Fatal("expected focused symbol")
	}
	if result.FocusedSymbol.Name != "createAuthMiddleware" {
		t.Fatalf("unexpected focused symbol: %#v", result.FocusedSymbol)
	}
	if !slices.Contains(result.Symbols["src/AuthService.java"], "AuthService") {
		t.Fatalf("expected class symbol in outline, got %#v", result.Symbols["src/AuthService.java"])
	}
}

func TestOutlineFindsRelatedFilesForFocusedSymbol(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":                    "module example.com/test\n\ngo 1.24.0\n",
		"internal/agent/planner.go": "package agent\n\nfunc chooseValidationCommand() string {\n\treturn \"go test ./...\"\n}\n",
		"internal/agent/agent.go":   "package agent\n\nfunc run() string {\n\treturn chooseValidationCommand()\n}\n",
		"cmd/projectkitty/main.go":  "package main\n\nfunc main() {\n\t_ = \"chooseValidationCommand\"\n}\n",
		"docs/notes.md":             "chooseValidationCommand notes\n",
	})

	result, err := New().Outline(context.Background(), OutlineRequest{
		Task:      "inspect planner validation command",
		Workspace: dir,
		Files:     []string{"internal/agent/planner.go", "internal/agent/agent.go", "cmd/projectkitty/main.go", "docs/notes.md"},
	})
	if err != nil {
		t.Fatalf("outline: %v", err)
	}
	if result.FocusedSymbol == nil || result.FocusedSymbol.Name != "chooseValidationCommand" {
		t.Fatalf("unexpected focused symbol: %#v", result.FocusedSymbol)
	}
	if !slices.Contains(result.RelatedFiles, "internal/agent/agent.go") {
		t.Fatalf("expected related file, got %#v", result.RelatedFiles)
	}
	if !strings.Contains(result.Summary, "Related files:") {
		t.Fatalf("expected related files summary, got %q", result.Summary)
	}
}
