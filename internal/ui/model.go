package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/w1ne/projectkitty/internal/agent"
)

type eventMsg struct {
	event agent.Event
}

type model struct {
	ctx      context.Context
	app      *agent.Agent
	input    agent.RunInput
	events   <-chan agent.Event
	spinner  spinner.Model
	logs     []string
	done     bool
	errText  string
	lastStep string
}

func NewModel(ctx context.Context, app *agent.Agent, input agent.RunInput) tea.Model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))

	return &model{
		ctx:     ctx,
		app:     app,
		input:   input,
		spinner: s,
		logs: []string{
			"Subsystems: planner | intelligence | runtime | memory | UI",
			"Article 2 scope: focused code reading before validation",
		},
	}
}

func (m *model) Init() tea.Cmd {
	m.events = m.app.Run(m.ctx, m.input)
	return tea.Batch(m.spinner.Tick, waitForEvent(m.events))
}

func waitForEvent(events <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return nil
		}
		return eventMsg{event: event}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case eventMsg:
		m.lastStep = msg.event.Title
		m.appendLog(msg.event)
		if msg.event.Kind == agent.EventErrored {
			m.done = true
			m.errText = msg.event.ErrText
			return m, tea.Quit
		}
		if msg.event.Kind == agent.EventFinished {
			m.done = true
			return m, tea.Quit
		}
		return m, waitForEvent(m.events)
	}

	return m, nil
}

func (m *model) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("ProjectKitty")
	subtitle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("Open-source coding agent foundations")
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("110"))

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(subtitle)
	b.WriteString("\n\n")
	if m.errText != "" {
		b.WriteString("Status: stopped with an error\n")
	} else if m.done {
		b.WriteString("Status: complete\n")
	} else {
		b.WriteString(fmt.Sprintf("Status: %s %s\n", m.spinner.View(), statusStyle.Render(m.lastStep)))
	}
	b.WriteString(fmt.Sprintf("Task: %s\n", m.input.Task))
	b.WriteString(fmt.Sprintf("Workspace: %s\n\n", m.input.Workspace))

	for _, line := range m.logs {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	if m.errText != "" {
		b.WriteString("\nError: ")
		b.WriteString(m.errText)
		b.WriteString("\n")
	}

	b.WriteString("\nPress q to exit.\n")
	return b.String()
}

func (m *model) appendLog(event agent.Event) {
	label := string(event.Kind)
	line := event.Title
	if event.Detail != "" {
		line = line + ": " + event.Detail
	}
	m.logs = append(m.logs, fmt.Sprintf("[%s] %s", label, line))
}
