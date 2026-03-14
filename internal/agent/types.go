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
	EventAction   EventKind = "action"
	EventObserved EventKind = "observed"
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

type State struct {
	Input          RunInput
	Context        *intelligence.ContextSnapshot
	LastToolResult *runtime.Result
	MemorySaved    bool
	Steps          int
}

type ActionKind string

const (
	ActionGatherContext ActionKind = "gather_context"
	ActionRunCommand    ActionKind = "run_command"
	ActionSaveMemory    ActionKind = "save_memory"
	ActionFinish        ActionKind = "finish"
)

type Decision struct {
	Kind    ActionKind
	Title   string
	Detail  string
	Command string
}
