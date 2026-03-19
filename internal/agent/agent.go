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

		send := func(e Event) bool {
			select {
			case events <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}

		sessionID, err := a.memory.StartSession(input.Task, input.Workspace)
		if err != nil {
			send(newErrorEvent(0, "Start session", err))
			return
		}

		record := func(step int, kind, detail string) {
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
			if decision.Thoughts != "" && !send(newEvent(EventThought, step, "Thinking", decision.Thoughts)) {
				return
			}
			record(step, "plan", decision.Title+": "+decision.Detail)

			switch decision.Kind {
			case ActionSearchRepository:
				query := input.Task
				if decision.Query != "" {
					query = decision.Query
				}
				req := intelligence.Request{Task: query, Workspace: input.Workspace}
				search, err := a.intelligence.Search(ctx, req)
				if err != nil {
					send(newErrorEvent(step, "Search repository", err))
					return
				}
				state.SearchTool = &SearchToolState{Request: req, Result: &search}
				if !send(newEvent(EventSearchObserved, step, "Search results", search.Summary)) {
					return
				}
				record(step, "search", search.Summary)

			case ActionBroadenSearch:
				broad := longestWord(input.Task)
				if broad != "" {
					req := intelligence.Request{Task: broad, Workspace: input.Workspace}
					search, err := a.intelligence.Search(ctx, req)
					if err != nil {
						if !send(newEvent(EventWarning, step, "Broadened search failed", err.Error())) {
							return
						}
					} else if state.SearchTool != nil && state.SearchTool.Result != nil && len(search.CandidateFiles) > 0 {
						merged := mergeUnique(state.SearchTool.Result.CandidateFiles, search.CandidateFiles)
						state.SearchTool.Result.CandidateFiles = merged
						search.Summary = fmt.Sprintf("Broadened search results (token %q): %s", broad, strings.Join(merged, ", "))
						if !send(newEvent(EventSearchObserved, step, "Broadened search results", search.Summary)) {
							return
						}
						record(step, "search_broad", search.Summary)
					}
				}
				state.BroadenedSearch = true
				state.OutlineTool = nil

			case ActionOutlineContext:
				req := intelligence.OutlineRequest{
					Task:      input.Task,
					Workspace: input.Workspace,
					Files:     state.SearchTool.Result.CandidateFiles,
				}
				outline, err := a.intelligence.Outline(ctx, req)
				if err != nil {
					send(newErrorEvent(step, "Outline context", err))
					return
				}
				state.OutlineTool = &OutlineToolState{Request: req, Result: &outline}
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
					send(newErrorEvent(step, "Read symbol", err))
					return
				}
				state.ReadSymbolTool = &ReadSymbolToolState{Call: call, Result: &result}
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
					send(newErrorEvent(step, "Outline related files", err))
					return
				}
				state.RelatedOutlineTool = &OutlineToolState{Request: req, Result: &outline}
				if !send(newEvent(EventOutlineObserved, step, "Related file outline", outline.Summary)) {
					return
				}
				record(step, "outline_related", outline.Summary)

			case ActionRunCommand:
				cmd := decision.Command
				if cmd == "" {
					cmd = chooseValidationCommand(state)
				}
				call := runtime.Call{
					Tool:      runtime.ToolShell,
					Workspace: input.Workspace,
					Command:   cmd,
					Stream: func(execID, line string) {
						select {
						case events <- newEvent(EventObserved, step, execID, line):
						case <-ctx.Done():
						}
					},
				}
				if !send(newEvent(EventAction, step, "Runtime action", cmd)) {
					return
				}
				result, err := a.runtime.Execute(ctx, call)
				if err != nil {
					if !send(newEvent(EventWarning, step, "Command failed", err.Error())) {
						return
					}
					state.ValidationTool = &ValidationToolState{
						Call:   call,
						Result: &runtime.Result{Tool: runtime.ToolShell, Summary: err.Error()},
					}
				} else {
					state.ValidationTool = &ValidationToolState{Call: call, Result: &result}
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
					Category: "article3-taking-action",
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
	if state.WriteFileTool != nil && state.WriteFileTool.Result != nil {
		parts = append(parts, "Write: "+state.WriteFileTool.Result.Summary)
	}
	if state.EditFileTool != nil && state.EditFileTool.Result != nil {
		parts = append(parts, "Edit: "+state.EditFileTool.Result.Summary)
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

func longestWord(s string) string {
	best := ""
	for _, w := range strings.Fields(s) {
		if len(w) > len(best) {
			best = w
		}
	}
	return best
}

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
