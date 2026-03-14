package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestRuntimeBlocksUnknownCommand(t *testing.T) {
	rt := New(Policy{
		ApprovalMode:    "manual",
		AllowedCommands: []string{"pwd"},
	})

	_, err := rt.Execute(context.Background(), Call{
		Tool:      ToolShell,
		Workspace: ".",
		Command:   "rm -rf /tmp/whatever",
	})
	if err == nil {
		t.Fatal("expected policy error")
	}
	if !strings.Contains(err.Error(), "blocked by runtime policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}
