package agent

import (
	"context"
	"testing"

	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/runtime"
)

func TestPlannerFlow(t *testing.T) {
	planner := NewPlanner()

	first := planner.Next(context.Background(), State{})
	if first.Kind != ActionSearchRepository {
		t.Fatalf("expected first action %q, got %q", ActionSearchRepository, first.Kind)
	}

	second := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{
				CandidateFiles: []string{"go.mod"},
				HasGoModule:    true,
			},
		},
	})
	if second.Kind != ActionOutlineContext {
		t.Fatalf("unexpected second decision: %#v", second)
	}

	third := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{
				CandidateFiles: []string{"go.mod"},
				HasGoModule:    true,
			},
		},
		OutlineTool: &OutlineToolState{
			Result: &intelligence.OutlineResult{
				FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/auth/middleware.go", Name: "AuthMiddleware"},
			},
		},
	})
	if third.Kind != ActionInspectSymbol || third.Symbol != "AuthMiddleware" {
		t.Fatalf("unexpected inspect decision: %#v", third)
	}

	fourth := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{
				CandidateFiles: []string{"go.mod"},
				HasGoModule:    true,
			},
		},
		OutlineTool: &OutlineToolState{
			Result: &intelligence.OutlineResult{
				FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/auth/middleware.go", Name: "AuthMiddleware"},
			},
		},
		ReadSymbolTool: &ReadSymbolToolState{Result: &runtime.Result{Tool: runtime.ToolReadSymbol, Summary: "ok"}},
	})
	if fourth.Kind != ActionRunCommand || fourth.Command != "go test ./..." {
		t.Fatalf("expected validation command, got %#v", fourth)
	}

	fifth := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{
				CandidateFiles: []string{"go.mod"},
				HasGoModule:    true,
			},
		},
		OutlineTool: &OutlineToolState{
			Result: &intelligence.OutlineResult{
				FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/auth/middleware.go", Name: "AuthMiddleware"},
			},
		},
		ReadSymbolTool: &ReadSymbolToolState{Result: &runtime.Result{Tool: runtime.ToolReadSymbol, Summary: "ok"}},
		ValidationTool: &ValidationToolState{Result: &runtime.Result{Tool: runtime.ToolShell, Summary: "ok"}},
	})
	if fifth.Kind != ActionSaveMemory {
		t.Fatalf("expected save memory, got %q", fifth.Kind)
	}

	sixth := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{
				CandidateFiles: []string{"go.mod"},
				HasGoModule:    true,
			},
		},
		OutlineTool: &OutlineToolState{
			Result: &intelligence.OutlineResult{
				FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/auth/middleware.go", Name: "AuthMiddleware"},
			},
		},
		ReadSymbolTool: &ReadSymbolToolState{Result: &runtime.Result{Tool: runtime.ToolReadSymbol, Summary: "ok"}},
		ValidationTool: &ValidationToolState{Result: &runtime.Result{Tool: runtime.ToolShell, Summary: "ok"}},
		MemorySaved:    true,
	})
	if sixth.Kind != ActionFinish {
		t.Fatalf("expected finish, got %q", sixth.Kind)
	}
}

func TestPlannerBroadensWhenNoFocusedSymbol(t *testing.T) {
	planner := NewPlanner()

	// No focused symbol and not yet broadened → should request broaden
	decision := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{Result: &intelligence.SearchResult{
			CandidateFiles: []string{"internal/app/main.go"},
		}},
		OutlineTool:     &OutlineToolState{Result: &intelligence.OutlineResult{}},
		BroadenedSearch: false,
	})
	if decision.Kind != ActionBroadenSearch {
		t.Fatalf("expected broaden_search when no focused symbol, got %#v", decision)
	}
}

func TestPlannerSkipsInspectWithoutStrongSymbolAfterBroadening(t *testing.T) {
	planner := NewPlanner()

	// After broadening with still no focused symbol → skip to validation
	decision := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{Result: &intelligence.SearchResult{
			CandidateFiles: []string{"internal/app/main.go"},
		}},
		OutlineTool:     &OutlineToolState{Result: &intelligence.OutlineResult{}},
		BroadenedSearch: true,
	})
	if decision.Kind != ActionRunCommand {
		t.Fatalf("expected validation without symbol inspect, got %#v", decision)
	}
}

func TestPlannerSkipsBroadenWhenAlreadyBroadened(t *testing.T) {
	planner := NewPlanner()

	// BroadenedSearch already true — should not loop back to broaden
	decision := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{Result: &intelligence.SearchResult{
			CandidateFiles: []string{"a.go"},
			HasGoModule:    true,
		}},
		OutlineTool:     &OutlineToolState{Result: &intelligence.OutlineResult{}},
		BroadenedSearch: true,
	})
	if decision.Kind == ActionBroadenSearch {
		t.Fatal("should not broaden search more than once")
	}
}

func TestPlannerOutlineRelated(t *testing.T) {
	planner := NewPlanner()

	// After reading the focused symbol and having related files, planner should outline them
	decision := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{
				CandidateFiles: []string{"internal/agent/planner.go"},
				HasGoModule:    true,
			},
		},
		OutlineTool: &OutlineToolState{
			Result: &intelligence.OutlineResult{
				FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/agent/planner.go", Name: "chooseValidationCommand"},
				RelatedFiles:  []string{"internal/agent/agent.go", "cmd/projectkitty/main.go"},
			},
		},
		ReadSymbolTool: &ReadSymbolToolState{Result: &runtime.Result{Tool: runtime.ToolReadSymbol, Summary: "ok"}},
	})
	if decision.Kind != ActionOutlineRelated {
		t.Fatalf("expected outline_related after reading focused symbol with related files, got %q", decision.Kind)
	}
	if decision.Detail == "" {
		t.Fatal("expected non-empty detail listing related files")
	}
}

func TestPlannerSkipsOutlineRelatedWhenNoRelatedFiles(t *testing.T) {
	planner := NewPlanner()

	// No related files — planner should jump straight to validation
	decision := planner.Next(context.Background(), State{
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{
				CandidateFiles: []string{"internal/agent/planner.go"},
				HasGoModule:    true,
			},
		},
		OutlineTool: &OutlineToolState{
			Result: &intelligence.OutlineResult{
				FocusedSymbol: &intelligence.SymbolMatch{Path: "internal/agent/planner.go", Name: "Plan"},
				RelatedFiles:  nil,
			},
		},
		ReadSymbolTool: &ReadSymbolToolState{Result: &runtime.Result{Tool: runtime.ToolReadSymbol, Summary: "ok"}},
	})
	if decision.Kind != ActionRunCommand {
		t.Fatalf("expected run_command when no related files, got %q", decision.Kind)
	}
}

func TestPlannerFullFlowWithRelatedFiles(t *testing.T) {
	planner := NewPlanner()

	steps := []struct {
		state    State
		wantKind ActionKind
	}{
		{State{}, ActionSearchRepository},
		{State{SearchTool: &SearchToolState{Result: &intelligence.SearchResult{CandidateFiles: []string{"a.go"}, HasGoModule: true}}},
			ActionOutlineContext},
		{State{
			SearchTool:  &SearchToolState{Result: &intelligence.SearchResult{CandidateFiles: []string{"a.go"}, HasGoModule: true}},
			OutlineTool: &OutlineToolState{Result: &intelligence.OutlineResult{FocusedSymbol: &intelligence.SymbolMatch{Path: "a.go", Name: "Foo"}, RelatedFiles: []string{"b.go"}}},
		}, ActionInspectSymbol},
		{State{
			SearchTool:     &SearchToolState{Result: &intelligence.SearchResult{CandidateFiles: []string{"a.go"}, HasGoModule: true}},
			OutlineTool:    &OutlineToolState{Result: &intelligence.OutlineResult{FocusedSymbol: &intelligence.SymbolMatch{Path: "a.go", Name: "Foo"}, RelatedFiles: []string{"b.go"}}},
			ReadSymbolTool: &ReadSymbolToolState{Result: &runtime.Result{Summary: "ok"}},
		}, ActionOutlineRelated},
		{State{
			SearchTool:         &SearchToolState{Result: &intelligence.SearchResult{CandidateFiles: []string{"a.go"}, HasGoModule: true}},
			OutlineTool:        &OutlineToolState{Result: &intelligence.OutlineResult{FocusedSymbol: &intelligence.SymbolMatch{Path: "a.go", Name: "Foo"}, RelatedFiles: []string{"b.go"}}},
			ReadSymbolTool:     &ReadSymbolToolState{Result: &runtime.Result{Summary: "ok"}},
			RelatedOutlineTool: &OutlineToolState{Result: &intelligence.OutlineResult{}},
		}, ActionRunCommand},
		{State{
			SearchTool:         &SearchToolState{Result: &intelligence.SearchResult{CandidateFiles: []string{"a.go"}, HasGoModule: true}},
			OutlineTool:        &OutlineToolState{Result: &intelligence.OutlineResult{FocusedSymbol: &intelligence.SymbolMatch{Path: "a.go", Name: "Foo"}, RelatedFiles: []string{"b.go"}}},
			ReadSymbolTool:     &ReadSymbolToolState{Result: &runtime.Result{Summary: "ok"}},
			RelatedOutlineTool: &OutlineToolState{Result: &intelligence.OutlineResult{}},
			ValidationTool:     &ValidationToolState{Result: &runtime.Result{Summary: "ok"}},
		}, ActionSaveMemory},
		{State{
			SearchTool:         &SearchToolState{Result: &intelligence.SearchResult{CandidateFiles: []string{"a.go"}, HasGoModule: true}},
			OutlineTool:        &OutlineToolState{Result: &intelligence.OutlineResult{FocusedSymbol: &intelligence.SymbolMatch{Path: "a.go", Name: "Foo"}, RelatedFiles: []string{"b.go"}}},
			ReadSymbolTool:     &ReadSymbolToolState{Result: &runtime.Result{Summary: "ok"}},
			RelatedOutlineTool: &OutlineToolState{Result: &intelligence.OutlineResult{}},
			ValidationTool:     &ValidationToolState{Result: &runtime.Result{Summary: "ok"}},
			MemorySaved:        true,
		}, ActionFinish},
	}

	for i, step := range steps {
		got := planner.Next(context.Background(), step.state)
		if got.Kind != step.wantKind {
			t.Fatalf("step %d: expected %q, got %q (decision: %#v)", i, step.wantKind, got.Kind, got)
		}
	}
}
