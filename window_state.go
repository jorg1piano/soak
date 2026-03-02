package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// WindowState represents the idle detection state for a tmux window
type WindowState int

const (
	StateWorking WindowState = iota // Content is changing
	StateIdleRed                     // Idle and needs attention (not visited)
	StateIdleYellow                  // Idle and acknowledged (visited)
)

// SnapshotBuffer maintains a rolling buffer of content snapshots for a window
type SnapshotBuffer struct {
	snapshots []string // Last N content hashes
	maxSize   int      // Buffer size (10 snapshots)
	visited   bool     // Has the user visited this window while idle?
}

// WindowStateTracker tracks idle state for all windows
type WindowStateTracker struct {
	mu      sync.RWMutex
	buffers map[string]*SnapshotBuffer // windowName -> buffer
}

// NewWindowStateTracker creates a new state tracker
func NewWindowStateTracker() *WindowStateTracker {
	return &WindowStateTracker{
		buffers: make(map[string]*SnapshotBuffer),
	}
}

// captureSnapshot captures the current content of a window and returns its hash
func captureSnapshot(windowTarget string) (string, error) {
	content, err := tmuxRun("capture-pane", "-p", "-t", windowTarget, "-S", "-10")
	if err != nil {
		return "", err
	}

	// Hash the content to save memory
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:]), nil
}

// AddSnapshot adds a new snapshot to the buffer for a window
func (wst *WindowStateTracker) AddSnapshot(windowName string, snapshot string) {
	wst.mu.Lock()
	defer wst.mu.Unlock()

	buffer, exists := wst.buffers[windowName]
	if !exists {
		buffer = &SnapshotBuffer{
			snapshots: make([]string, 0, 10),
			maxSize:   10,
			visited:   false,
		}
		wst.buffers[windowName] = buffer
	}

	// Add new snapshot
	buffer.snapshots = append(buffer.snapshots, snapshot)

	// Keep only last maxSize snapshots
	if len(buffer.snapshots) > buffer.maxSize {
		buffer.snapshots = buffer.snapshots[1:]
	}
}

// GetState determines the current state of a window based on its snapshot buffer
func (wst *WindowStateTracker) GetState(windowName string) WindowState {
	wst.mu.RLock()
	defer wst.mu.RUnlock()

	buffer, exists := wst.buffers[windowName]
	if !exists || len(buffer.snapshots) < 5 {
		return StateWorking // Not enough data yet
	}

	// Check if last 5 snapshots are identical (idle detection)
	lastFive := buffer.snapshots[len(buffer.snapshots)-5:]
	allSame := true
	for i := 1; i < len(lastFive); i++ {
		if lastFive[i] != lastFive[0] {
			allSame = false
			break
		}
	}

	if !allSame {
		return StateWorking
	}

	// Window is idle - check if visited
	if buffer.visited {
		return StateIdleYellow
	}
	return StateIdleRed
}

// MarkVisited marks a window as visited (user has acknowledged its idle state)
func (wst *WindowStateTracker) MarkVisited(windowName string) {
	wst.mu.Lock()
	defer wst.mu.Unlock()

	buffer, exists := wst.buffers[windowName]
	if !exists {
		buffer = &SnapshotBuffer{
			snapshots: make([]string, 0, 10),
			maxSize:   10,
			visited:   true,
		}
		wst.buffers[windowName] = buffer
	} else {
		buffer.visited = true
	}
}

// ClearVisited clears the visited flag (called when window becomes active again)
func (wst *WindowStateTracker) ClearVisited(windowName string) {
	wst.mu.Lock()
	defer wst.mu.Unlock()

	if buffer, exists := wst.buffers[windowName]; exists {
		buffer.visited = false
	}
}

// isWindowIdle checks if a window is idle based on its current state
func (wst *WindowStateTracker) isWindowIdle(windowName string) bool {
	state := wst.GetState(windowName)
	return state == StateIdleRed || state == StateIdleYellow
}

// RemoveWindow removes a window's state buffer from the tracker
func (wst *WindowStateTracker) RemoveWindow(windowName string) {
	wst.mu.Lock()
	defer wst.mu.Unlock()
	delete(wst.buffers, windowName)
}
