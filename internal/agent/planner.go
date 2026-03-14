package agent

import "strings"

type Planner interface {
	Next(State) Decision
}

type DefaultPlanner struct{}

func NewPlanner() *DefaultPlanner {
	return &DefaultPlanner{}
}

func (p *DefaultPlanner) Next(state State) Decision {
	if state.Search == nil {
		return Decision{
			Kind:   ActionSearchRepository,
			Title:  "Search repository",
			Detail: "Use cheap search to find the smallest useful repository slice.",
		}
	}

	if state.Outline == nil {
		return Decision{
			Kind:   ActionOutlineContext,
			Title:  "Outline candidate files",
			Detail: "Extract top-level symbols from likely files before reading code.",
		}
	}

	if state.Outline.FocusedSymbol != nil && state.SymbolReadResult == nil {
		return Decision{
			Kind:   ActionInspectSymbol,
			Title:  "Inspect focused symbol",
			Detail: "Read the smallest useful symbol before running repository validation.",
			Path:   state.Outline.FocusedSymbol.Path,
			Symbol: state.Outline.FocusedSymbol.Name,
		}
	}

	if state.ValidationResult == nil {
		return Decision{
			Kind:    ActionRunCommand,
			Title:   "Run safe validation",
			Detail:  "Execute the safest command that can validate the current repository state.",
			Command: chooseValidationCommand(state),
		}
	}

	if !state.MemorySaved {
		return Decision{
			Kind:   ActionSaveMemory,
			Title:  "Persist findings",
			Detail: "Write the key findings into durable project memory and the session log.",
		}
	}

	return Decision{
		Kind:   ActionFinish,
		Title:  "Finish",
		Detail: "The foundational meow loop has completed.",
	}
}

func chooseValidationCommand(state State) string {
	if state.Search.HasGoModule {
		return "go test ./..."
	}

	for _, file := range state.Search.CandidateFiles {
		if strings.EqualFold(file, "go.mod") || strings.HasSuffix(file, "/go.mod") {
			return "go test ./..."
		}
	}

	return "git status --short"
}
