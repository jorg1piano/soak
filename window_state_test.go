package main

import (
	"testing"
)

func TestSnapshotBuffer_InitialState(t *testing.T) {
	tracker := NewWindowStateTracker()

	// New window should be in Working state (not enough data)
	state := tracker.GetState("test-window")
	if state != StateWorking {
		t.Errorf("Expected StateWorking for new window, got %v", state)
	}
}

func TestSnapshotBuffer_WorkingState(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Add 5 different snapshots
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("test-window", string(rune('a'+i)))
	}

	state := tracker.GetState("test-window")
	if state != StateWorking {
		t.Errorf("Expected StateWorking with changing content, got %v", state)
	}
}

func TestSnapshotBuffer_IdleRedState(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Add 5 identical snapshots (idle detected)
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("test-window", "identical")
	}

	state := tracker.GetState("test-window")
	if state != StateIdleRed {
		t.Errorf("Expected StateIdleRed with 5 identical snapshots, got %v", state)
	}
}

func TestSnapshotBuffer_IdleYellowState(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Add 5 identical snapshots
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("test-window", "identical")
	}

	// Mark as visited
	tracker.MarkVisited("test-window")

	state := tracker.GetState("test-window")
	if state != StateIdleYellow {
		t.Errorf("Expected StateIdleYellow after visiting idle window, got %v", state)
	}
}

func TestSnapshotBuffer_IdleToWorking(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Add 5 identical snapshots (becomes idle)
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("test-window", "identical")
	}

	// Mark as visited (becomes yellow)
	tracker.MarkVisited("test-window")

	if tracker.GetState("test-window") != StateIdleYellow {
		t.Fatal("Expected StateIdleYellow before activity")
	}

	// Add new content (activity detected)
	tracker.AddSnapshot("test-window", "new-content")

	state := tracker.GetState("test-window")
	if state != StateWorking {
		t.Errorf("Expected StateWorking after new content, got %v", state)
	}
}

func TestSnapshotBuffer_RedToYellowTransition(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Become idle (red)
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("test-window", "idle")
	}

	if tracker.GetState("test-window") != StateIdleRed {
		t.Fatal("Expected StateIdleRed initially")
	}

	// Visit window
	tracker.MarkVisited("test-window")

	state := tracker.GetState("test-window")
	if state != StateIdleYellow {
		t.Errorf("Expected StateIdleYellow after visit, got %v", state)
	}
}

func TestSnapshotBuffer_BufferSize(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Add 15 snapshots (buffer size is 10)
	for i := 0; i < 15; i++ {
		tracker.AddSnapshot("test-window", string(rune('a'+i)))
	}

	tracker.mu.RLock()
	buffer := tracker.buffers["test-window"]
	tracker.mu.RUnlock()

	if len(buffer.snapshots) != 10 {
		t.Errorf("Expected buffer size of 10, got %d", len(buffer.snapshots))
	}
}

func TestSnapshotBuffer_ExactlyFiveIdentical(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Add 4 different, then 5 identical
	tracker.AddSnapshot("test-window", "a")
	tracker.AddSnapshot("test-window", "b")
	tracker.AddSnapshot("test-window", "c")
	tracker.AddSnapshot("test-window", "d")

	// Now add 5 identical
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("test-window", "idle")
	}

	state := tracker.GetState("test-window")
	if state != StateIdleRed {
		t.Errorf("Expected StateIdleRed when last 5 are identical, got %v", state)
	}
}

func TestSnapshotBuffer_FourIdenticalNotIdle(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Add only 4 identical snapshots (not enough to be idle)
	for i := 0; i < 4; i++ {
		tracker.AddSnapshot("test-window", "same")
	}

	state := tracker.GetState("test-window")
	if state != StateWorking {
		t.Errorf("Expected StateWorking with only 4 identical snapshots, got %v", state)
	}
}

func TestSnapshotBuffer_ClearVisitedOnActivity(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Become idle and visit
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("test-window", "idle")
	}
	tracker.MarkVisited("test-window")

	// Clear visited flag
	tracker.ClearVisited("test-window")

	// Should still be idle, but now red (not visited)
	state := tracker.GetState("test-window")
	if state != StateIdleRed {
		t.Errorf("Expected StateIdleRed after clearing visited flag, got %v", state)
	}
}

func TestSnapshotBuffer_IsWindowIdle(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Working state - not idle
	tracker.AddSnapshot("test-window", "a")
	tracker.AddSnapshot("test-window", "b")
	tracker.AddSnapshot("test-window", "c")
	tracker.AddSnapshot("test-window", "d")
	tracker.AddSnapshot("test-window", "e")

	if tracker.isWindowIdle("test-window") {
		t.Error("Expected isWindowIdle to return false for working window")
	}

	// Become idle (red)
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("test-window", "idle")
	}

	if !tracker.isWindowIdle("test-window") {
		t.Error("Expected isWindowIdle to return true for idle red window")
	}

	// Visit (yellow)
	tracker.MarkVisited("test-window")

	if !tracker.isWindowIdle("test-window") {
		t.Error("Expected isWindowIdle to return true for idle yellow window")
	}
}

func TestSnapshotBuffer_MultipleWindows(t *testing.T) {
	tracker := NewWindowStateTracker()

	// Window 1: idle red
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("window1", "idle")
	}

	// Window 2: idle yellow
	for i := 0; i < 5; i++ {
		tracker.AddSnapshot("window2", "idle")
	}
	tracker.MarkVisited("window2")

	// Window 3: working
	tracker.AddSnapshot("window3", "a")
	tracker.AddSnapshot("window3", "b")
	tracker.AddSnapshot("window3", "c")
	tracker.AddSnapshot("window3", "d")
	tracker.AddSnapshot("window3", "e")

	if tracker.GetState("window1") != StateIdleRed {
		t.Error("Window 1 should be StateIdleRed")
	}
	if tracker.GetState("window2") != StateIdleYellow {
		t.Error("Window 2 should be StateIdleYellow")
	}
	if tracker.GetState("window3") != StateWorking {
		t.Error("Window 3 should be StateWorking")
	}
}
