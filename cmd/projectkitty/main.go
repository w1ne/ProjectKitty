package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	plain := flag.Bool("plain", false, "run without the Bubble Tea UI and print events to stdout")
	sandbox := flag.String("sandbox", "", "shell sandbox mode: host, auto, or bwrap (overrides PROJECTKITTY_SANDBOX)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := memory.NewStore(*workspace)
	if err != nil {
		log.Fatal(err)
	}

	var planner agent.Planner
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		planner = agent.NewModelPlanner(key)
	} else {
		planner = agent.NewPlanner()
	}

	app := agent.New(
		planner,
		intelligence.New(),
		runtime.New(runtime.Policy{
			ApprovalMode:      "auto",
			InactivityTimeout: 90 * time.Second,
			SandboxMode:       resolveSandboxMode(*sandbox),
		}),
		store,
	)

	input := agent.RunInput{
		Task:      *task,
		Workspace: *workspace,
	}
	if usePlainMode(*plain) {
		if err := runPlain(ctx, os.Stdout, app, input); err != nil {
			log.Fatal(err)
		}
		return
	}

	program := tea.NewProgram(ui.NewModel(ctx, app, input))
	if _, err := program.Run(); err != nil {
		log.Fatal(err)
	}
}

func usePlainMode(force bool) bool {
	if force {
		return true
	}
	return !isTerminal(os.Stdin) || !isTerminal(os.Stdout)
}

func resolveSandboxMode(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("PROJECTKITTY_SANDBOX")
}

func runPlain(ctx context.Context, w io.Writer, app *agent.Agent, input agent.RunInput) error {
	for event := range app.Run(ctx, input) {
		switch event.Kind {
		case agent.EventErrored:
			if _, err := fmt.Fprintf(w, "[%s] %s: %s\n", event.Kind, event.Title, event.ErrText); err != nil {
				return err
			}
			return fmt.Errorf("%s: %s", event.Title, event.ErrText)
		case agent.EventFinished:
			if _, err := fmt.Fprintf(w, "[%s] %s: %s\n", event.Kind, event.Title, event.Detail); err != nil {
				return err
			}
		default:
			detail := event.Detail
			if detail == "" {
				detail = event.Title
			}
			if _, err := fmt.Fprintf(w, "[%s] %s: %s\n", event.Kind, event.Title, detail); err != nil {
				return err
			}
		}
	}
	return nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
