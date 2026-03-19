package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultGeminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"

const plannerSystemPrompt = `You are the planning component of a coding agent called projectKitty.
Your job is to choose the next action to complete the given task by calling one of the available tools.

Rules:
- Start with search_repository. Rephrase the task as specific technical identifiers, not natural language.
  Example: task "how does the agent stay within budget" → query "token overflow limit context session turns"
- After search, call outline_context to see what symbols exist in the candidate files.
- If the outline shows a relevant symbol, call inspect_symbol with its exact path and name.
- After reading a symbol, call outline_related to trace cross-file connections, then run_command to validate.
- If no relevant symbol is found after outlining, try search_repository again with different terms.
- Call save_memory then finish when the task is complete.
- Prefer specific function names and identifiers in search queries over generic words.`

// ModelPlanner is a model-driven planner that calls Gemini to decide the next action.
// This mirrors how Claude Code and Gemini CLI expose search/read tools to the model
// and let it navigate iteratively, rather than running a hardcoded sequence.
type ModelPlanner struct {
	apiKey   string
	endpoint string
	fallback *DefaultPlanner
}

func NewModelPlanner(apiKey string) *ModelPlanner {
	return &ModelPlanner{
		apiKey:   apiKey,
		endpoint: defaultGeminiEndpoint,
		fallback: NewPlanner(),
	}
}

// httpClient has a 30-second timeout so a slow or hung Gemini API never
// blocks the agent loop indefinitely.
var httpClient = &http.Client{Timeout: 30 * time.Second}

func (p *ModelPlanner) Next(ctx context.Context, state State) Decision {
	decision, err := p.next(ctx, state)
	if err != nil {
		return p.fallback.Next(ctx, state)
	}
	return decision
}

func (p *ModelPlanner) next(ctx context.Context, state State) (Decision, error) {
	reqBody := map[string]any{
		"system_instruction": map[string]any{
			"parts": []map[string]any{{"text": plannerSystemPrompt}},
		},
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": buildStatePrompt(state)}}},
		},
		"tools": []map[string]any{
			{"function_declarations": geminiToolDefinitions()},
		},
		"tool_config": map[string]any{
			"function_calling_config": map[string]any{"mode": "ANY"},
		},
		"generationConfig": map[string]any{"temperature": 0.1, "maxOutputTokens": 256},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return Decision{}, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"?key="+p.apiKey, bytes.NewReader(body))
	if err != nil {
		return Decision{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Decision{}, err
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Decision{}, err
	}
	if errObj, ok := result["error"]; ok {
		return Decision{}, fmt.Errorf("gemini API error: %v", errObj)
	}

	return parseGeminiDecision(result)
}

func buildStatePrompt(state State) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Task: %s\n\nCurrent state:\n", state.Input.Task)

	if state.SearchTool == nil {
		sb.WriteString("- Search: not done\n")
	} else if state.SearchTool.Result != nil {
		fmt.Fprintf(&sb, "- Search: done — %s\n", state.SearchTool.Result.Summary)
	}

	if state.OutlineTool == nil {
		sb.WriteString("- Outline: not done\n")
	} else if state.OutlineTool.Result != nil {
		fmt.Fprintf(&sb, "- Outline: done — %s\n", state.OutlineTool.Result.Summary)
		if state.OutlineTool.Result.FocusedSymbol != nil {
			sym := state.OutlineTool.Result.FocusedSymbol
			fmt.Fprintf(&sb, "  Focused symbol: %s in %s (confidence %d)\n", sym.Name, sym.Path, sym.Confidence)
		}
		if len(state.OutlineTool.Result.RelatedFiles) > 0 {
			fmt.Fprintf(&sb, "  Related files: %s\n", strings.Join(state.OutlineTool.Result.RelatedFiles, ", "))
		}
	}

	if state.ReadSymbolTool != nil && state.ReadSymbolTool.Result != nil {
		fmt.Fprintf(&sb, "- Symbol read: %s\n", state.ReadSymbolTool.Result.Summary)
	}
	if state.RelatedOutlineTool != nil && state.RelatedOutlineTool.Result != nil {
		fmt.Fprintf(&sb, "- Related outline: done — %s\n", state.RelatedOutlineTool.Result.Summary)
	}
	if state.ValidationTool != nil && state.ValidationTool.Result != nil {
		fmt.Fprintf(&sb, "- Validation: done — %s\n", state.ValidationTool.Result.Summary)
	}
	if state.MemorySaved {
		sb.WriteString("- Memory: saved\n")
	}

	fmt.Fprintf(&sb, "\nSteps taken: %d/20\n\nWhat is the next action?", state.Steps)
	return sb.String()
}

func geminiToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "search_repository",
			"description": "Search the repository for files relevant to the task. Reformulate the task as specific technical identifiers — function names, type names, constants — not natural language questions.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query using specific identifiers. Example: 'chooseValidationCommand' or 'contextOverflowTokens maxSessionTurns'.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "outline_context",
			"description": "Extract top-level symbols from candidate files found by search. Call after search_repository.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "inspect_symbol",
			"description": "Read the full source of one specific symbol. Use the exact path and name from outline results.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": "File path relative to workspace root"},
					"symbol": map[string]any{"type": "string", "description": "Exact symbol name as it appears in the outline"},
				},
				"required": []string{"path", "symbol"},
			},
		},
		{
			"name":        "outline_related",
			"description": "Outline files related to the focused symbol for one cross-file hop. Call after inspect_symbol when related files are listed.",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "run_command",
			"description": "Run the appropriate validation command for this repository (go test, git status, etc.).",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "save_memory",
			"description": "Persist key findings to project memory.",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "finish",
			"description": "The task is complete. All needed information has been gathered and validated.",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func parseGeminiDecision(result map[string]any) (Decision, error) {
	candidates, ok := result["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		return Decision{}, fmt.Errorf("no candidates in Gemini response")
	}
	content, ok := candidates[0].(map[string]any)["content"].(map[string]any)
	if !ok {
		return Decision{}, fmt.Errorf("no content in Gemini candidate")
	}
	parts, ok := content["parts"].([]any)
	if !ok || len(parts) == 0 {
		return Decision{}, fmt.Errorf("no parts in Gemini content")
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		return Decision{}, fmt.Errorf("invalid Gemini part format")
	}
	funcCall, ok := part["functionCall"].(map[string]any)
	if !ok {
		return Decision{}, fmt.Errorf("Gemini returned text instead of function call")
	}

	name, _ := funcCall["name"].(string)
	args, _ := funcCall["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	return functionCallToDecision(name, args)
}

func functionCallToDecision(name string, args map[string]any) (Decision, error) {
	switch name {
	case "search_repository":
		query, _ := args["query"].(string)
		return Decision{
			Kind:   ActionSearchRepository,
			Title:  "Search repository",
			Detail: fmt.Sprintf("Model-driven search: %q", query),
			Query:  query,
		}, nil
	case "outline_context":
		return Decision{Kind: ActionOutlineContext, Title: "Outline candidate files"}, nil
	case "inspect_symbol":
		path, _ := args["path"].(string)
		symbol, _ := args["symbol"].(string)
		return Decision{
			Kind:   ActionInspectSymbol,
			Title:  "Inspect focused symbol",
			Detail: fmt.Sprintf("Read %s from %s", symbol, path),
			Path:   path,
			Symbol: symbol,
		}, nil
	case "outline_related":
		return Decision{Kind: ActionOutlineRelated, Title: "Outline related files"}, nil
	case "run_command":
		return Decision{Kind: ActionRunCommand, Title: "Run validation"}, nil
	case "save_memory":
		return Decision{Kind: ActionSaveMemory, Title: "Save memory"}, nil
	case "finish":
		return Decision{Kind: ActionFinish, Title: "Finish", Detail: "Model-driven loop completed."}, nil
	default:
		return Decision{}, fmt.Errorf("unknown Gemini function call: %q", name)
	}
}
