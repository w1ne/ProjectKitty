package agent

import (
	"testing"

	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/runtime"
)

func TestPlannerFlow(t *testing.T) {
	planner := NewPlanner()

	first := planner.Next(State{})
	if first.Kind != ActionGatherContext {
		t.Fatalf("expected first action %q, got %q", ActionGatherContext, first.Kind)
	}

	second := planner.Next(State{
		Context: &intelligence.ContextSnapshot{CandidateFiles: []string{"go.mod"}},
	})
	if second.Kind != ActionRunCommand || second.Command != "go test ./..." {
		t.Fatalf("unexpected second decision: %#v", second)
	}

	third := planner.Next(State{
		Context:        &intelligence.ContextSnapshot{CandidateFiles: []string{"go.mod"}},
		LastToolResult: &runtime.Result{Tool: runtime.ToolShell, Summary: "ok"},
	})
	if third.Kind != ActionSaveMemory {
		t.Fatalf("expected save memory, got %q", third.Kind)
	}

	fourth := planner.Next(State{
		Context:        &intelligence.ContextSnapshot{CandidateFiles: []string{"go.mod"}},
		LastToolResult: &runtime.Result{Tool: runtime.ToolShell, Summary: "ok"},
		MemorySaved:    true,
	})
	if fourth.Kind != ActionFinish {
		t.Fatalf("expected finish, got %q", fourth.Kind)
	}
}
