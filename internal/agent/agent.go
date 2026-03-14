package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/memory"
	"github.com/w1ne/projectkitty/internal/runtime"
)

type Agent struct {
	planner      Planner
	intelligence intelligence.Service
	runtime      *runtime.Runtime
	memory       *memory.Store
}

func New(planner Planner, intel intelligence.Service, rt *runtime.Runtime, memory *memory.Store) *Agent {
	return &Agent{
		planner:      planner,
		intelligence: intel,
		runtime:      rt,
		memory:       memory,
	}
}

func (a *Agent) Run(ctx context.Context, input RunInput) <-chan Event {
	events := make(chan Event)

	go func() {
		defer close(events)

		sessionID, err := a.memory.StartSession(input.Task, input.Workspace)
		if err != nil {
			events <- newErrorEvent(0, "Start session", err)
			return
		}

		state := State{Input: input}
		events <- newEvent(EventStarted, 0, "Session started", fmt.Sprintf("Task: %s", input.Task))

		for {
			decision := a.planner.Next(state)
			step := state.Steps + 1
			events <- newEvent(EventPlanning, step, decision.Title, decision.Detail)

			if err := a.memory.RecordSessionEvent(sessionID, "plan", decision.Title+": "+decision.Detail); err != nil {
				events <- newErrorEvent(step, "Record plan", err)
				return
			}

			switch decision.Kind {
			case ActionSearchRepository:
				search, err := a.intelligence.Search(ctx, intelligence.Request{
					Task:      input.Task,
					Workspace: input.Workspace,
				})
				if err != nil {
					events <- newErrorEvent(step, "Search repository", err)
					return
				}
				state.Search = &search
				events <- newEvent(EventObserved, step, "Search results", search.Summary)
				if err := a.memory.RecordSessionEvent(sessionID, "search", search.Summary); err != nil {
					events <- newErrorEvent(step, "Record search", err)
					return
				}

			case ActionOutlineContext:
				outline, err := a.intelligence.Outline(ctx, intelligence.OutlineRequest{
					Task:      input.Task,
					Workspace: input.Workspace,
					Files:     state.Search.CandidateFiles,
				})
				if err != nil {
					events <- newErrorEvent(step, "Outline context", err)
					return
				}
				state.Outline = &outline
				events <- newEvent(EventObserved, step, "Outline results", outline.Summary)
				if err := a.memory.RecordSessionEvent(sessionID, "outline", outline.Summary); err != nil {
					events <- newErrorEvent(step, "Record outline", err)
					return
				}

			case ActionInspectSymbol:
				call := runtime.Call{
					Tool:      runtime.ToolReadSymbol,
					Workspace: input.Workspace,
					Path:      decision.Path,
					Symbol:    decision.Symbol,
				}
				events <- newEvent(EventAction, step, "Runtime action", fmt.Sprintf("Read symbol %s from %s", decision.Symbol, decision.Path))
				result, err := a.runtime.Execute(ctx, call)
				if err != nil {
					events <- newErrorEvent(step, "Read symbol", err)
					return
				}
				state.SymbolReadResult = &result
				events <- newEvent(EventObserved, step, "Focused symbol", result.Summary)
				if err := a.memory.RecordSessionEvent(sessionID, "read_symbol", result.Summary); err != nil {
					events <- newErrorEvent(step, "Record symbol read", err)
					return
				}

			case ActionRunCommand:
				call := runtime.Call{
					Tool:      runtime.ToolShell,
					Workspace: input.Workspace,
					Command:   decision.Command,
				}
				events <- newEvent(EventAction, step, "Runtime action", decision.Command)
				result, err := a.runtime.Execute(ctx, call)
				if err != nil {
					events <- newErrorEvent(step, "Execute command", err)
					return
				}
				state.ValidationResult = &result
				events <- newEvent(EventObserved, step, "Runtime result", result.Summary)
				if err := a.memory.RecordSessionEvent(sessionID, "runtime", result.Summary); err != nil {
					events <- newErrorEvent(step, "Record runtime", err)
					return
				}

			case ActionSaveMemory:
				factSummary := summarizeFact(state)
				if err := a.memory.SaveFact(memory.Fact{
					Category: "article1-foundation",
					Summary:  factSummary,
				}); err != nil {
					events <- newErrorEvent(step, "Save fact", err)
					return
				}
				if err := a.memory.RecordSessionEvent(sessionID, "memory", factSummary); err != nil {
					events <- newErrorEvent(step, "Record memory", err)
					return
				}
				state.MemorySaved = true
				events <- newEvent(EventMemory, step, "Memory updated", factSummary)

			case ActionFinish:
				events <- newEvent(EventFinished, step, "Loop finished", decision.Detail)
				_ = a.memory.EndSession(sessionID)
				return
			}

			state.Steps++
		}
	}()

	return events
}

func summarizeFact(state State) string {
	parts := make([]string, 0, 3)
	if state.Search != nil {
		parts = append(parts, "Candidates: "+strings.Join(state.Search.CandidateFiles, ", "))
	}
	if state.Outline != nil && state.Outline.FocusedSymbol != nil {
		parts = append(parts, "Best symbol: "+state.Outline.FocusedSymbol.Name+" in "+state.Outline.FocusedSymbol.Path)
	}
	if state.SymbolReadResult != nil {
		parts = append(parts, "Focused read: "+state.SymbolReadResult.Summary)
	}
	if state.ValidationResult != nil {
		parts = append(parts, "Validation: "+state.ValidationResult.Summary)
	}
	if len(parts) == 0 {
		return "No durable facts captured."
	}
	return strings.Join(parts, " | ")
}

func newEvent(kind EventKind, step int, title, detail string) Event {
	return Event{
		Kind:      kind,
		Step:      step,
		Title:     title,
		Detail:    detail,
		Timestamp: time.Now().UTC(),
	}
}

func newErrorEvent(step int, title string, err error) Event {
	return Event{
		Kind:      EventErrored,
		Step:      step,
		Title:     title,
		ErrText:   err.Error(),
		Timestamp: time.Now().UTC(),
	}
}
