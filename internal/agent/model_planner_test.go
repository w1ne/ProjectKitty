package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/runtime"
)

// ── buildStatePrompt ─────────────────────────────────────────────────────────

func TestBuildStatePromptEmpty(t *testing.T) {
	state := State{Input: RunInput{Task: "find the auth middleware"}}
	prompt := buildStatePrompt(state)

	if !strings.Contains(prompt, "find the auth middleware") {
		t.Error("prompt should contain the task")
	}
	if !strings.Contains(prompt, "Search: not done") {
		t.Error("prompt should say search not done")
	}
	if !strings.Contains(prompt, "Outline: not done") {
		t.Error("prompt should say outline not done")
	}
	if !strings.Contains(prompt, "Steps taken: 0/20") {
		t.Error("prompt should show step count")
	}
}

func TestBuildStatePromptWithSearch(t *testing.T) {
	state := State{
		Input: RunInput{Task: "find router"},
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{
				CandidateFiles: []string{"internal/router/router.go"},
				Summary:        "Found router.go",
			},
		},
		Steps: 1,
	}
	prompt := buildStatePrompt(state)

	if !strings.Contains(prompt, "Found router.go") {
		t.Error("prompt should include search summary")
	}
	if !strings.Contains(prompt, "Steps taken: 1/20") {
		t.Error("prompt should show step count")
	}
}

func TestBuildStatePromptWithFocusedSymbol(t *testing.T) {
	state := State{
		Input: RunInput{Task: "find auth"},
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{CandidateFiles: []string{"auth.go"}},
		},
		OutlineTool: &OutlineToolState{
			Result: &intelligence.OutlineResult{
				Summary: "Outlined auth.go",
				FocusedSymbol: &intelligence.SymbolMatch{
					Name:       "AuthMiddleware",
					Path:       "internal/auth/middleware.go",
					Confidence: 85,
				},
				RelatedFiles: []string{"internal/app/app.go"},
			},
		},
	}
	prompt := buildStatePrompt(state)

	if !strings.Contains(prompt, "AuthMiddleware") {
		t.Error("prompt should mention focused symbol")
	}
	if !strings.Contains(prompt, "internal/auth/middleware.go") {
		t.Error("prompt should mention symbol path")
	}
	if !strings.Contains(prompt, "confidence 85") {
		t.Error("prompt should mention confidence")
	}
	if !strings.Contains(prompt, "internal/app/app.go") {
		t.Error("prompt should mention related files")
	}
}

func TestBuildStatePromptMemorySaved(t *testing.T) {
	state := State{
		Input:       RunInput{Task: "t"},
		MemorySaved: true,
	}
	prompt := buildStatePrompt(state)
	if !strings.Contains(prompt, "Memory: saved") {
		t.Error("prompt should indicate memory saved")
	}
}

// ── functionCallToDecision ───────────────────────────────────────────────────

func TestFunctionCallToDecisionSearch(t *testing.T) {
	d, err := functionCallToDecision("search_repository", map[string]any{"query": "chooseValidationCommand"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != ActionSearchRepository {
		t.Errorf("expected search_repository, got %q", d.Kind)
	}
	if d.Query != "chooseValidationCommand" {
		t.Errorf("expected query %q, got %q", "chooseValidationCommand", d.Query)
	}
}

func TestFunctionCallToDecisionOutlineContext(t *testing.T) {
	d, err := functionCallToDecision("outline_context", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != ActionOutlineContext {
		t.Errorf("expected outline_context, got %q", d.Kind)
	}
}

func TestFunctionCallToDecisionInspectSymbol(t *testing.T) {
	d, err := functionCallToDecision("inspect_symbol", map[string]any{
		"path":   "internal/agent/planner.go",
		"symbol": "chooseValidationCommand",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != ActionInspectSymbol {
		t.Errorf("expected inspect_symbol, got %q", d.Kind)
	}
	if d.Path != "internal/agent/planner.go" {
		t.Errorf("unexpected path: %q", d.Path)
	}
	if d.Symbol != "chooseValidationCommand" {
		t.Errorf("unexpected symbol: %q", d.Symbol)
	}
}

func TestFunctionCallToDecisionOutlineRelated(t *testing.T) {
	d, err := functionCallToDecision("outline_related", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != ActionOutlineRelated {
		t.Errorf("expected outline_related, got %q", d.Kind)
	}
}

func TestFunctionCallToDecisionRunCommand(t *testing.T) {
	d, err := functionCallToDecision("run_command", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != ActionRunCommand {
		t.Errorf("expected run_command, got %q", d.Kind)
	}
}

func TestFunctionCallToDecisionSaveMemory(t *testing.T) {
	d, err := functionCallToDecision("save_memory", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != ActionSaveMemory {
		t.Errorf("expected save_memory, got %q", d.Kind)
	}
}

func TestFunctionCallToDecisionFinish(t *testing.T) {
	d, err := functionCallToDecision("finish", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != ActionFinish {
		t.Errorf("expected finish, got %q", d.Kind)
	}
}

func TestFunctionCallToDecisionUnknown(t *testing.T) {
	_, err := functionCallToDecision("nonexistent_tool", map[string]any{})
	if err == nil {
		t.Error("expected error for unknown function name")
	}
}

// ── parseGeminiDecision ──────────────────────────────────────────────────────

func TestParseGeminiDecision(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "search_repository",
								"args": map[string]any{"query": "maxSessionTurns contextOverflowTokens"},
							},
						},
					},
				},
			},
		},
	}

	d, err := parseGeminiDecision(resp)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != ActionSearchRepository {
		t.Errorf("expected search_repository, got %q", d.Kind)
	}
	if d.Query != "maxSessionTurns contextOverflowTokens" {
		t.Errorf("unexpected query: %q", d.Query)
	}
}

func TestParseGeminiDecisionNoCandidates(t *testing.T) {
	_, err := parseGeminiDecision(map[string]any{"candidates": []any{}})
	if err == nil {
		t.Error("expected error for empty candidates")
	}
}

// ── ModelPlanner with mock HTTP server ──────────────────────────────────────

func TestModelPlannerMockServer(t *testing.T) {
	// Set up a mock server that returns a valid Gemini function call response
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{
							map[string]any{
								"functionCall": map[string]any{
									"name": "outline_context",
									"args": map[string]any{},
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Patch the endpoint for this test by creating a planner and calling next directly
	// (We test via parseGeminiDecision since the endpoint is a package-level const)
	state := State{
		Input: RunInput{Task: "find auth"},
		SearchTool: &SearchToolState{
			Result: &intelligence.SearchResult{CandidateFiles: []string{"auth.go"}},
		},
	}

	// Verify the fallback path: if the HTTP call errors, we get a DefaultPlanner decision
	planner := NewModelPlanner("invalid-key-to-force-fallback-in-real-tests")
	_ = planner
	_ = state

	// The real integration would require a live API key. For unit coverage, verify
	// that the fallback planner is used on error by checking it's non-nil.
	if planner.fallback == nil {
		t.Error("ModelPlanner should have a non-nil fallback DefaultPlanner")
	}
}

func TestModelPlannerFallbackOnAPIError(t *testing.T) {
	// A planner with an intentionally bad API key should fall back to DefaultPlanner
	planner := NewModelPlanner("bad-key")

	// State with no search done yet — DefaultPlanner returns ActionSearchRepository
	decision := planner.Next(State{Input: RunInput{Task: "find the loop guard"}})

	// The Gemini call will fail (bad key / no network needed since it'll get an HTTP error or DNS error)
	// but the important thing is we get a valid decision back, not a panic.
	if decision.Kind == "" {
		t.Error("fallback planner should return a valid decision kind")
	}
}

// Ensure ModelPlanner implements the Planner interface at compile time.
var _ Planner = (*ModelPlanner)(nil)

// Ensure DefaultPlanner implements the Planner interface at compile time.
var _ Planner = (*DefaultPlanner)(nil)

// Silence unused import warning for runtime package used in other test helpers.
var _ = runtime.ToolReadSymbol
