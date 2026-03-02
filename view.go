package main

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

//go:embed asci.txt
var asciiArt string

// View rendering and styles for the Bubble Tea UI.

var (
	colWidth = 54

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1).
			Width(colWidth).
			Align(lipgloss.Center)

	activeHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("33")).
				Padding(0, 1).
				Width(colWidth).
				Align(lipgloss.Center)

	ticketStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1).
			Width(colWidth - 2)

	feedbackTicketStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("196")).
				Padding(0, 1).
				Width(colWidth - 2)

	columnStyle = lipgloss.NewStyle().
			Width(colWidth).
			Padding(0, 0)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("99")).
			MarginBottom(1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("33")).
			Bold(true).
			Padding(0, 1)

	attentionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("226")).
			Background(lipgloss.Color("196")).
			Bold(true).
			Padding(0, 2)

	workerInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true).
			Padding(0, 1)

	attentionTicketStyle = lipgloss.NewStyle().
				Border(lipgloss.ThickBorder()).
				BorderForeground(lipgloss.Color("196")).
				Padding(0, 1).
				Width(colWidth - 2)
)

func (m model) View() string {
	var b strings.Builder

	// Calculate dynamic column width based on terminal width
	// Account for: ASCII art (~35 chars) + padding + margins
	availableWidth := m.width - 35 - 4 // art + margins
	numCols := len(m.pipe.Stages)
	if numCols == 0 {
		numCols = 1
	}
	dynamicColWidth := availableWidth / numCols
	if dynamicColWidth < 20 {
		dynamicColWidth = 20 // minimum width
	}
	if dynamicColWidth > 54 {
		dynamicColWidth = 54 // maximum width (original)
	}

	// Check if any ticket needs attention
	hasAttention := false
	for _, t := range m.tickets {
		if t.NeedsAttention {
			hasAttention = true
			break
		}
	}

	// Set art color based on attention status
	artColor := "99"
	if hasAttention {
		artColor = "196"
	}
	artStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(artColor)).
		Padding(0, 2, 0, 2)

	// Render ASCII art on the left
	var artRendered string
	if asciiArt != "" {
		artRendered = artStyle.Render(asciiArt)
	}

	b.WriteString(titleStyle.Render("AI Orchestration Pipeline"))
	b.WriteString("\n\n")

	// Create dynamic styles based on calculated width
	dynamicHeaderStyle := headerStyle.Copy().Width(dynamicColWidth)
	dynamicActiveHeaderStyle := activeHeaderStyle.Copy().Width(dynamicColWidth)
	dynamicTicketStyle := ticketStyle.Copy().Width(dynamicColWidth - 2)
	dynamicFeedbackTicketStyle := feedbackTicketStyle.Copy().Width(dynamicColWidth - 2)
	dynamicAttentionTicketStyle := attentionTicketStyle.Copy().Width(dynamicColWidth - 2)
	dynamicColumnStyle := columnStyle.Copy().Width(dynamicColWidth)

	var cols []string
	for ci, stage := range m.pipe.Stages {
		var col strings.Builder

		if ci == m.col {
			col.WriteString(dynamicActiveHeaderStyle.Render(stage.Title))
		} else {
			col.WriteString(dynamicHeaderStyle.Render(stage.Title))
		}
		col.WriteString("\n")

		items := m.ticketsInColumn(stage.Name)
		if len(items) == 0 {
			col.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				Italic(true).
				Padding(1, 1).
				Render("(empty)"))
			col.WriteString("\n")
		}
		for ti, t := range items {
			label := t.ID
			maxTitle := dynamicColWidth - 6
			if maxTitle < 4 {
				maxTitle = 4
			}
			if len(t.Title) > maxTitle {
				label += "\n" + t.Title[:maxTitle] + ".."
			} else {
				label += "\n" + t.Title
			}

			// Choose base style based on ticket state
			var style lipgloss.Style
			if t.NeedsAttention {
				style = dynamicAttentionTicketStyle
			} else if t.Feedback != "" {
				style = dynamicFeedbackTicketStyle
			} else {
				style = dynamicTicketStyle
			}

			// If selected, add blue bottom border to whatever style it has
			if ci == m.col && ti == m.row {
				style = style.Copy().BorderBottomForeground(lipgloss.Color("33"))
			}

			col.WriteString(style.Render(label))
			col.WriteString("\n")
		}

		cols = append(cols, dynamicColumnStyle.Render(col.String()))
	}

	// Build right side content (board + status/help)
	var rightSide strings.Builder
	board := lipgloss.JoinHorizontal(lipgloss.Top, cols...)
	rightSide.WriteString(board)
	rightSide.WriteString("\n")

	if t := m.selectedTicket(); t != nil && t.Feedback != "" {
		rightSide.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true).
			Padding(0, 1).
			Render(fmt.Sprintf("  Feedback: %s", t.Feedback)))
		rightSide.WriteString("\n")
	}

	if m.err != nil {
		rightSide.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf("  Error: %v", m.err)))
		rightSide.WriteString("\n")
	}

	if m.createMode {
		ticketTypes := []string{"story", "bug", "feature", "chore", "task"}
		if m.createStep == 0 {
			rightSide.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("33")).
				Bold(true).
				Padding(0, 1).
				Render("  Create ticket — select type:"))
			rightSide.WriteString("\n")
			for i, t := range ticketTypes {
				prefix := "    "
				if i == m.createTypeSelect {
					prefix = "  > "
				}
				style := lipgloss.NewStyle().Padding(0, 1)
				if i == m.createTypeSelect {
					style = style.Foreground(lipgloss.Color("33")).Bold(true)
				}
				rightSide.WriteString(style.Render(fmt.Sprintf("%s%s", prefix, t)))
				rightSide.WriteString("\n")
			}
		} else {
			ticketType := ticketTypes[m.createTypeSelect]
			rightSide.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("33")).
				Bold(true).
				Padding(0, 1).
				Render(fmt.Sprintf("  Create %s ticket — title: ", ticketType)))
			rightSide.WriteString(m.createTitleInput.View())
			rightSide.WriteString("\n")
		}
	}

	if m.importMode && len(m.importList) > 0 {
		rightSide.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("33")).
			Bold(true).
			Padding(0, 1).
			Render("  Import ticket:"))
		rightSide.WriteString("\n")
		// Show a window of tickets around the selection
		visibleCount := 8
		start := m.importSelect - visibleCount/2
		if start < 0 {
			start = 0
		}
		end := start + visibleCount
		if end > len(m.importList) {
			end = len(m.importList)
			start = end - visibleCount
			if start < 0 {
				start = 0
			}
		}
		for i := start; i < end; i++ {
			t := m.importList[i]
			prefix := "    "
			if i == m.importSelect {
				prefix = "  > "
			}
			style := lipgloss.NewStyle().Padding(0, 1)
			if i == m.importSelect {
				style = style.Foreground(lipgloss.Color("33")).Bold(true)
			}
			label := fmt.Sprintf("%s#%d %s", prefix, t.ID, t.Title)
			if len(label) > 80 {
				label = label[:77] + "..."
			}
			rightSide.WriteString(style.Render(label))
			rightSide.WriteString("\n")
		}
		if len(m.importList) > visibleCount {
			rightSide.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				Padding(0, 3).
				Render(fmt.Sprintf("  %d/%d tickets", m.importSelect+1, len(m.importList))))
			rightSide.WriteString("\n")
		}
	}

	if m.rejectMode {
		rightSide.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true).
			Padding(0, 1).
			Render(fmt.Sprintf("  Reject %s — reason: ", m.rejectID)))
		rightSide.WriteString(m.rejectInput.View())
		rightSide.WriteString("\n")
	}

	rightSide.WriteString(statusBarStyle.Render(fmt.Sprintf("  %s", m.status)))
	rightSide.WriteString("\n")

	if m.sessionCount > 0 {
		rightSide.WriteString(workerInfoStyle.Render(fmt.Sprintf("  Claude sessions: %d  (tmux prefix+n/p to switch tabs)", m.sessionCount)))
		rightSide.WriteString("\n")
	}

	rightSide.WriteString("\n")

	// Show approve/reject keys only in human stages (stages without agents)
	currentStage := m.pipe.StageByName(m.currentStageName())
	if currentStage != nil && currentStage.Agent == nil {
		rightSide.WriteString(lipgloss.NewStyle().Padding(0, 1).Render(m.help.ShortHelpView(m.keys.ShortHelpWithApproval())))
	} else {
		rightSide.WriteString(lipgloss.NewStyle().Padding(0, 1).Render(m.help.View(m.keys)))
	}

	// Join ASCII art on left with board on right
	if artRendered != "" {
		// Calculate height to make art fill the same height as content
		rightHeight := strings.Count(rightSide.String(), "\n") + 1
		artWithHeight := artStyle.Height(rightHeight).Render(asciiArt)
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, artWithHeight, rightSide.String()))
	} else {
		b.WriteString(rightSide.String())
	}
	b.WriteString("\n")

	return b.String()
}
