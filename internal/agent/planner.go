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
	if state.Context == nil {
		return Decision{
			Kind:   ActionGatherContext,
			Title:  "Gather focused context",
			Detail: "Use code intelligence to find the smallest useful repository slice.",
		}
	}

	if state.LastToolResult == nil {
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
	if state.Context.HasGoModule {
		return "go test ./..."
	}

	for _, file := range state.Context.CandidateFiles {
		if strings.EqualFold(file, "go.mod") || strings.HasSuffix(file, "/go.mod") {
			return "go test ./..."
		}
	}

	return "git status --short"
}
