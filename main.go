package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

func findPipelineConfig() string {
	// Look for soak.yaml next to the binary, then in cwd
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), "soak.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if _, err := os.Stat("soak.yaml"); err == nil {
		return "soak.yaml"
	}
	return ""
}

func main() {
	configPath := findPipelineConfig()
	if configPath == "" {
		log.Fatal("soak.yaml not found (looked next to binary and in cwd)")
	}

	pipe, err := LoadPipeline(configPath)
	if err != nil {
		log.Fatalf("Failed to load pipeline: %v", err)
	}

	// CLI subcommand mode
	if len(os.Args) > 1 {
		cliCommands := map[string]bool{
			"create": true, "move": true, "reject": true, "status": true,
			"list": true, "whoami": true, "ping": true, "idle": true,
			"install-hooks": true, "subscribe": true,
		}
		if cliCommands[os.Args[1]] {
			runCLI(os.Args[1:], pipe)
			return
		}
	}

	// TUI board mode
	if os.Getenv("TMUX") == "" {
		fmt.Println("Not inside tmux. Starting a tmux session...")
		self, _ := os.Executable()
		if self == "" {
			self = os.Args[0]
		}
		cmd := exec.Command("tmux", "new-session", "-s", tmuxSession, self)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("tmux: %v", err)
		}
		return
	}

	store, err := NewServerStore()
	if err != nil {
		log.Fatalf("Failed to start store: %v", err)
	}
	defer store.Close()

	tmux := NewTmuxManager(pipe)

	p := tea.NewProgram(initialModel(store, tmux, pipe), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
