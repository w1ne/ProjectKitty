package agent

import (
	"context"
	"fmt"
	"strings"
)

type Planner interface {
	Next(ctx context.Context, state State) Decision
}

type DefaultPlanner struct{}

func NewPlanner() *DefaultPlanner {
	return &DefaultPlanner{}
}

func (p *DefaultPlanner) Next(_ context.Context, state State) Decision {
	if state.SearchTool == nil || state.SearchTool.Result == nil {
		return Decision{
			Kind:     ActionSearchRepository,
			Title:    "Search repository",
			Detail:   "Use cheap search to find the smallest useful repository slice.",
			Thoughts: "Whiskers needs to narrow the search space before parsing to remain efficient like Claude and Codex.",
		}
	}

	if state.OutlineTool == nil || state.OutlineTool.Result == nil {
		return Decision{
			Kind:     ActionOutlineContext,
			Title:    "Outline candidate files",
			Detail:   "Extract top-level symbols from likely files before reading code.",
			Thoughts: "Structural outlining is faster than reading full files. Mirroring the 'Outline' stage from the research.",
		}
	}

	if state.OutlineTool.Result.FocusedSymbol == nil && !state.BroadenedSearch {
		return Decision{
			Kind:     ActionBroadenSearch,
			Title:    "Broaden search",
			Detail:   "No focused symbol found — retry with the strongest task token to widen the candidate set.",
			Thoughts: "The first outline pass found no strong match. Broadening to the single most distinctive token mirrors Claude's iterative tool-call refinement on low confidence.",
		}
	}

	if state.OutlineTool.Result.FocusedSymbol != nil && (state.ReadSymbolTool == nil || state.ReadSymbolTool.Result == nil) {
		return Decision{
			Kind:     ActionInspectSymbol,
			Title:    "Inspect focused symbol",
			Detail:   "Read the smallest useful symbol before running repository validation.",
			Thoughts: "We found a strong symbol match. Reading only this symbol reduces context usage and potential noise.",
			Path:     state.OutlineTool.Result.FocusedSymbol.Path,
			Symbol:   state.OutlineTool.Result.FocusedSymbol.Name,
		}
	}

	if state.ReadSymbolTool != nil && state.ReadSymbolTool.Result != nil &&
		state.OutlineTool != nil && len(state.OutlineTool.Result.RelatedFiles) > 0 &&
		state.RelatedOutlineTool == nil {
		return Decision{
			Kind:     ActionOutlineRelated,
			Title:    "Outline related files",
			Detail:   fmt.Sprintf("Trace cross-file relationships from %s via: %s", state.OutlineTool.Result.FocusedSymbol.Name, strings.Join(state.OutlineTool.Result.RelatedFiles, ", ")),
			Thoughts: "After reading the focused symbol, outline related files to understand callers and co-located types. This is the cross-file hop Claude handles through iterative model-driven tool calls.",
		}
	}

	// If a file was written or edited (by a model-driven planner), proceed
	// straight to validation. DefaultPlanner does not initiate writes — it
	// cannot know what to write without model guidance — but it correctly
	// handles the post-write validation and memory-save phases.
	wroteOrEdited := (state.WriteFileTool != nil && state.WriteFileTool.Result != nil) ||
		(state.EditFileTool != nil && state.EditFileTool.Result != nil)

	if state.ValidationTool == nil || state.ValidationTool.Result == nil {
		title := "Run safe validation"
		thoughts := "Validation confirms our understanding of the changes. Choosing the most targeted command available."
		if wroteOrEdited {
			title = "Validate file change"
			thoughts = "A file was written or edited. Validate immediately to confirm it compiles and tests pass."
		}
		return Decision{
			Kind:     ActionRunCommand,
			Title:    title,
			Detail:   "Execute the safest command that can validate the current repository state.",
			Thoughts: thoughts,
			Command:  chooseValidationCommand(state),
		}
	}

	if !state.MemorySaved {
		return Decision{
			Kind:     ActionSaveMemory,
			Title:    "Persist findings",
			Detail:   "Write the key findings into durable project memory and the session log.",
			Thoughts: "Durable memory prevents information loss across sessions, similar to Claude's MEMORY.md and Codex's SQLite state.",
		}
	}

	return Decision{
		Kind:     ActionFinish,
		Title:    "Finish",
		Detail:   "The foundational meow loop has completed.",
		Thoughts: "All stages (Search -> Outline -> Read -> Validate) are finished. Repository state is confirmed.",
	}
}

func chooseValidationCommand(state State) string {
	if state.SearchTool != nil && state.SearchTool.Result != nil && state.SearchTool.Result.HasGoModule {
		return "go test ./..."
	}

	if state.SearchTool == nil || state.SearchTool.Result == nil {
		return "git status --short"
	}
	for _, file := range state.SearchTool.Result.CandidateFiles {
		if strings.EqualFold(file, "go.mod") || strings.HasSuffix(file, "/go.mod") {
			return "go test ./..."
		}
	}

	return "git status --short"
}
