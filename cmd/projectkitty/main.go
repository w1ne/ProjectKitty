package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w1ne/projectkitty/internal/agent"
	"github.com/w1ne/projectkitty/internal/intelligence"
	"github.com/w1ne/projectkitty/internal/memory"
	"github.com/w1ne/projectkitty/internal/runtime"
	"github.com/w1ne/projectkitty/internal/ui"
)

func main() {
	task := flag.String("task", "Inspect the repo, identify likely entrypoints, and run the safe validation command.", "task for the agent to execute")
	workspace := flag.String("workspace", ".", "repository path to inspect")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := memory.NewStore(*workspace)
	if err != nil {
		log.Fatal(err)
	}

	app := agent.New(
		agent.NewPlanner(),
		intelligence.New(),
		runtime.New(runtime.Policy{
			ApprovalMode: "on-failure",
			AllowedCommands: []string{
				"go test ./...",
				"go test ./... -run TestDoesNotExist",
				"git status --short",
				"ls",
				"pwd",
			},
		}),
		store,
	)

	program := tea.NewProgram(ui.NewModel(ctx, app, agent.RunInput{
		Task:      *task,
		Workspace: *workspace,
	}))
	if _, err := program.Run(); err != nil {
		log.Fatal(err)
	}
}
