package main

import (
	"strings"
)

// IdleDetector is a function that checks if pane content indicates Claude is idle.
// It returns true if idle is detected, false otherwise.
type IdleDetector func(paneContent string) bool

// idleDetectors is the list of all detection methods.
// Add new detectors here to extend idle detection capabilities.
var idleDetectors = []IdleDetector{
	detectProceedPrompt,
	// Add more detectors here as needed
}

// detectProceedPrompt checks if the last 10 lines contain "Do you want to proceed?"
func detectProceedPrompt(paneContent string) bool {
	lines := strings.Split(paneContent, "\n")

	// Check last 10 lines
	start := len(lines) - 10
	if start < 0 {
		start = 0
	}

	lastLines := lines[start:]
	for _, line := range lastLines {
		if strings.Contains(line, "Do you want to proceed?") {
			return true
		}
	}
	return false
}

// isIdle runs all idle detectors and returns true if any detector matches.
func isIdle(paneContent string) bool {
	for _, detector := range idleDetectors {
		if detector(paneContent) {
			return true
		}
	}
	return false
}

// getPaneContent captures the content of a tmux pane.
func getPaneContent(target string) (string, error) {
	// Capture all scrollback and visible content
	content, err := tmuxRun("capture-pane", "-p", "-t", target, "-S", "-")
	if err != nil {
		return "", err
	}
	return content, nil
}

// checkIdleTickets checks all active ticket panes for idle signals using WindowStateTracker.
// Returns a list of ticket IDs that are detected as idle.
func (tm *TmuxManager) checkIdleTickets(tickets []Ticket) []string {
	var idleTickets []string

	tm.mu.Lock()
	windowNames := make(map[string]string)
	for name := range tm.windows {
		windowNames[name] = name
	}
	tm.mu.Unlock()

	for _, ticket := range tickets {
		windowName := ticket.ID + "-" + ticket.Status

		// Skip if window doesn't exist
		if _, ok := windowNames[windowName]; !ok {
			continue
		}

		if !tm.windowExists(windowName) {
			continue
		}

		// Capture snapshot and add to state tracker
		target := tm.target(windowName)
		snapshot, err := captureSnapshot(target)
		if err != nil {
			continue
		}

		tm.stateTracker.AddSnapshot(windowName, snapshot)

		// Check if window is idle (StateIdleRed or StateIdleYellow)
		state := tm.stateTracker.GetState(windowName)
		if state == StateIdleRed || state == StateIdleYellow {
			idleTickets = append(idleTickets, ticket.ID)
		}
	}

	return idleTickets
}
