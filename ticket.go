package main

import (
	"crypto/rand"
	"encoding/hex"
)

// Domain types

// Ticket represents a work item in the pipeline.
type Ticket struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Status          string `json:"status"`
	Feedback        string `json:"feedback,omitempty"`
	Worktree        string `json:"worktree,omitempty"`
	NeedsAttention  bool   `json:"needsAttention,omitempty"`
	LastPingTime    int64  `json:"lastPingTime,omitempty"` // Unix timestamp
}

// Configuration constants

const (
	natsPort    = 14222
	natsDataDir = "/tmp/soak"
	portFile    = "/tmp/soak.port"
)

// generateTicketID creates a short random ID for tickets.
// For third-party integrations, you can use their ID instead.
func generateTicketID() string {
	bytes := make([]byte, 4) // 8 character hex string
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
