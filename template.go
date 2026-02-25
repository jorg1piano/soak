package main

import (
	"bytes"
	"text/template"
)

// Template rendering for agent prompts

// PromptData contains template variables for rendering agent prompts.
type PromptData struct {
	ID       string
	Title    string
	Feedback string
	Kanban   string // path to the soak binary
	Worktree string // worktree path (empty if not set)
}

func renderTemplate(tmpl string, data PromptData) (string, error) {
	t, err := template.New("prompt").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func renderTools(tools []string, data PromptData) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		// Simple template rendering for tool strings
		r, err := renderTemplate(tool, data)
		if err != nil {
			out[i] = tool
		} else {
			out[i] = r
		}
	}
	return out
}
