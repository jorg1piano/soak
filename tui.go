package main

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Bubble Tea model, key bindings, and update loop.

type keyMap struct {
	Left       key.Binding
	Right      key.Binding
	Up         key.Binding
	Down       key.Binding
	Open       key.Binding
	New        key.Binding
	Claude     key.Binding
	Approve    key.Binding
	Reject     key.Binding
	Delete     key.Binding
	Editor     key.Binding
	Shell      key.Binding
	FreeClaude key.Binding
	OpenTicket key.Binding
	Quit       key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Left:       key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("<-/h", "col")),
		Right:      key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("->/l", "col")),
		Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("k", "up")),
		Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("j", "down")),
		Open:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("ret", "open")),
		New:        key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Claude:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "claude")),
		Approve:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "approve")),
		Reject:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "reject")),
		Delete:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Editor:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "vscode")),
		Shell:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "shell")),
		FreeClaude: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "free claude")),
		OpenTicket: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open ticket")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Open, k.New, k.Claude, k.Editor, k.Shell, k.FreeClaude, k.OpenTicket, k.Quit}
}

func (k keyMap) ShortHelpWithApproval() []key.Binding {
	return []key.Binding{k.Open, k.New, k.Claude, k.Approve, k.Reject, k.Editor, k.Shell, k.FreeClaude, k.OpenTicket, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding { return nil }

type model struct {
	store     *Store
	tmux      *TmuxManager
	pipe      *Pipeline
	tickets   []Ticket
	col       int
	row       int
	keys      keyMap
	help      help.Model
	err       error
	status    string
	// Reject input mode
	rejectMode  bool
	rejectInput textinput.Model
	rejectID    string // ticket being rejected
	// Create ticket mode
	createMode       bool
	createTypeSelect int // 0=bug, 1=feature, 2=chore, 3=task
	createTitleInput textinput.Model
	createStep       int // 0=select type, 1=enter title
	// Cached values to avoid blocking during render
	sessionCount int
	width        int
	height       int
	animFrame    int
}

type tickMsg struct{}
type animMsg struct{}

// Async operation result messages
type claudeOpenedMsg struct {
	ticket  Ticket
	err     error
	updated Ticket
}

type shellOpenedMsg struct {
	err error
}

type freeClaudeOpenedMsg struct {
	err error
}

type tickCompleteMsg struct {
	sessionCount int
	spawned      []spawnResult
}

type spawnResult struct {
	ticketID   string
	stageName  string
	stageTitle string
	err        error
}

func tickCmd() tea.Cmd {
	return tea.Tick(1*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func animCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return animMsg{}
	})
}

func initialModel(store *Store, tmux *TmuxManager, pipe *Pipeline) model {
	rejectInput := textinput.New()
	rejectInput.Placeholder = "Rejection reason..."
	rejectInput.CharLimit = 200
	rejectInput.Width = 60

	createInput := textinput.New()
	createInput.Placeholder = "Ticket title..."
	createInput.CharLimit = 100
	createInput.Width = 60
	createInput.Blur()

	m := model{
		store:            store,
		tmux:             tmux,
		pipe:             pipe,
		rejectInput:      rejectInput,
		createTitleInput: createInput,
		keys:             newKeyMap(),
		help:             help.New(),
		status:           "Ready",
		sessionCount:     0, // Will be updated on first tick
	}
	m.reload()
	return m
}

func (m *model) reload() {
	tickets, err := m.store.AllTickets()
	if err != nil {
		m.err = err
		return
	}
	m.tickets = tickets
	m.clampRow()
}

func (m *model) ticketsInColumn(stage string) []Ticket {
	var out []Ticket
	for _, t := range m.tickets {
		if t.Status == stage {
			out = append(out, t)
		}
	}
	return out
}

func (m *model) currentStageName() string {
	return m.pipe.Stages[m.col].Name
}

func (m *model) clampRow() {
	items := m.ticketsInColumn(m.currentStageName())
	if m.row >= len(items) {
		m.row = max(0, len(items)-1)
	}
}

func (m *model) selectedTicket() *Ticket {
	items := m.ticketsInColumn(m.currentStageName())
	if m.row < len(items) {
		return &items[m.row]
	}
	return nil
}

func (m model) Init() tea.Cmd {
	return tickCmd()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case claudeOpenedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = fmt.Sprintf("Failed to open agent: %v", msg.err)
		} else {
			if msg.updated.Worktree != msg.ticket.Worktree {
				m.store.PutTicket(msg.updated)
			}
			m.status = fmt.Sprintf("Opened %s agent for %s", m.pipe.Title(msg.ticket.Status), msg.ticket.ID)
		}
		return m, nil

	case shellOpenedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Failed to open shell: %v", msg.err)
		} else {
			m.status = "Opened shell tab"
		}
		return m, nil

	case freeClaudeOpenedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Failed to open free Claude: %v", msg.err)
		} else {
			m.status = "Opened free Claude session"
		}
		return m, nil

	case tickCompleteMsg:
		// Update state from async tick operation
		m.sessionCount = msg.sessionCount
		m.reload()

		// Show status for auto-spawned sessions
		if len(msg.spawned) > 0 {
			for _, s := range msg.spawned {
				if s.err != nil {
					m.status = fmt.Sprintf("Spawn error for %s: %v", s.ticketID, s.err)
				} else {
					m.status = fmt.Sprintf("Auto-spawned %s agent for %s", s.stageTitle, s.ticketID)
				}
			}
		}
		return m, nil

	case tickMsg:
		// Don't update tick when in input mode to avoid blocking
		if m.createMode || m.rejectMode {
			return m, tickCmd()
		}
		return m.handleTick()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.rejectMode {
			return m.handleRejectInput(msg)
		}

		if m.createMode {
			return m.handleCreateInput(msg)
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			return m.handleQuit()
		case key.Matches(msg, m.keys.Open):
			return m.handleOpen()
		case key.Matches(msg, m.keys.New):
			return m.handleNew()
		case key.Matches(msg, m.keys.Claude):
			return m.handleClaude()
		case key.Matches(msg, m.keys.Approve):
			return m.handleApprove()
		case key.Matches(msg, m.keys.Reject):
			return m.handleReject()
		case key.Matches(msg, m.keys.Delete):
			return m.handleDelete()
		case key.Matches(msg, m.keys.Editor):
			return m.handleEditor()
		case key.Matches(msg, m.keys.Shell):
			return m.handleShell()
		case key.Matches(msg, m.keys.FreeClaude):
			return m.handleFreeClaude()
		case key.Matches(msg, m.keys.OpenTicket):
			return m.handleOpenTicket()
		default:
			return m.handleNavigation(msg)
		}
	}

	// Update text inputs for cursor blink
	if m.createMode && m.createStep == 1 {
		var cmd tea.Cmd
		m.createTitleInput, cmd = m.createTitleInput.Update(msg)
		return m, cmd
	}
	if m.rejectMode {
		var cmd tea.Cmd
		m.rejectInput, cmd = m.rejectInput.Update(msg)
		return m, cmd
	}

	return m, nil
}
