package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

func runCLI(args []string, pipe *Pipeline) {
	if len(args) == 0 {
		fmt.Println("Usage: soak <move|reject|status|list> [args...]")
		os.Exit(1)
	}

	// Commands that don't need NATS connection
	if args[0] == "install-hooks" {
		global := len(args) > 1 && args[1] == "--global"
		installHooks(global)
		return
	}

	store, err := NewClientStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to soak server: %v\n", err)
		fmt.Fprintf(os.Stderr, "Is the soak board running?\n")
		os.Exit(1)
	}
	defer store.Close()

	switch args[0] {
	case "move":
		if len(args) < 2 {
			fmt.Println("Usage: soak move <ticket-id>")
			os.Exit(1)
		}
		t, err := store.GetTicket(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: ticket %s not found: %v\n", args[1], err)
			os.Exit(1)
		}
		next, ok := pipe.NextStage(t.Status)
		if !ok {
			fmt.Printf("Ticket %s is already in %s (final state)\n", t.ID, pipe.Title(t.Status))
			return
		}
		old := t.Status
		t.Status = next
		t.Feedback = ""
		// Cleanup worktree if moved to final stage
		if _, hasNext := pipe.NextStage(next); !hasNext && t.Worktree != "" {
			cleanupWorktree(t.Worktree)
			t.Worktree = ""
		}
		if err := store.PutTicket(*t); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating ticket: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Moved %s: %s -> %s\n", t.ID, pipe.Title(old), pipe.Title(next))

	case "reject":
		if len(args) < 2 {
			fmt.Println("Usage: soak reject <ticket-id> [reason]")
			os.Exit(1)
		}
		t, err := store.GetTicket(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: ticket %s not found: %v\n", args[1], err)
			os.Exit(1)
		}
		target, ok := pipe.RejectTarget(t.Status)
		if !ok {
			fmt.Fprintf(os.Stderr, "Cannot reject ticket in %s\n", pipe.Title(t.Status))
			os.Exit(1)
		}
		reason := ""
		if len(args) > 2 {
			reason = strings.Join(args[2:], " ")
		}
		old := t.Status
		t.Status = target
		t.Feedback = reason
		if err := store.PutTicket(*t); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating ticket: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Rejected %s: %s -> %s\n", t.ID, pipe.Title(old), pipe.Title(target))
		if reason != "" {
			fmt.Printf("Feedback: %s\n", reason)
		}

	case "status":
		if len(args) < 2 {
			fmt.Println("Usage: soak status <ticket-id>")
			os.Exit(1)
		}
		t, err := store.GetTicket(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: ticket %s not found: %v\n", args[1], err)
			os.Exit(1)
		}
		fmt.Printf("ID:       %s\nTitle:    %s\nStatus:   %s\n", t.ID, t.Title, pipe.Title(t.Status))
		if t.Worktree != "" {
			fmt.Printf("Worktree: %s\n", t.Worktree)
		}
		if t.Feedback != "" {
			fmt.Printf("Feedback: %s\n", t.Feedback)
		}

	case "create":
		if len(args) < 2 {
			fmt.Println("Usage: soak create <title>")
			os.Exit(1)
		}
		title := strings.Join(args[1:], " ")
		t := Ticket{
			ID:     generateTicketID(),
			Title:  title,
			Status: pipe.Stages[0].Name,
		}
		if err := store.PutTicket(t); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating ticket: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created %s: %s\n", t.ID, t.Title)

	case "list":
		tickets, err := store.AllTickets()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(tickets) == 0 {
			fmt.Println("No tickets")
			return
		}
		for _, t := range tickets {
			line := fmt.Sprintf("%-10s %-12s %s", t.ID, pipe.Title(t.Status), t.Title)
			if t.Feedback != "" {
				line += fmt.Sprintf("  [feedback: %s]", t.Feedback)
			}
			fmt.Println(line)
		}

	case "idle":
		// Debug: Play sound to verify hook is called
		exec.Command("afplay", "/System/Library/Sounds/Tink.aiff").Start()

		// Called when Claude goes idle - sets NeedsAttention flag.
		// Check env var first (set by tmux spawn script)
		ticketID := os.Getenv("SOAK_TICKET_ID")

		if ticketID == "" {
			// Fallback to cwd matching for manual launches
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			// Find ticket ID from worktree
			tickets, err := store.AllTickets()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching tickets: %v\n", err)
				os.Exit(1)
			}

			for _, t := range tickets {
				// Try exact path prefix match first
				if t.Worktree != "" && (strings.HasPrefix(cwd, t.Worktree) || strings.HasPrefix(t.Worktree, cwd)) {
					ticketID = t.ID
					break
				}
				// Fallback: match by ticket ID in path (handles worktrees in different repos)
				if strings.Contains(cwd, "/.worktrees/"+t.ID) || strings.Contains(cwd, "\\.worktrees\\"+t.ID) {
					ticketID = t.ID
					break
				}
			}
		}

		if ticketID == "" {
			return
		}

		// Get fresh ticket before updating
		ticket, err := store.GetTicket(ticketID)
		if err != nil {
			return
		}

		// Set NeedsAttention flag - Claude is idle and waiting
		ticket.NeedsAttention = true
		if err := store.PutTicket(*ticket); err != nil {
			return
		}

		// Publish idle event to NATS
		store.Publish("soak.idle", []byte(ticketID))

	case "subscribe":
		// Subscribe to NATS subject and print messages
		subject := "soak.>"
		if len(args) > 1 {
			subject = args[1]
		}

		fmt.Printf("Subscribing to '%s' on nats://127.0.0.1:%d\n", subject, natsPort)
		fmt.Println("Press Ctrl+C to stop...")

		sub, err := store.nc.Subscribe(subject, func(msg *nats.Msg) {
			fmt.Printf("[%s] Subject: %s\n", time.Now().Format("15:04:05"), msg.Subject)
			if len(msg.Data) > 0 {
				fmt.Printf("  Data: %s\n", string(msg.Data))
			}
			if len(msg.Header) > 0 {
				fmt.Printf("  Headers: %v\n", msg.Header)
			}
			fmt.Println()
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error subscribing: %v\n", err)
			os.Exit(1)
		}
		defer sub.Unsubscribe()

		// Ensure subscription is active
		if err := store.nc.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "Error flushing: %v\n", err)
		}
		fmt.Println("Subscription active, waiting for messages...")

		// Block forever
		select {}

	case "whoami":
		// Detect ticket ID from current working directory.
		// If cwd is inside a worktree like .worktrees/TICK-3, return TICK-3.
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		tickets, _ := store.AllTickets()
		for _, t := range tickets {
			// Try exact path prefix match first
			if t.Worktree != "" && strings.HasPrefix(cwd, t.Worktree) {
				fmt.Println(t.ID)
				return
			}
			// Fallback: match by ticket ID in path
			if strings.Contains(cwd, "/.worktrees/"+t.ID) || strings.Contains(cwd, "\\.worktrees\\"+t.ID) {
				fmt.Println(t.ID)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "Not in a ticket worktree\n")
		os.Exit(1)

	case "ping":
		// Debug: Play sound to verify hook is called
		exec.Command("afplay", "/System/Library/Sounds/Pop.aiff").Start()

		// Ping the soak server - used by Claude hooks to signal activity.
		// Detects current ticket and clears its NeedsAttention flag.
		logFile, _ := os.OpenFile("/tmp/soak-ping.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if logFile != nil {
			defer logFile.Close()
			fmt.Fprintf(logFile, "[%s] ping hook called\n", time.Now().Format(time.RFC3339))
		}

		// Check env var first (set by tmux spawn script)
		ticketID := os.Getenv("SOAK_TICKET_ID")

		if logFile != nil {
			fmt.Fprintf(logFile, "[%s] SOAK_TICKET_ID=%s\n", time.Now().Format(time.RFC3339), ticketID)
		}

		if ticketID == "" {
			// Fallback to cwd matching for manual launches
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}

			if logFile != nil {
				fmt.Fprintf(logFile, "[%s] cwd=%s\n", time.Now().Format(time.RFC3339), cwd)
			}

			// Find ticket ID from worktree
			tickets, err := store.AllTickets()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching tickets: %v\n", err)
				os.Exit(1)
			}

			if logFile != nil {
				fmt.Fprintf(logFile, "[%s] found %d tickets\n", time.Now().Format(time.RFC3339), len(tickets))
				for _, t := range tickets {
					fmt.Fprintf(logFile, "[%s]   - %s: worktree=%s\n", time.Now().Format(time.RFC3339), t.ID, t.Worktree)
				}
			}

			for _, t := range tickets {
				// Try exact path prefix match first
				if t.Worktree != "" && (strings.HasPrefix(cwd, t.Worktree) || strings.HasPrefix(t.Worktree, cwd)) {
					ticketID = t.ID
					if logFile != nil {
						fmt.Fprintf(logFile, "[%s] matched via worktree path\n", time.Now().Format(time.RFC3339))
					}
					break
				}
				// Fallback: match by ticket ID in path (handles worktrees in different repos)
				if strings.Contains(cwd, "/.worktrees/"+t.ID) || strings.Contains(cwd, "\\.worktrees\\"+t.ID) {
					ticketID = t.ID
					if logFile != nil {
						fmt.Fprintf(logFile, "[%s] matched via ticket ID in path\n", time.Now().Format(time.RFC3339))
					}
					break
				}
			}
		}

		if ticketID == "" {
			if logFile != nil {
				fmt.Fprintf(logFile, "[%s] no matching ticket found\n", time.Now().Format(time.RFC3339))
			}
			// Not in a worktree, just print pong
			fmt.Println("pong")
			return
		}

		if logFile != nil {
			fmt.Fprintf(logFile, "[%s] matched ticket %s\n", time.Now().Format(time.RFC3339), ticketID)
		}

		// Get fresh ticket before updating
		ticket, err := store.GetTicket(ticketID)
		if err != nil {
			if logFile != nil {
				fmt.Fprintf(logFile, "[%s] error getting ticket: %v\n", time.Now().Format(time.RFC3339), err)
			}
			fmt.Fprintf(os.Stderr, "Error getting ticket: %v\n", err)
			os.Exit(1)
		}

		// Clear NeedsAttention and update LastPingTime
		ticket.NeedsAttention = false
		ticket.LastPingTime = time.Now().Unix()
		if err := store.PutTicket(*ticket); err != nil {
			if logFile != nil {
				fmt.Fprintf(logFile, "[%s] error saving ticket: %v\n", time.Now().Format(time.RFC3339), err)
			}
			fmt.Fprintf(os.Stderr, "Error updating ticket: %v\n", err)
			os.Exit(1)
		}

		// Publish ping event to NATS
		if err := store.Publish("soak.ping", []byte(ticketID)); err != nil {
			if logFile != nil {
				fmt.Fprintf(logFile, "[%s] error publishing ping event: %v\n", time.Now().Format(time.RFC3339), err)
			}
		} else if logFile != nil {
			fmt.Fprintf(logFile, "[%s] published soak.ping event for %s\n", time.Now().Format(time.RFC3339), ticketID)
		}

		if logFile != nil {
			fmt.Fprintf(logFile, "[%s] cleared NeedsAttention for %s\n", time.Now().Format(time.RFC3339), ticketID)
		}
		fmt.Println("pong")

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0])
		fmt.Println("Commands: create, move, reject, status, list, whoami, ping, idle, install-hooks, subscribe")
		os.Exit(1)
	}
}

//go:embed hooks-template.json
var hooksTemplate string

// CLI subcommands for ticket operations (used by agents and humans).

func installHooks(global bool) {
	if global {
		installGlobalHooks()
		return
	}

	// Install Claude Code hooks for the current project (non-destructive)
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := installHooksInDir(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Installed soak hooks in .claude/settings.local.json")
}

// installHooksInDir installs hooks in a specific directory's .claude/settings.local.json
func installHooksInDir(dir string) error {
	// Find or create .claude directory
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("creating .claude directory: %w", err)
	}

	settingsFile := filepath.Join(claudeDir, "settings.local.json")

	// Get soak binary path and replace in template
	soakPath, err := os.Executable()
	if err != nil {
		soakPath = "~/.soak/soak"
	}
	template := strings.ReplaceAll(hooksTemplate, "~/.soak/soak", soakPath)

	// Use jq to merge template with existing settings
	cmd := exec.Command("jq", "-s", ".[0] * .[1]", settingsFile, "-")
	cmd.Stdin = strings.NewReader(template)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// If file doesn't exist, jq fails reading it, so just use template
		if _, statErr := os.Stat(settingsFile); os.IsNotExist(statErr) {
			if err := os.WriteFile(settingsFile, []byte(template), 0644); err != nil {
				return fmt.Errorf("writing settings.json: %w", err)
			}
			return nil
		}
		return fmt.Errorf("running jq: %w: %s", err, output)
	}

	if err := os.WriteFile(settingsFile, output, 0644); err != nil {
		return fmt.Errorf("writing settings.json: %w", err)
	}

	return nil
}

func installGlobalHooks() {
	// Install hooks in global Claude Code settings (non-destructive)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	settingsFile := filepath.Join(homeDir, ".claude.json")

	// Get soak binary path and replace in template
	soakPath, err := os.Executable()
	if err != nil {
		soakPath = "soak"
	}
	template := strings.ReplaceAll(hooksTemplate, "~/.soak/soak", soakPath)

	// Use jq to merge template with existing settings
	cmd := exec.Command("jq", "-s", ".[0] * .[1]", settingsFile, "-")
	cmd.Stdin = strings.NewReader(template)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// If file doesn't exist, jq fails reading it, so just use template
		if _, statErr := os.Stat(settingsFile); os.IsNotExist(statErr) {
			if err := os.WriteFile(settingsFile, []byte(template), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", settingsFile, err)
				os.Exit(1)
			}
			fmt.Println("✓ Installed soak hooks globally")
			return
		}
		fmt.Fprintf(os.Stderr, "Error running jq: %v: %s\n", err, output)
		os.Exit(1)
	}

	if err := os.WriteFile(settingsFile, output, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", settingsFile, err)
		os.Exit(1)
	}

	fmt.Printf("✓ Installed soak hooks globally in %s\n", settingsFile)
}
