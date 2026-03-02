package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func cleanupWorktree(path string) {
	if path == "" {
		return
	}
	removeTrustedDir(path)
	cmd := exec.Command("git", "worktree", "remove", "--force", path)
	if err := cmd.Run(); err != nil {
		os.RemoveAll(path)
	}
}

// Event handlers for the Bubble Tea update loop.

func (m model) handleTick() (tea.Model, tea.Cmd) {
	// Skip heavy operations when in interactive input modes
	if m.createMode || m.rejectMode {
		return m, tickCmd()
	}

	// Run all blocking operations in background
	store := m.store
	tmux := m.tmux
	pipe := m.pipe

	return m, tea.Batch(
		tickCmd(),
		func() tea.Msg {
			// Reload tickets
			tickets, _ := store.AllTickets()

			// Cleanup stale windows (fast operation)
			tmux.CleanupStale(tickets)

			// Check for idle tickets
			idleTicketIDs := tmux.checkIdleTickets(tickets)
			idleSet := make(map[string]bool)
			for _, id := range idleTicketIDs {
				idleSet[id] = true
			}

			// Update NeedsAttention flag for all tickets
			// If a ticket has no window, clear NeedsAttention
			for _, ticket := range tickets {
				shouldNeedAttention := idleSet[ticket.ID]
				// Clear NeedsAttention if ticket has no active window
				if !tmux.HasSession(ticket) {
					shouldNeedAttention = false
				}
				if ticket.NeedsAttention != shouldNeedAttention {
					ticket.NeedsAttention = shouldNeedAttention
					store.PutTicket(ticket)
				}
			}

			// Auto-spawn sessions concurrently for tickets in auto-spawn stages
			type result struct {
				ticket  Ticket
				updated Ticket
				err     error
			}

			results := make(chan result, len(tickets))
			var wg sync.WaitGroup

			for _, t := range tickets {
				if pipe.IsAutoSpawn(t.Status) && !tmux.HasSession(t) {
					wg.Add(1)
					ticket := t // Capture loop variable
					go func() {
						defer wg.Done()
						updated, err := tmux.SpawnSession(ticket)
						results <- result{ticket: ticket, updated: updated, err: err}
						if err == nil {
							if updated.Worktree != ticket.Worktree || updated.LastPingTime != ticket.LastPingTime {
								store.PutTicket(updated)
							}
						}
					}()
				}
			}

			go func() {
				wg.Wait()
				close(results)
			}()

			// Collect spawn results
			var spawned []spawnResult
			for r := range results {
				spawned = append(spawned, spawnResult{
					ticketID:   r.ticket.ID,
					stageName:  r.ticket.Status,
					stageTitle: pipe.Title(r.ticket.Status),
					err:        r.err,
				})
			}

			// Get session count
			sessionCount := tmux.SessionCount()

			return tickCompleteMsg{sessionCount: sessionCount, spawned: spawned}
		},
	)
}

func (m model) handleCreateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ticketTypes := []string{"story", "bug", "feature", "chore", "task"}

	if m.createStep == 0 {
		// Step 0: Select ticket type
		switch msg.Type {
		case tea.KeyUp, tea.KeyCtrlK:
			if m.createTypeSelect > 0 {
				m.createTypeSelect--
			}
			return m, nil
		case tea.KeyDown, tea.KeyCtrlJ:
			if m.createTypeSelect < len(ticketTypes)-1 {
				m.createTypeSelect++
			}
			return m, nil
		case tea.KeyEnter:
			m.createStep = 1
			m.createTitleInput.SetValue("")
			m.createTitleInput.Focus()
			m.status = fmt.Sprintf("Creating %s ticket — enter title and press Enter (Esc to cancel)", ticketTypes[m.createTypeSelect])
			return m, m.createTitleInput.Cursor.BlinkCmd()
		case tea.KeyEsc:
			m.createMode = false
			m.createStep = 0
			m.createTypeSelect = 0
			m.status = "Ticket creation cancelled"
			return m, nil
		}
	} else {
		// Step 1: Enter title
		switch msg.Type {
		case tea.KeyEnter:
			title := m.createTitleInput.Value()
			if title == "" {
				m.status = "Title cannot be empty"
				return m, nil
			}
			ticketType := ticketTypes[m.createTypeSelect]
			firstStage := m.pipe.Stages[0].Name

			// Use createTicket command if configured, otherwise generate random ID
			ticketID := generateTicketID()
			if m.pipe.CreateTicket != "" {
				cmdStr, err := renderCreateTicketTemplate(m.pipe.CreateTicket, CreateTicketData{
					Type:  ticketType,
					Title: title,
				})
				if err != nil {
					m.status = fmt.Sprintf("Template error: %v", err)
					return m, nil
				}
				cmd := exec.Command("bash", "-c", strings.TrimSpace(cmdStr))
				out, err := cmd.Output()
				if err != nil {
					m.status = fmt.Sprintf("createTicket failed: %v", err)
					return m, nil
				}
				externalID := strings.TrimSpace(string(out))
				if externalID != "" {
					ticketID = externalID
				}
			}

			t := Ticket{
				ID:     ticketID,
				Title:  fmt.Sprintf("[%s] %s", ticketType, title),
				Status: firstStage,
			}
			if err := m.store.PutTicket(t); err != nil {
				m.err = err
			}
			m.reload()
			m.createMode = false
			m.createStep = 0
			m.createTypeSelect = 0
			m.createTitleInput.SetValue("")
			m.status = fmt.Sprintf("Created %s: [%s] %s", t.ID, ticketType, title)
			return m, nil
		case tea.KeyEsc:
			m.createMode = false
			m.createStep = 0
			m.createTypeSelect = 0
			m.createTitleInput.SetValue("")
			m.status = "Ticket creation cancelled"
			return m, nil
		default:
			// Let the text input handle the key
			var cmd tea.Cmd
			m.createTitleInput, cmd = m.createTitleInput.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m model) handleRejectInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		reason := m.rejectInput.Value()
		if reason == "" {
			reason = "Rejected by human reviewer"
		}
		t, err := m.store.GetTicket(m.rejectID)
		if err == nil {
			target, ok := m.pipe.RejectTarget(t.Status)
			if ok {
				old := t.Status
				m.tmux.CleanupTicket(t.ID)
				t.Status = target
				t.Feedback = reason
				if err := m.store.PutTicket(*t); err != nil {
					m.err = err
				}
				m.reload()
				m.status = fmt.Sprintf("Rejected %s: %s -> %s", t.ID, m.pipe.Title(old), m.pipe.Title(target))
			}
		}
		m.rejectMode = false
		m.rejectInput.SetValue("")
		return m, nil
	case tea.KeyEsc:
		m.rejectMode = false
		m.rejectInput.SetValue("")
		m.status = "Reject cancelled"
		return m, nil
	default:
		var cmd tea.Cmd
		m.rejectInput, cmd = m.rejectInput.Update(msg)
		return m, cmd
	}
}

func (m model) handleQuit() (tea.Model, tea.Cmd) {
	m.tmux.Cleanup()
	return m, tea.Quit
}

func (m model) handleOpen() (tea.Model, tea.Cmd) {
	// Open Claude session for selected ticket
	return m.handleClaude()
}

func (m model) handleNew() (tea.Model, tea.Cmd) {
	m.createMode = true
	m.createStep = 0
	m.createTypeSelect = 0
	m.status = "Select ticket type (↑/↓ to navigate, Enter to select, Esc to cancel)"
	return m, nil
}

func (m model) handleClaude() (tea.Model, tea.Cmd) {
	t := m.selectedTicket()
	if t == nil {
		m.status = "No ticket selected"
		return m, nil
	}
	if !m.pipe.HasAgent(t.Status) {
		m.status = fmt.Sprintf("%s in %s (no agent)", t.ID, m.pipe.Title(t.Status))
		return m, nil
	}

	ticket := *t
	m.status = fmt.Sprintf("Opening %s agent for %s...", m.pipe.Title(ticket.Status), ticket.ID)

	// Run tmux commands in background to avoid blocking UI
	return m, func() tea.Msg {
		updated, err := m.tmux.OpenSession(ticket)
		return claudeOpenedMsg{ticket: ticket, updated: updated, err: err}
	}
}

func (m model) handleApprove() (tea.Model, tea.Cmd) {
	t := m.selectedTicket()
	if t == nil {
		m.status = "No ticket selected"
	} else {
		next, ok := m.pipe.NextStage(t.Status)
		stage := m.pipe.StageByName(t.Status)
		if !ok {
			m.status = fmt.Sprintf("%s is in final stage", t.ID)
		} else if stage.Agent != nil {
			m.status = "Approve is for human stages (use enter to advance)"
		} else {
			old := t.Status
			m.tmux.CleanupTicket(t.ID)
			t.Status = next
			t.Feedback = ""
			// Cleanup worktree if moved to final stage
			if _, hasNext := m.pipe.NextStage(next); !hasNext && t.Worktree != "" {
				cleanupWorktree(t.Worktree)
				t.Worktree = ""
			}
			if err := m.store.PutTicket(*t); err != nil {
				m.err = err
			}
			m.reload()
			m.status = fmt.Sprintf("Approved %s: %s -> %s", t.ID, m.pipe.Title(old), m.pipe.Title(next))
		}
	}
	return m, nil
}

func (m model) handleReject() (tea.Model, tea.Cmd) {
	t := m.selectedTicket()
	if t == nil {
		m.status = "No ticket selected"
	} else {
		_, ok := m.pipe.RejectTarget(t.Status)
		if !ok {
			m.status = fmt.Sprintf("Cannot reject from %s", m.pipe.Title(t.Status))
		} else {
			m.rejectMode = true
			m.rejectID = t.ID
			m.rejectInput.SetValue("")
			m.rejectInput.Focus()
			m.status = fmt.Sprintf("Rejecting %s — type reason and press Enter (Esc to cancel)", t.ID)
			return m, m.rejectInput.Cursor.BlinkCmd()
		}
	}
	return m, nil
}

func (m model) handleDelete() (tea.Model, tea.Cmd) {
	t := m.selectedTicket()
	if t != nil {
		m.tmux.CleanupTicket(t.ID)
		cleanupWorktree(t.Worktree)
		if err := m.store.DeleteTicket(t.ID); err != nil {
			m.err = err
		}
		m.reload()
		m.status = fmt.Sprintf("Deleted %s", t.ID)
	}
	return m, nil
}

func (m model) handleEditor() (tea.Model, tea.Cmd) {
	dir := "."
	if t := m.selectedTicket(); t != nil && t.Worktree != "" {
		dir = t.Worktree
	}
	cmd := exec.Command("open", "-a", "Visual Studio Code", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		m.status = fmt.Sprintf("VS Code error: %v (%s)", err, strings.TrimSpace(string(out)))
	} else {
		m.status = fmt.Sprintf("Opened VS Code in %s", dir)
	}
	return m, nil
}

func (m model) handleShell() (tea.Model, tea.Cmd) {
	m.status = "Opening shell tab..."
	tmux := m.tmux

	return m, func() tea.Msg {
		windowName := "shell"
		var err error
		if !tmux.windowExists(windowName) {
			_, err = tmuxRun("new-window", "-d", "-t", tmux.sessionID+":", "-n", windowName)
		} else {
			_, err = tmuxRun("select-window", "-t", tmux.target(windowName))
		}
		return shellOpenedMsg{err: err}
	}
}

func (m model) handleOpenTicket() (tea.Model, tea.Cmd) {
	if m.pipe.OpenTicket == "" {
		m.status = "No openTicket configured in soak.yaml"
		return m, nil
	}
	t := m.selectedTicket()
	if t == nil {
		m.status = "No ticket selected"
		return m, nil
	}
	url, err := renderTemplate(m.pipe.OpenTicket, PromptData{
		ID:       t.ID,
		Title:    t.Title,
		Feedback: t.Feedback,
	})
	if err != nil {
		m.status = fmt.Sprintf("Template error: %v", err)
		return m, nil
	}
	url = strings.TrimSpace(url)
	cmd := exec.Command("open", url)
	if err := cmd.Run(); err != nil {
		m.status = fmt.Sprintf("Failed to open: %v", err)
	} else {
		m.status = fmt.Sprintf("Opened %s", url)
	}
	return m, nil
}

func (m model) handleCopyTicket() (tea.Model, tea.Cmd) {
	if m.pipe.CopyTicket == "" {
		m.status = "No copyTicket configured in soak.yaml"
		return m, nil
	}
	t := m.selectedTicket()
	if t == nil {
		m.status = "No ticket selected"
		return m, nil
	}
	rendered, err := renderTemplate(m.pipe.CopyTicket, PromptData{
		ID:       t.ID,
		Title:    t.Title,
		Feedback: t.Feedback,
	})
	if err != nil {
		m.status = fmt.Sprintf("Template error: %v", err)
		return m, nil
	}
	rendered = strings.TrimSpace(rendered)
	cmd := exec.Command("sh", "-c", rendered)
	if err := cmd.Run(); err != nil {
		m.status = fmt.Sprintf("Copy failed: %v", err)
	} else {
		m.status = fmt.Sprintf("Copied link for #%s", t.ID)
	}
	return m, nil
}

func (m model) handleImport() (tea.Model, tea.Cmd) {
	if m.pipe.ListTickets == "" {
		m.status = "No listTickets configured in soak.yaml"
		return m, nil
	}
	m.status = "Fetching tickets..."
	cmdStr := m.pipe.ListTickets
	return m, func() tea.Msg {
		cmd := exec.Command("bash", "-c", strings.TrimSpace(cmdStr))
		out, err := cmd.Output()
		if err != nil {
			return listTicketsMsg{err: err}
		}
		var tickets []ExternalTicket
		if err := json.Unmarshal(out, &tickets); err != nil {
			return listTicketsMsg{err: fmt.Errorf("parse JSON: %w", err)}
		}
		// Filter to ticket kind (treat entries without kind as tickets)
		var filtered []ExternalTicket
		for _, t := range tickets {
			if t.Kind == "" || t.Kind == "ticket" {
				filtered = append(filtered, t)
			}
		}
		return listTicketsMsg{tickets: filtered}
	}
}

func (m model) handleImportInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp, tea.KeyCtrlK:
		if m.importSelect > 0 {
			m.importSelect--
		}
		return m, nil
	case tea.KeyDown, tea.KeyCtrlJ:
		if m.importSelect < len(m.importList)-1 {
			m.importSelect++
		}
		return m, nil
	case tea.KeyEnter:
		ext := m.importList[m.importSelect]
		firstStage := m.pipe.Stages[0].Name
		t := Ticket{
			ID:     fmt.Sprintf("%d", ext.ID),
			Title:  ext.Title,
			Status: firstStage,
		}
		if err := m.store.PutTicket(t); err != nil {
			m.err = err
		}
		m.reload()
		m.importMode = false
		m.importList = nil
		m.status = fmt.Sprintf("Imported #%d: %s", ext.ID, ext.Title)
		return m, nil
	case tea.KeyEsc:
		m.importMode = false
		m.importList = nil
		m.status = "Import cancelled"
		return m, nil
	}
	return m, nil
}

func (m model) handleFreeClaude() (tea.Model, tea.Cmd) {
	m.status = "Opening free Claude session..."
	tmux := m.tmux

	return m, func() tea.Msg {
		windowName := "claude-free"
		// Try to select existing window first
		_, err := tmuxRun("select-window", "-t", tmux.target(windowName))
		if err != nil {
			// Window doesn't exist, create it
			_, err = tmuxRun("new-window", "-t", tmux.sessionID+":", "-n", windowName, "claude")
		}
		return freeClaudeOpenedMsg{err: err}
	}
}

func (m model) handleNavigation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Left):
		if m.col > 0 {
			m.col--
			m.clampRow()
		}
	case key.Matches(msg, m.keys.Right):
		if m.col < len(m.pipe.Stages)-1 {
			m.col++
			m.clampRow()
		}
	case key.Matches(msg, m.keys.Up):
		if m.row > 0 {
			m.row--
		}
	case key.Matches(msg, m.keys.Down):
		items := m.ticketsInColumn(m.currentStageName())
		if m.row < len(items)-1 {
			m.row++
		}
	}
	return m, nil
}
