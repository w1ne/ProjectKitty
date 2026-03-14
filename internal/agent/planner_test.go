package agent

import (
	"testing"

	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/runtime"
)

func TestPlannerFlow(t *testing.T) {
	planner := NewPlanner()

	first := planner.Next(State{})
	if first.Kind != ActionSearchRepository {
		t.Fatalf("expected first action %q, got %q", ActionSearchRepository, first.Kind)
	}

	second := planner.Next(State{
		Search: &intelligence.SearchResult{
			CandidateFiles: []string{"go.mod"},
			HasGoModule:    true,
		},
	})
	if second.Kind != ActionOutlineContext {
		t.Fatalf("unexpected second decision: %#v", second)
	}

	third := planner.Next(State{
		Search: &intelligence.SearchResult{
			CandidateFiles: []string{"go.mod"},
			HasGoModule:    true,
		},
		Outline: &intelligence.OutlineResult{
			FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/auth/middleware.go", Name: "AuthMiddleware"},
		},
	})
	if third.Kind != ActionInspectSymbol || third.Symbol != "AuthMiddleware" {
		t.Fatalf("unexpected inspect decision: %#v", third)
	}

	fourth := planner.Next(State{
		Search: &intelligence.SearchResult{
			CandidateFiles: []string{"go.mod"},
			HasGoModule:    true,
		},
		Outline: &intelligence.OutlineResult{
			FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/auth/middleware.go", Name: "AuthMiddleware"},
		},
		SymbolReadResult: &runtime.Result{Tool: runtime.ToolReadSymbol, Summary: "ok"},
	})
	if fourth.Kind != ActionRunCommand || fourth.Command != "go test ./..." {
		t.Fatalf("expected validation command, got %#v", fourth)
	}

	fifth := planner.Next(State{
		Search: &intelligence.SearchResult{
			CandidateFiles: []string{"go.mod"},
			HasGoModule:    true,
		},
		Outline: &intelligence.OutlineResult{
			FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/auth/middleware.go", Name: "AuthMiddleware"},
		},
		SymbolReadResult: &runtime.Result{Tool: runtime.ToolReadSymbol, Summary: "ok"},
		ValidationResult: &runtime.Result{Tool: runtime.ToolShell, Summary: "ok"},
	})
	if fifth.Kind != ActionSaveMemory {
		t.Fatalf("expected save memory, got %q", fifth.Kind)
	}

	sixth := planner.Next(State{
		Search: &intelligence.SearchResult{
			CandidateFiles: []string{"go.mod"},
			HasGoModule:    true,
		},
		Outline: &intelligence.OutlineResult{
			FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/auth/middleware.go", Name: "AuthMiddleware"},
		},
		SymbolReadResult: &runtime.Result{Tool: runtime.ToolReadSymbol, Summary: "ok"},
		ValidationResult: &runtime.Result{Tool: runtime.ToolShell, Summary: "ok"},
		MemorySaved:      true,
	})
	if sixth.Kind != ActionFinish {
		t.Fatalf("expected finish, got %q", sixth.Kind)
	}
}

func TestPlannerSkipsInspectWithoutStrongSymbol(t *testing.T) {
	planner := NewPlanner()

	decision := planner.Next(State{
		Search: &intelligence.SearchResult{
			CandidateFiles: []string{"internal/app/main.go"},
		},
		Outline: &intelligence.OutlineResult{},
	})
	if decision.Kind != ActionRunCommand {
		t.Fatalf("expected validation without symbol inspect, got %#v", decision)
	}
}
