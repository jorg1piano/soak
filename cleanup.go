package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type cleanupModel struct {
	store         *Store
	pipe          *Pipeline
	tickets       []Ticket
	selected      int
	width         int
	height        int
	err           error
	message       string
	quitting      bool
}

type cleanupKeys struct {
	Up         key.Binding
	Down       key.Binding
	Delete     key.Binding
	DeleteFull key.Binding
	Quit       key.Binding
}

var cleanupKeyMap = cleanupKeys{
	Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Delete:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "clean worktree")),
	DeleteFull: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete ticket")),
	Quit:       key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "quit")),
}

func newCleanupModel(store *Store, pipe *Pipeline) cleanupModel {
	tickets, _ := store.AllTickets()

	// Filter tickets with worktrees
	var withWorktrees []Ticket
	for _, t := range tickets {
		if t.Worktree != "" {
			withWorktrees = append(withWorktrees, t)
		}
	}

	return cleanupModel{
		store:   store,
		pipe:    pipe,
		tickets: withWorktrees,
	}
}

func (m cleanupModel) Init() tea.Cmd {
	return nil
}

func (m cleanupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, cleanupKeyMap.Quit):
			m.quitting = true
			return m, tea.Quit

		case key.Matches(msg, cleanupKeyMap.Up):
			if m.selected > 0 {
				m.selected--
			}
			return m, nil

		case key.Matches(msg, cleanupKeyMap.Down):
			if m.selected < len(m.tickets)-1 {
				m.selected++
			}
			return m, nil

		case key.Matches(msg, cleanupKeyMap.Delete):
			// Clean worktree but keep ticket
			if len(m.tickets) == 0 {
				return m, nil
			}
			t := m.tickets[m.selected]
			cleanupWorktree(t.Worktree)

			t.Worktree = ""
			if err := m.store.PutTicket(t); err != nil {
				m.err = err
			} else {
				m.message = fmt.Sprintf("✓ Cleaned worktree for %s", t.ID)
			}

			// Reload
			tickets, _ := m.store.AllTickets()
			m.tickets = m.tickets[:0]
			for _, t := range tickets {
				if t.Worktree != "" {
					m.tickets = append(m.tickets, t)
				}
			}
			if len(m.tickets) == 0 {
				m.quitting = true
				return m, tea.Quit
			}
			if m.selected >= len(m.tickets) {
				m.selected = len(m.tickets) - 1
			}
			return m, nil

		case key.Matches(msg, cleanupKeyMap.DeleteFull):
			// Delete ticket entirely
			if len(m.tickets) == 0 {
				return m, nil
			}
			t := m.tickets[m.selected]
			cleanupWorktree(t.Worktree)

			if err := m.store.DeleteTicket(t.ID); err != nil {
				m.err = err
			} else {
				m.message = fmt.Sprintf("✓ Deleted ticket %s", t.ID)
			}

			// Reload
			tickets, _ := m.store.AllTickets()
			m.tickets = m.tickets[:0]
			for _, t := range tickets {
				if t.Worktree != "" {
					m.tickets = append(m.tickets, t)
				}
			}
			if len(m.tickets) == 0 {
				m.quitting = true
				return m, tea.Quit
			}
			if m.selected >= len(m.tickets) {
				m.selected = len(m.tickets) - 1
			}
			return m, nil
		}
	}

	return m, nil
}

func (m cleanupModel) View() string {
	if m.quitting {
		if len(m.tickets) == 0 {
			return "✓ All worktrees cleaned up\n"
		}
		return "Cleanup cancelled\n"
	}

	if len(m.tickets) == 0 {
		return "No tickets with worktrees to clean up\n"
	}

	var view string

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	view += titleStyle.Render("=== Soak Cleanup ===") + "\n\n"

	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	for i, t := range m.tickets {
		prefix := "  "
		style := normalStyle
		if i == m.selected {
			prefix = "> "
			style = selectedStyle
		}

		// Check if worktree exists
		wtExists := "✓"
		wtStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
		if _, err := os.Stat(t.Worktree); os.IsNotExist(err) {
			wtExists = "✗"
			wtStyle = errorStyle
		}

		view += style.Render(fmt.Sprintf("%s%s - %s", prefix, t.ID, t.Title)) + "\n"
		view += style.Render(fmt.Sprintf("    Status: %s", m.pipe.Title(t.Status))) + "\n"
		view += style.Render("    Worktree: ") + wtStyle.Render(wtExists) + style.Render(fmt.Sprintf(" %s", t.Worktree)) + "\n"
		if t.Feedback != "" {
			maxLen := 60
			feedback := t.Feedback
			if len(feedback) > maxLen {
				feedback = feedback[:maxLen] + "..."
			}
			view += style.Render(fmt.Sprintf("    Feedback: %s", feedback)) + "\n"
		}
		view += "\n"
	}

	if m.message != "" {
		view += lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(m.message) + "\n"
	}

	if m.err != nil {
		view += errorStyle.Render(fmt.Sprintf("Error: %v", m.err)) + "\n"
	}

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	view += helpStyle.Render("↑/↓: Navigate  |  Enter: Clean worktree  |  d: Delete ticket  |  q/Esc: Quit") + "\n"

	return view
}

func runCleanupUI(pipe *Pipeline) {
	store, err := NewClientStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to soak server: %v\n", err)
		fmt.Fprintf(os.Stderr, "Is the soak board running?\n")
		os.Exit(1)
	}
	defer store.Close()

	m := newCleanupModel(store, pipe)

	if len(m.tickets) == 0 {
		fmt.Println("No tickets with worktrees to clean up")
		return
	}

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
