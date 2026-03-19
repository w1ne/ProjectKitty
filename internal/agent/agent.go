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

type sessionRunner struct {
	agent     *Agent
	ctx       context.Context
	input     RunInput
	sessionID string
	state     State
	events    chan<- Event
}

func (a *Agent) Run(ctx context.Context, input RunInput) <-chan Event {
	events := make(chan Event)

	go func() {
		defer close(events)

		sessionID, err := a.memory.StartSession(input.Task, input.Workspace)
		if err != nil {
			select {
			case events <- newErrorEvent(0, "Start session", err):
			case <-ctx.Done():
			}
			return
		}

		runner := sessionRunner{
			agent:     a,
			ctx:       ctx,
			input:     input,
			sessionID: sessionID,
			state:     State{Input: input},
			events:    events,
		}
		runner.run()
	}()

	return events
}

func (r *sessionRunner) run() {
	if !r.send(newEvent(EventStarted, 0, "Session started", fmt.Sprintf("Task: %s", r.input.Task))) {
		return
	}

	for {
		if r.state.Steps >= maxSessionTurns {
			r.send(newEvent(
				EventLoopDetected,
				r.state.Steps,
				"Loop detected",
				fmt.Sprintf("Session exceeded %d steps — stopping to prevent runaway execution.", maxSessionTurns),
			))
			_ = r.agent.memory.EndSession(r.sessionID)
			return
		}

		decision := r.agent.planner.Next(r.ctx, r.state)
		step := r.state.Steps + 1
		if !r.emitDecision(step, decision) {
			return
		}

		if stop := r.handleDecision(step, decision); stop {
			return
		}

		r.state.Steps++
	}
}

func (r *sessionRunner) emitDecision(step int, decision Decision) bool {
	if !r.send(newEvent(EventPlanning, step, decision.Title, decision.Detail)) {
		return false
	}
	if decision.Thoughts != "" && !r.send(newEvent(EventThought, step, "Thinking", decision.Thoughts)) {
		return false
	}
	r.record(step, "plan", decision.Title+": "+decision.Detail)
	return true
}

func (r *sessionRunner) handleDecision(step int, decision Decision) bool {
	switch decision.Kind {
	case ActionSearchRepository:
		return r.handleSearchRepository(step, decision)
	case ActionBroadenSearch:
		return r.handleBroadenSearch(step)
	case ActionOutlineContext:
		return r.handleOutlineContext(step)
	case ActionInspectSymbol:
		return r.handleInspectSymbol(step, decision)
	case ActionOutlineRelated:
		return r.handleOutlineRelated(step)
	case ActionRunCommand:
		return r.handleRunCommand(step, decision)
	case ActionWriteFile:
		return r.handleWriteFile(step, decision)
	case ActionEditFile:
		return r.handleEditFile(step, decision)
	case ActionSaveMemory:
		return r.handleSaveMemory(step)
	case ActionFinish:
		r.send(newEvent(EventFinished, step, "Loop finished", decision.Detail))
		_ = r.agent.memory.EndSession(r.sessionID)
		return true
	default:
		r.send(newErrorEvent(step, "Unknown action", fmt.Errorf("unsupported action: %s", decision.Kind)))
		return true
	}
}

func (r *sessionRunner) handleSearchRepository(step int, decision Decision) bool {
	query := r.input.Task
	if decision.Query != "" {
		query = decision.Query
	}

	req := intelligence.Request{Task: query, Workspace: r.input.Workspace}
	search, err := r.agent.intelligence.Search(r.ctx, req)
	if err != nil {
		r.send(newErrorEvent(step, "Search repository", err))
		return true
	}

	r.state.SearchTool = &SearchToolState{Request: req, Result: &search}
	if !r.send(newEvent(EventSearchObserved, step, "Search results", search.Summary)) {
		return true
	}
	r.record(step, "search", search.Summary)
	return false
}

func (r *sessionRunner) handleBroadenSearch(step int) bool {
	broad := longestWord(r.input.Task)
	if broad == "" {
		r.state.BroadenedSearch = true
		r.state.OutlineTool = nil
		return false
	}

	req := intelligence.Request{Task: broad, Workspace: r.input.Workspace}
	search, err := r.agent.intelligence.Search(r.ctx, req)
	if err != nil {
		if !r.send(newEvent(EventWarning, step, "Broadened search failed", err.Error())) {
			return true
		}
	} else if r.state.SearchTool != nil && r.state.SearchTool.Result != nil && len(search.CandidateFiles) > 0 {
		merged := mergeUnique(r.state.SearchTool.Result.CandidateFiles, search.CandidateFiles)
		r.state.SearchTool.Result.CandidateFiles = merged
		search.Summary = fmt.Sprintf("Broadened search results (token %q): %s", broad, strings.Join(merged, ", "))
		if !r.send(newEvent(EventSearchObserved, step, "Broadened search results", search.Summary)) {
			return true
		}
		r.record(step, "search_broad", search.Summary)
	}

	r.state.BroadenedSearch = true
	r.state.OutlineTool = nil
	return false
}

func (r *sessionRunner) handleOutlineContext(step int) bool {
	req := intelligence.OutlineRequest{
		Task:      r.input.Task,
		Workspace: r.input.Workspace,
		Files:     r.state.SearchTool.Result.CandidateFiles,
	}
	outline, err := r.agent.intelligence.Outline(r.ctx, req)
	if err != nil {
		r.send(newErrorEvent(step, "Outline context", err))
		return true
	}

	r.state.OutlineTool = &OutlineToolState{Request: req, Result: &outline}
	if !r.send(newEvent(EventOutlineObserved, step, "Outline results", outline.Summary)) {
		return true
	}
	if outline.EstimatedTokens > contextOverflowTokens {
		if !r.send(newEvent(
			EventContextWindowWillOverflow,
			step,
			"Context window warning",
			fmt.Sprintf("Estimated context ~%d tokens exceeds threshold — candidate set will be trimmed on next pass.", outline.EstimatedTokens),
		)) {
			return true
		}
	}
	r.record(step, "outline", outline.Summary)
	return false
}

func (r *sessionRunner) handleInspectSymbol(step int, decision Decision) bool {
	call := runtime.Call{
		Tool:      runtime.ToolReadSymbol,
		Workspace: r.input.Workspace,
		Path:      decision.Path,
		Symbol:    decision.Symbol,
	}
	if !r.send(newEvent(EventAction, step, "Runtime action", fmt.Sprintf("Read symbol %s from %s", decision.Symbol, decision.Path))) {
		return true
	}

	result, err := r.agent.runtime.Execute(r.ctx, call)
	if err != nil {
		r.send(newErrorEvent(step, "Read symbol", err))
		return true
	}

	r.state.ReadSymbolTool = &ReadSymbolToolState{Call: call, Result: &result}
	if !r.send(newEvent(EventSymbolObserved, step, "Focused symbol", result.Summary)) {
		return true
	}
	r.record(step, "read_symbol", result.Summary)
	return false
}

func (r *sessionRunner) handleOutlineRelated(step int) bool {
	var relatedFiles []string
	if r.state.OutlineTool != nil && r.state.OutlineTool.Result != nil {
		relatedFiles = r.state.OutlineTool.Result.RelatedFiles
	}

	req := intelligence.OutlineRequest{
		Task:      r.input.Task,
		Workspace: r.input.Workspace,
		Files:     relatedFiles,
	}
	outline, err := r.agent.intelligence.Outline(r.ctx, req)
	if err != nil {
		r.send(newErrorEvent(step, "Outline related files", err))
		return true
	}

	r.state.RelatedOutlineTool = &OutlineToolState{Request: req, Result: &outline}
	if !r.send(newEvent(EventOutlineObserved, step, "Related file outline", outline.Summary)) {
		return true
	}
	r.record(step, "outline_related", outline.Summary)
	return false
}

func (r *sessionRunner) handleRunCommand(step int, decision Decision) bool {
	cmd := decision.Command
	if cmd == "" {
		cmd = chooseValidationCommand(r.state)
	}

	call := runtime.Call{
		Tool:      runtime.ToolShell,
		Workspace: r.input.Workspace,
		Command:   cmd,
		Stream: func(execID, line string) {
			select {
			case r.events <- newEvent(EventObserved, step, execID, line):
			case <-r.ctx.Done():
			}
		},
	}
	if !r.send(newEvent(EventAction, step, "Runtime action", cmd)) {
		return true
	}

	result, err := r.agent.runtime.Execute(r.ctx, call)
	if err != nil {
		if !r.send(newEvent(EventWarning, step, "Command failed", err.Error())) {
			return true
		}
		r.state.ValidationTool = &ValidationToolState{
			Call:   call,
			Result: &runtime.Result{Tool: runtime.ToolShell, Summary: err.Error()},
		}
		return false
	}

	r.state.ValidationTool = &ValidationToolState{Call: call, Result: &result}
	if !r.send(newEvent(EventObserved, step, "Runtime result", result.Summary)) {
		return true
	}
	r.record(step, "runtime", result.Summary)
	return false
}

func (r *sessionRunner) handleWriteFile(step int, decision Decision) bool {
	call := runtime.Call{
		Tool:      runtime.ToolWriteFile,
		Workspace: r.input.Workspace,
		Path:      decision.Path,
		Content:   decision.Content,
	}
	if !r.send(newEvent(EventAction, step, "Runtime action", fmt.Sprintf("Write file %s", decision.Path))) {
		return true
	}

	result, err := r.agent.runtime.Execute(r.ctx, call)
	if err != nil {
		return !r.send(newEvent(EventWarning, step, "Write file failed", err.Error()))
	}

	r.state.WriteFileTool = &WriteFileToolState{Call: call, Result: &result}
	if !r.send(newEvent(EventWriteObserved, step, "File written", result.Summary)) {
		return true
	}
	r.record(step, "write_file", result.Summary)
	return false
}

func (r *sessionRunner) handleEditFile(step int, decision Decision) bool {
	call := runtime.Call{
		Tool:      runtime.ToolEditFile,
		Workspace: r.input.Workspace,
		Path:      decision.Path,
		OldString: decision.OldString,
		NewString: decision.NewString,
	}
	if !r.send(newEvent(EventAction, step, "Runtime action", fmt.Sprintf("Edit file %s", decision.Path))) {
		return true
	}

	result, err := r.agent.runtime.Execute(r.ctx, call)
	if err != nil {
		return !r.send(newEvent(EventWarning, step, "Edit file failed", err.Error()))
	}

	r.state.EditFileTool = &EditFileToolState{Call: call, Result: &result}
	if !r.send(newEvent(EventEditObserved, step, "File edited", result.Summary)) {
		return true
	}
	r.record(step, "edit_file", result.Summary)
	return false
}

func (r *sessionRunner) handleSaveMemory(step int) bool {
	factSummary := summarizeFact(r.state)
	if err := r.agent.memory.SaveFact(memory.Fact{
		Category: "article3-taking-action",
		Summary:  factSummary,
	}); err != nil {
		if !r.send(newEvent(EventWarning, step, "Save fact failed", err.Error())) {
			return true
		}
	}

	r.record(step, "memory", factSummary)
	r.state.MemorySaved = true
	return !r.send(newEvent(EventMemory, step, "Memory updated", factSummary))
}

func (r *sessionRunner) send(event Event) bool {
	select {
	case r.events <- event:
		return true
	case <-r.ctx.Done():
		return false
	}
}

func (r *sessionRunner) record(step int, kind, detail string) {
	if err := r.agent.memory.RecordSessionEvent(r.sessionID, kind, detail); err != nil {
		r.send(newEvent(EventWarning, step, "Memory recording skipped", err.Error()))
	}
}

func summarizeFact(state State) string {
	parts := make([]string, 0, 7)
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
