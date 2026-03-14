package agent

import (
	"time"

	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/runtime"
)

type EventKind string

const (
	EventStarted  EventKind = "started"
	EventPlanning EventKind = "planning"
	EventThought  EventKind = "thought"
	EventAction   EventKind = "action"
	EventObserved EventKind = "observed" // Base type

	// Specific observed types (Gemini style)
	EventSearchObserved  EventKind = "search_observed"
	EventOutlineObserved EventKind = "outline_observed"
	EventSymbolObserved  EventKind = "symbol_observed"

	EventMemory   EventKind = "memory"
	EventFinished EventKind = "finished"
	EventErrored  EventKind = "errored"
)

type Event struct {
	Kind      EventKind
	Step      int
	Title     string
	Detail    string
	ErrText   string
	Timestamp time.Time
}

type RunInput struct {
	Task      string
	Workspace string
}

type SearchToolState struct {
	Request intelligence.Request
	Result  *intelligence.SearchResult
}

type OutlineToolState struct {
	Request intelligence.OutlineRequest
	Result  *intelligence.OutlineResult
}

type ReadSymbolToolState struct {
	Call   runtime.Call
	Result *runtime.Result
}

type ValidationToolState struct {
	Call   runtime.Call
	Result *runtime.Result
}

type State struct {
	Input          RunInput
	SearchTool     *SearchToolState
	OutlineTool    *OutlineToolState
	ReadSymbolTool *ReadSymbolToolState
	ValidationTool *ValidationToolState
	MemorySaved    bool
	Steps          int
}

type ActionKind string

const (
	ActionSearchRepository ActionKind = "search_repository"
	ActionOutlineContext   ActionKind = "outline_context"
	ActionInspectSymbol    ActionKind = "inspect_symbol"
	ActionRunCommand       ActionKind = "run_command"
	ActionSaveMemory       ActionKind = "save_memory"
	ActionFinish           ActionKind = "finish"
)

type Decision struct {
	Kind     ActionKind
	Title    string
	Detail   string
	Thoughts string // Added for Gemini-style thought emission
	Command  string
	Path     string
	Symbol   string
}
