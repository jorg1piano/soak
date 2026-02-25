package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Trust management for Claude Code.
// Claude stores project trust in ~/.claude.json under "projects" keyed by absolute path.
// Each entry has "hasTrustDialogAccepted": true to skip the interactive trust prompt.

// claudeJSONPath returns the absolute path to Claude Code's global settings file.
func claudeJSONPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// loadClaudeJSON reads and parses Claude Code's settings file.
// Returns an empty map if the file doesn't exist, or an error if parsing fails.
func loadClaudeJSON() (map[string]any, error) {
	data, err := os.ReadFile(claudeJSONPath())
	if err != nil {
		return map[string]any{}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// saveClaudeJSON writes the settings map back to Claude Code's settings file.
// The file is formatted with indentation and has mode 0600 (user read/write only).
func saveClaudeJSON(raw map[string]any) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(claudeJSONPath(), append(data, '\n'), 0600)
}

// getProjects extracts or initializes the "projects" map from Claude settings.
// If the "projects" key is missing or malformed, creates a new empty map and stores it.
// This ensures the returned map is always valid and safe to modify.
func getProjects(raw map[string]any) map[string]any {
	val, ok := raw["projects"]
	if !ok {
		projects := make(map[string]any)
		raw["projects"] = projects
		return projects
	}
	projects, ok := val.(map[string]any)
	if !ok {
		projects := make(map[string]any)
		raw["projects"] = projects
		return projects
	}
	return projects
}

// addTrustedDir adds a project entry with hasTrustDialogAccepted: true.
// It copies allowedTools from the parent project entry if one exists.
func addTrustedDir(dir string) error {
	raw, err := loadClaudeJSON()
	if err != nil {
		return err
	}
	projects := getProjects(raw)
	if _, exists := projects[dir]; exists {
		return nil // already has an entry
	}

	// Find the parent project entry to copy permissions from.
	// Walk up from dir looking for a matching project.
	var parentTools []any
	search := dir
	for search != "/" && search != "." {
		search = filepath.Dir(search)
		if entry, ok := projects[search]; ok {
			if m, ok := entry.(map[string]any); ok {
				if tools, ok := m["allowedTools"]; ok {
					if arr, ok := tools.([]any); ok {
						parentTools = arr
					}
				}
			}
			break
		}
	}
	if parentTools == nil {
		parentTools = []any{}
	}

	projects[dir] = map[string]any{
		"allowedTools":           parentTools,
		"hasTrustDialogAccepted": true,
	}
	return saveClaudeJSON(raw)
}

// removeTrustedDir removes the project entry for a directory.
func removeTrustedDir(dir string) error {
	raw, err := loadClaudeJSON()
	if err != nil {
		return err
	}
	projects := getProjects(raw)
	if _, exists := projects[dir]; !exists {
		return nil
	}
	delete(projects, dir)
	return saveClaudeJSON(raw)
}
