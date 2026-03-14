package intelligence

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestScanFindsRelevantFilesAndSymbols(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"go.mod":                      "module example.com/test\n\ngo 1.24.0\n",
		"internal/auth/middleware.go": "package auth\n\nfunc AuthMiddleware() {}\n\ntype SessionManager struct{}\n",
		"internal/http/router.go":     "package http\n\nfunc RegisterRoutes() {}\n",
		"README.md":                   "# test repo\n",
	}

	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	service := New()
	snapshot, err := service.Scan(context.Background(), Request{
		Task:      "inspect auth middleware",
		Workspace: dir,
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if !snapshot.HasGoModule {
		t.Fatal("expected Go module to be detected")
	}
	if len(snapshot.CandidateFiles) == 0 {
		t.Fatal("expected at least one candidate file")
	}
	if !slices.Contains(snapshot.CandidateFiles, "internal/auth/middleware.go") {
		t.Fatalf("expected auth middleware candidate, got %#v", snapshot.CandidateFiles)
	}

	symbols := snapshot.Symbols["internal/auth/middleware.go"]
	if !slices.Contains(symbols, "AuthMiddleware") || !slices.Contains(symbols, "SessionManager") {
		t.Fatalf("expected extracted symbols, got %#v", symbols)
	}
}
