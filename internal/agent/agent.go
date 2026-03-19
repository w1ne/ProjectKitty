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
	maxSessionTurns       = 20
	contextOverflowTokens = 40_000
)

func (a *Agent) Run(ctx context.Context, input RunInput) <-chan Event {
	events := make(chan Event)

	go func() {
		defer close(events)

		// send delivers an event to the channel, aborting the session if the
		// caller has cancelled the context (prevents goroutine leak when the
		// UI stops reading).
		send := func(e Event) bool {
			select {
			case events <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// record persists a session event but only warns on failure — a
		// filesystem hiccup should never abort the agent loop.
		record := func(step int, kind, detail string) {
			if err := a.memory.RecordSessionEvent("", kind, detail); err != nil {
				send(newEvent(EventWarning, step, "Memory recording skipped", err.Error()))
			}
		}

		sessionID, err := a.memory.StartSession(input.Task, input.Workspace)
		if err != nil {
			send(newErrorEvent(0, "Start session", err))
			return
		}
		// Rebind record with the real sessionID now that we have it.
		record = func(step int, kind, detail string) {
			if err := a.memory.RecordSessionEvent(sessionID, kind, detail); err != nil {
				send(newEvent(EventWarning, step, "Memory recording skipped", err.Error()))
			}
		}

		state := State{Input: input}
		if !send(newEvent(EventStarted, 0, "Session started", fmt.Sprintf("Task: %s", input.Task))) {
			return
		}

		for {
			if state.Steps >= maxSessionTurns {
				send(newEvent(EventLoopDetected, state.Steps, "Loop detected",
					fmt.Sprintf("Session exceeded %d steps — stopping to prevent runaway execution.", maxSessionTurns)))
				_ = a.memory.EndSession(sessionID)
				return
			}

			decision := a.planner.Next(ctx, state)
			step := state.Steps + 1
			if !send(newEvent(EventPlanning, step, decision.Title, decision.Detail)) {
				return
			}

			if decision.Thoughts != "" {
				if !send(newEvent(EventThought, step, "Thinking", decision.Thoughts)) {
					return
				}
			}

			record(step, "plan", decision.Title+": "+decision.Detail)

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
					if !send(newErrorEvent(step, "Search repository", err)) {
						return
					}
					return
				}
				state.SearchTool = &SearchToolState{
					Request: req,
					Result:  &search,
				}
				if !send(newEvent(EventSearchObserved, step, "Search results", search.Summary)) {
					return
				}
				record(step, "search", search.Summary)

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
				if err != nil {
					// Non-fatal: warn and mark broadened so we don't loop.
					if !send(newEvent(EventWarning, step, "Broadened search failed", err.Error())) {
						return
					}
				} else if len(broadSearch.CandidateFiles) > 0 && state.SearchTool != nil && state.SearchTool.Result != nil {
					merged := mergeUnique(state.SearchTool.Result.CandidateFiles, broadSearch.CandidateFiles)
					state.SearchTool.Result.CandidateFiles = merged
					broadSearch.Summary = fmt.Sprintf("Broadened search results (token %q): %s", broad, strings.Join(merged, ", "))
					if !send(newEvent(EventSearchObserved, step, "Broadened search results", broadSearch.Summary)) {
						return
					}
					record(step, "search_broad", broadSearch.Summary)
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
					if !send(newErrorEvent(step, "Outline context", err)) {
						return
					}
					return
				}
				state.OutlineTool = &OutlineToolState{
					Request: req,
					Result:  &outline,
				}
				if !send(newEvent(EventOutlineObserved, step, "Outline results", outline.Summary)) {
					return
				}
				if outline.EstimatedTokens > contextOverflowTokens {
					if !send(newEvent(EventContextWindowWillOverflow, step, "Context window warning",
						fmt.Sprintf("Estimated context ~%d tokens exceeds threshold — candidate set will be trimmed on next pass.", outline.EstimatedTokens))) {
						return
					}
				}
				record(step, "outline", outline.Summary)

			case ActionInspectSymbol:
				call := runtime.Call{
					Tool:      runtime.ToolReadSymbol,
					Workspace: input.Workspace,
					Path:      decision.Path,
					Symbol:    decision.Symbol,
				}
				if !send(newEvent(EventAction, step, "Runtime action", fmt.Sprintf("Read symbol %s from %s", decision.Symbol, decision.Path))) {
					return
				}
				result, err := a.runtime.Execute(ctx, call)
				if err != nil {
					if !send(newErrorEvent(step, "Read symbol", err)) {
						return
					}
					return
				}
				state.ReadSymbolTool = &ReadSymbolToolState{
					Call:   call,
					Result: &result,
				}
				if !send(newEvent(EventSymbolObserved, step, "Focused symbol", result.Summary)) {
					return
				}
				record(step, "read_symbol", result.Summary)

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
					if !send(newErrorEvent(step, "Outline related files", err)) {
						return
					}
					return
				}
				state.RelatedOutlineTool = &OutlineToolState{
					Request: req,
					Result:  &outline,
				}
				if !send(newEvent(EventOutlineObserved, step, "Related file outline", outline.Summary)) {
					return
				}
				record(step, "outline_related", outline.Summary)

			case ActionRunCommand:
				cmd := decision.Command
				if cmd == "" {
					cmd = chooseValidationCommand(state)
				}
				if !send(newEvent(EventAction, step, "Runtime action", cmd)) {
					return
				}
				call := runtime.Call{
					Tool:      runtime.ToolShell,
					Workspace: input.Workspace,
					Command:   cmd,
					Stream: func(execID, line string) {
						// Best-effort stream: don't abort if ctx is done.
						select {
						case events <- newEvent(EventObserved, step, execID, line):
						case <-ctx.Done():
						}
					},
				}
				result, err := a.runtime.Execute(ctx, call)
				if err != nil {
					// Policy blocks and execution errors are non-fatal:
					// emit a warning and let the planner decide next action.
					if !send(newEvent(EventWarning, step, "Command failed", err.Error())) {
						return
					}
					state.ValidationTool = &ValidationToolState{
						Call:   call,
						Result: &runtime.Result{Summary: err.Error()},
					}
				} else {
					state.ValidationTool = &ValidationToolState{
						Call:   call,
						Result: &result,
					}
					if !send(newEvent(EventObserved, step, "Runtime result", result.Summary)) {
						return
					}
					record(step, "runtime", result.Summary)
				}

			case ActionWriteFile:
			call := runtime.Call{
				Tool:      runtime.ToolWriteFile,
				Workspace: input.Workspace,
				Path:      decision.Path,
				Content:   decision.Content,
			}
			if !send(newEvent(EventAction, step, "Runtime action", fmt.Sprintf("Write file %s", decision.Path))) {
				return
			}
			result, err := a.runtime.Execute(ctx, call)
			if err != nil {
				if !send(newEvent(EventWarning, step, "Write file failed", err.Error())) {
					return
				}
			} else {
				state.WriteFileTool = &WriteFileToolState{Call: call, Result: &result}
				if !send(newEvent(EventWriteObserved, step, "File written", result.Summary)) {
					return
				}
				record(step, "write_file", result.Summary)
			}

		case ActionEditFile:
			call := runtime.Call{
				Tool:      runtime.ToolEditFile,
				Workspace: input.Workspace,
				Path:      decision.Path,
				OldString: decision.OldString,
				NewString: decision.NewString,
			}
			if !send(newEvent(EventAction, step, "Runtime action", fmt.Sprintf("Edit file %s", decision.Path))) {
				return
			}
			result, err := a.runtime.Execute(ctx, call)
			if err != nil {
				if !send(newEvent(EventWarning, step, "Edit file failed", err.Error())) {
					return
				}
			} else {
				state.EditFileTool = &EditFileToolState{Call: call, Result: &result}
				if !send(newEvent(EventEditObserved, step, "File edited", result.Summary)) {
					return
				}
				record(step, "edit_file", result.Summary)
			}

		case ActionSaveMemory:
				factSummary := summarizeFact(state)
				if err := a.memory.SaveFact(memory.Fact{
					Category: "article1-foundation",
					Summary:  factSummary,
				}); err != nil {
					if !send(newEvent(EventWarning, step, "Save fact failed", err.Error())) {
						return
					}
				}
				record(step, "memory", factSummary)
				state.MemorySaved = true
				if !send(newEvent(EventMemory, step, "Memory updated", factSummary)) {
					return
				}

			case ActionFinish:
				send(newEvent(EventFinished, step, "Loop finished", decision.Detail))
				_ = a.memory.EndSession(sessionID)
				return
			}

			state.Steps++
		}
	}()

	return events
}

func summarizeFact(state State) string {
	parts := make([]string, 0, 5)
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
	if state.WriteFileTool != nil && state.WriteFileTool.Result != nil {
		parts = append(parts, "Write: "+state.WriteFileTool.Result.Summary)
	}
	if state.EditFileTool != nil && state.EditFileTool.Result != nil {
		parts = append(parts, "Edit: "+state.EditFileTool.Result.Summary)
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
