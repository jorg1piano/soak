package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tmux session and window management for agent spawning.

const tmuxSession = "soak"

// TmuxManager manages tmux windows for spawning Claude agents.
type TmuxManager struct {
	windows      map[string]string   // "ticketID-stage" -> window name
	mu           sync.Mutex          // protects windows map
	selfPath     string
	pipe         *Pipeline
	sessionID    string              // tmux session we're running in
	stateTracker *WindowStateTracker // tracks idle state for windows
}

// NewTmuxManager creates a new TmuxManager for the given pipeline.
func NewTmuxManager(pipe *Pipeline) *TmuxManager {
	self, _ := os.Executable()
	if self == "" {
		self = os.Args[0]
	}
	self, _ = filepath.Abs(self)
	sid, _ := tmuxRun("display-message", "-p", "#{session_id}")
	return &TmuxManager{
		windows:      make(map[string]string),
		selfPath:     self,
		pipe:         pipe,
		sessionID:    sid,
		stateTracker: NewWindowStateTracker(),
	}
}

func tmuxRun(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// target returns a tmux target for a window name within our session.
func (tm *TmuxManager) target(windowName string) string {
	return tm.sessionID + ":" + windowName
}

func (tm *TmuxManager) windowExists(name string) bool {
	_, err := tmuxRun("display-message", "-t", tm.target(name), "-p", "#{window_id}")
	return err == nil
}

func (tm *TmuxManager) HasSession(ticket Ticket) bool {
	windowName := fmt.Sprintf("%s-%s", ticket.ID, ticket.Status)
	tm.mu.Lock()
	_, ok := tm.windows[windowName]
	tm.mu.Unlock()
	if ok {
		return tm.windowExists(windowName)
	}
	return false
}

func (tm *TmuxManager) OpenSession(ticket Ticket) (Ticket, error) {
	return tm.openSession(ticket, true)
}

func (tm *TmuxManager) SpawnSession(ticket Ticket) (Ticket, error) {
	return tm.openSession(ticket, false)
}

func (tm *TmuxManager) openSession(ticket Ticket, switchTo bool) (Ticket, error) {
	stage := tm.pipe.StageByName(ticket.Status)
	if stage == nil || stage.Agent == nil {
		return ticket, fmt.Errorf("no agent for stage %s", ticket.Status)
	}

	windowName := fmt.Sprintf("%s-%s", ticket.ID, ticket.Status)

	// Lock to prevent race conditions when spawning multiple sessions
	tm.mu.Lock()

	// Close any old windows for this ticket from previous stages
	for name := range tm.windows {
		if strings.HasPrefix(name, ticket.ID+"-") && name != windowName {
			if tm.windowExists(name) {
				tmuxRun("kill-window", "-t", tm.target(name))
			}
			delete(tm.windows, name)
			tm.stateTracker.RemoveWindow(name)
		}
	}

	// Check if window already exists
	if _, ok := tm.windows[windowName]; ok {
		tm.mu.Unlock()
		if tm.windowExists(windowName) {
			if switchTo {
				tmuxRun("select-window", "-t", tm.target(windowName))
			}
			// Reset state tracker since this is a reused window for the same ticket/stage
			tm.stateTracker.RemoveWindow(windowName)
			return ticket, nil
		}
		tm.mu.Lock()
		delete(tm.windows, windowName)
		tm.stateTracker.RemoveWindow(windowName)
		tm.mu.Unlock()
		tm.mu.Lock()
	}

	// Mark window as being created (reserve it)
	tm.windows[windowName] = windowName
	tm.mu.Unlock()

	// Execute worktree command if stage defines one and ticket has no worktree yet
	if stage.Worktree != "" && ticket.Worktree == "" {
		wtData := PromptData{
			ID:       ticket.ID,
			Title:    ticket.Title,
			Feedback: ticket.Feedback,
			Kanban:   tm.selfPath,
		}
		wtCmd, err := renderTemplate(stage.Worktree, wtData)
		if err != nil {
			return ticket, fmt.Errorf("render worktree command: %w", err)
		}
		cmd := exec.Command("bash", "-c", wtCmd)
		out, err := cmd.Output()
		if err != nil {
			return ticket, fmt.Errorf("worktree command failed: %w", err)
		}
		wtPath := strings.TrimSpace(string(out))
		if wtPath == "" {
			return ticket, fmt.Errorf("worktree command produced no output")
		}
		if !filepath.IsAbs(wtPath) {
			cwd, _ := os.Getwd()
			wtPath = filepath.Join(cwd, wtPath)
		}
		ticket.Worktree = wtPath
		addTrustedDir(wtPath)
		// Auto-install hooks so idle/ping detection works
		if err := installHooksInDir(wtPath); err != nil {
			// Non-fatal - just log the error
			fmt.Fprintf(os.Stderr, "Warning: could not install hooks in %s: %v\n", wtPath, err)
		}
	}

	// Execute setup command if stage defines one
	if stage.Setup != "" && ticket.Worktree != "" {
		setupData := PromptData{
			ID:       ticket.ID,
			Title:    ticket.Title,
			Feedback: ticket.Feedback,
			Kanban:   tm.selfPath,
			Worktree: ticket.Worktree,
		}
		setupCmd, err := renderTemplate(stage.Setup, setupData)
		if err != nil {
			return ticket, fmt.Errorf("render setup command: %w", err)
		}
		cmd := exec.Command("bash", "-c", setupCmd)
		cmd.Dir = ticket.Worktree
		if err := cmd.Run(); err != nil {
			return ticket, fmt.Errorf("setup command failed: %w", err)
		}
	}

	data := PromptData{
		ID:       ticket.ID,
		Title:    ticket.Title,
		Feedback: ticket.Feedback,
		Kanban:   tm.selfPath,
		Worktree: ticket.Worktree,
	}

	prompt, err := renderTemplate(stage.Agent.Prompt, data)
	if err != nil {
		return ticket, fmt.Errorf("render prompt: %w", err)
	}

	// Create new window with interactive shell (not a script file)
	// This ensures Claude runs in a proper interactive environment where hooks work
	args := []string{"new-window"}
	if !switchTo {
		args = append(args, "-d")
	}
	args = append(args, "-t", tm.sessionID+":", "-n", windowName)

	_, err = tmuxRun(args...)
	if err != nil {
		return ticket, fmt.Errorf("new-window: %w", err)
	}

	// Use send-keys to set up environment and launch Claude
	// This mimics typing commands interactively, which properly sets up hooks
	target := tm.target(windowName)

	// Change to worktree directory
	if ticket.Worktree != "" {
		tmuxRun("send-keys", "-t", target, fmt.Sprintf("cd %q", ticket.Worktree), "C-m")
		time.Sleep(200 * time.Millisecond)
	}

	// Export SOAK_TICKET_ID for hooks
	tmuxRun("send-keys", "-t", target, fmt.Sprintf("export SOAK_TICKET_ID=%q", ticket.ID), "C-m")
	time.Sleep(100 * time.Millisecond)

	// Build Claude command without prompt
	renderedTools := renderTools(stage.Agent.AllowedTools, data)
	promptText := strings.TrimSpace(prompt)

	var claudeCmd string
	if len(renderedTools) > 0 {
		claudeCmd = fmt.Sprintf("claude --allowedTools %q", strings.Join(renderedTools, ","))
	} else {
		claudeCmd = "claude"
	}

	// Launch Claude (without prompt)
	tmuxRun("send-keys", "-t", target, claudeCmd, "C-m")

	// Wait for Claude to show the trust prompt
	time.Sleep(3 * time.Second)

	// Select option 1 and confirm (trust prompt)
	tmuxRun("send-keys", "-t", target, "1")
	time.Sleep(500 * time.Millisecond)
	tmuxRun("send-keys", "-t", target, "C-m")

	// Wait for Claude to fully initialize and show the input prompt
	time.Sleep(5 * time.Second)

	// Now send the prompt using tmux paste buffer (handles multiline correctly)
	if promptText != "" {
		tmuxRun("set-buffer", promptText)
		tmuxRun("paste-buffer", "-t", target)
		time.Sleep(300 * time.Millisecond)
		// Send Enter to submit the prompt
		tmuxRun("send-keys", "-t", target, "C-m")
		time.Sleep(500 * time.Millisecond)
	}

	// Initialize LastPingTime and clear NeedsAttention when spawning
	ticket.LastPingTime = time.Now().Unix()
	ticket.NeedsAttention = false

	// Reset state tracker for this window (fresh Claude session)
	tm.stateTracker.RemoveWindow(windowName)

	return ticket, nil
}

// CleanupTicket kills any existing windows for a ticket (all stages).
// Call this when a ticket is deleted or completed.
func (tm *TmuxManager) CleanupTicket(ticketID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for name := range tm.windows {
		if strings.HasPrefix(name, ticketID+"-") {
			if tm.windowExists(name) {
				tmuxRun("kill-window", "-t", tm.target(name))
			}
			delete(tm.windows, name)
			tm.stateTracker.RemoveWindow(name)
		}
	}
}

// CleanupStale kills windows for stages a ticket is no longer in.
// This handles the case where a CLI move/reject changed the ticket's stage.
func (tm *TmuxManager) CleanupStale(tickets []Ticket) {
	// Build a set of valid windows: "ticketID-currentStage"
	valid := make(map[string]bool)
	for _, t := range tickets {
		valid[fmt.Sprintf("%s-%s", t.ID, t.Status)] = true
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for name := range tm.windows {
		if !valid[name] {
			if tm.windowExists(name) {
				tmuxRun("kill-window", "-t", tm.target(name))
			}
			delete(tm.windows, name)
			tm.stateTracker.RemoveWindow(name)
		}
	}
}

// NeedsAttention returns tickets that have the NeedsAttention flag set.
// The flag is set by the "soak idle" hook when Claude goes idle.
func (tm *TmuxManager) NeedsAttention(tickets []Ticket, store *Store) []Ticket {
	var attention []Ticket

	for i := range tickets {
		ticket := &tickets[i]
		windowName := fmt.Sprintf("%s-%s", ticket.ID, ticket.Status)

		if !tm.windowExists(windowName) {
			continue
		}

		if ticket.NeedsAttention {
			attention = append(attention, *ticket)
		}
	}
	return attention
}


func (tm *TmuxManager) SessionCount() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	count := 0
	for name := range tm.windows {
		if tm.windowExists(name) {
			count++
		} else {
			delete(tm.windows, name)
		}
	}
	return count
}

func (tm *TmuxManager) Cleanup() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for name := range tm.windows {
		tmuxRun("kill-window", "-t", tm.target(name))
		delete(tm.windows, name)
		tm.stateTracker.RemoveWindow(name)
	}
}
