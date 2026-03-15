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

const (
	maxSessionTurns          = 20
	contextOverflowTokens    = 40_000
)

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
			if state.Steps >= maxSessionTurns {
				events <- newEvent(EventLoopDetected, state.Steps, "Loop detected", fmt.Sprintf("Session exceeded %d steps — stopping to prevent runaway execution.", maxSessionTurns))
				_ = a.memory.EndSession(sessionID)
				return
			}
			decision := a.planner.Next(state)
			step := state.Steps + 1
			events <- newEvent(EventPlanning, step, decision.Title, decision.Detail)

			if decision.Thoughts != "" {
				events <- newEvent(EventThought, step, "Thinking", decision.Thoughts)
			}

			if err := a.memory.RecordSessionEvent(sessionID, "plan", decision.Title+": "+decision.Detail); err != nil {
				events <- newErrorEvent(step, "Record plan", err)
				return
			}

			switch decision.Kind {
			case ActionSearchRepository:
				query := input.Task
				if decision.Query != "" {
					query = decision.Query
				}
				req := intelligence.Request{
					Task:      query,
					Workspace: input.Workspace,
				}
				search, err := a.intelligence.Search(ctx, req)
				if err != nil {
					events <- newErrorEvent(step, "Search repository", err)
					return
				}
				state.SearchTool = &SearchToolState{
					Request: req,
					Result:  &search,
				}
				events <- newEvent(EventSearchObserved, step, "Search results", search.Summary)
				if err := a.memory.RecordSessionEvent(sessionID, "search", search.Summary); err != nil {
					events <- newErrorEvent(step, "Record search", err)
					return
				}

			case ActionBroadenSearch:
			broad := longestWord(input.Task)
			if broad == "" {
				state.BroadenedSearch = true
				break
			}
			broadReq := intelligence.Request{
				Task:      broad,
				Workspace: input.Workspace,
			}
			broadSearch, err := a.intelligence.Search(ctx, broadReq)
			if err == nil && len(broadSearch.CandidateFiles) > 0 {
				merged := mergeUnique(state.SearchTool.Result.CandidateFiles, broadSearch.CandidateFiles)
				state.SearchTool.Result.CandidateFiles = merged
				broadSearch.Summary = fmt.Sprintf("Broadened search results (token %q): %s", broad, strings.Join(merged, ", "))
				events <- newEvent(EventSearchObserved, step, "Broadened search results", broadSearch.Summary)
				if err := a.memory.RecordSessionEvent(sessionID, "search_broad", broadSearch.Summary); err != nil {
					events <- newErrorEvent(step, "Record broadened search", err)
					return
				}
			}
			state.BroadenedSearch = true
			state.OutlineTool = nil // reset so planner re-outlines with merged candidates

		case ActionOutlineContext:
				req := intelligence.OutlineRequest{
					Task:      input.Task,
					Workspace: input.Workspace,
					Files:     state.SearchTool.Result.CandidateFiles,
				}
				outline, err := a.intelligence.Outline(ctx, req)
				if err != nil {
					events <- newErrorEvent(step, "Outline context", err)
					return
				}
				state.OutlineTool = &OutlineToolState{
					Request: req,
					Result:  &outline,
				}
				events <- newEvent(EventOutlineObserved, step, "Outline results", outline.Summary)
				if outline.EstimatedTokens > contextOverflowTokens {
					events <- newEvent(EventContextWindowWillOverflow, step, "Context window warning",
						fmt.Sprintf("Estimated context ~%d tokens exceeds threshold — candidate set will be trimmed on next pass.", outline.EstimatedTokens))
				}
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
				state.ReadSymbolTool = &ReadSymbolToolState{
					Call:   call,
					Result: &result,
				}
				events <- newEvent(EventSymbolObserved, step, "Focused symbol", result.Summary)
				if err := a.memory.RecordSessionEvent(sessionID, "read_symbol", result.Summary); err != nil {
					events <- newErrorEvent(step, "Record symbol read", err)
					return
				}

			case ActionOutlineRelated:
			var relatedFiles []string
			if state.OutlineTool != nil && state.OutlineTool.Result != nil {
				relatedFiles = state.OutlineTool.Result.RelatedFiles
			}
			req := intelligence.OutlineRequest{
				Task:      input.Task,
				Workspace: input.Workspace,
				Files:     relatedFiles,
			}
			outline, err := a.intelligence.Outline(ctx, req)
			if err != nil {
				events <- newErrorEvent(step, "Outline related files", err)
				return
			}
			state.RelatedOutlineTool = &OutlineToolState{
				Request: req,
				Result:  &outline,
			}
			events <- newEvent(EventOutlineObserved, step, "Related file outline", outline.Summary)
			if err := a.memory.RecordSessionEvent(sessionID, "outline_related", outline.Summary); err != nil {
				events <- newErrorEvent(step, "Record related outline", err)
				return
			}

		case ActionRunCommand:
				cmd := decision.Command
				if cmd == "" {
					cmd = chooseValidationCommand(state)
				}
				call := runtime.Call{
					Tool:      runtime.ToolShell,
					Workspace: input.Workspace,
					Command:   cmd,
				}
				events <- newEvent(EventAction, step, "Runtime action", cmd)
				result, err := a.runtime.Execute(ctx, call)
				if err != nil {
					events <- newErrorEvent(step, "Execute command", err)
					return
				}
				state.ValidationTool = &ValidationToolState{
					Call:   call,
					Result: &result,
				}
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
	if state.SearchTool != nil && state.SearchTool.Result != nil {
		parts = append(parts, "Candidates: "+strings.Join(state.SearchTool.Result.CandidateFiles, ", "))
	}
	if state.OutlineTool != nil && state.OutlineTool.Result != nil && state.OutlineTool.Result.FocusedSymbol != nil {
		parts = append(parts, "Best symbol: "+state.OutlineTool.Result.FocusedSymbol.Name+" in "+state.OutlineTool.Result.FocusedSymbol.Path)
		if len(state.OutlineTool.Result.RelatedFiles) > 0 {
			parts = append(parts, "Related files: "+strings.Join(state.OutlineTool.Result.RelatedFiles, ", "))
		}
	}
	if state.ReadSymbolTool != nil && state.ReadSymbolTool.Result != nil {
		parts = append(parts, "Focused read: "+state.ReadSymbolTool.Result.Summary)
	}
	if state.RelatedOutlineTool != nil && state.RelatedOutlineTool.Result != nil {
		parts = append(parts, "Related outline: "+state.RelatedOutlineTool.Result.Summary)
	}
	if state.ValidationTool != nil && state.ValidationTool.Result != nil {
		parts = append(parts, "Validation: "+state.ValidationTool.Result.Summary)
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

// longestWord returns the longest whitespace-separated word in s.
func longestWord(s string) string {
	best := ""
	for _, w := range strings.Fields(s) {
		if len(w) > len(best) {
			best = w
		}
	}
	return best
}

// mergeUnique appends elements of b to a copy of a, skipping duplicates.
func mergeUnique(a, b []string) []string {
	seen := make(map[string]struct{}, len(a))
	out := make([]string, 0, len(a)+len(b))
	for _, v := range a {
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range b {
		if _, ok := seen[v]; !ok {
			out = append(out, v)
		}
	}
	return out
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
