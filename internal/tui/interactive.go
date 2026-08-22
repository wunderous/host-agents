package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type bubbleEventKind uint8

const (
	bubbleOutputEvent bubbleEventKind = iota
	bubblePromptEvent
)

type bubbleEvent struct {
	kind bubbleEventKind
	text string
}

// interactiveIO adapts the synchronous App command engine to Bubble Tea. A
// capability can ask for approval or entity selection while running in a
// command goroutine; the prompt is delivered to the model and the answer is
// sent back through this reader.
type interactiveIO struct {
	events  chan bubbleEvent
	answers chan string
	done    chan struct{}

	mu     sync.RWMutex
	closed bool
	ctx    context.Context
}

func newInteractiveIO(ctx context.Context) *interactiveIO {
	return &interactiveIO{
		events:  make(chan bubbleEvent, 256),
		answers: make(chan string, 1),
		done:    make(chan struct{}),
		ctx:     ctx,
	}
}

func (i *interactiveIO) setContext(ctx context.Context) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.ctx = ctx
}

func (i *interactiveIO) context() context.Context {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.ctx == nil {
		return context.Background()
	}
	return i.ctx
}

func (i *interactiveIO) ReadLine(prompt string, _ func(string) []string) (string, error) {
	if err := i.send(bubbleEvent{kind: bubblePromptEvent, text: prompt}); err != nil {
		return "", err
	}
	select {
	case answer := <-i.answers:
		return answer, nil
	case <-i.done:
		return "", io.EOF
	case <-i.context().Done():
		return "", i.context().Err()
	}
}

func (i *interactiveIO) Write(value []byte) (int, error) {
	text := strings.TrimSuffix(strings.ReplaceAll(string(value), "\r\n", "\n"), "\n")
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		if err := i.send(bubbleEvent{kind: bubbleOutputEvent, text: line}); err != nil {
			return 0, err
		}
	}
	return len(value), nil
}

func (i *interactiveIO) answer(value string) {
	select {
	case i.answers <- value:
	case <-i.done:
	default:
	}
}

func (i *interactiveIO) send(event bubbleEvent) error {
	i.mu.RLock()
	closed := i.closed
	i.mu.RUnlock()
	if closed {
		return io.EOF
	}
	select {
	case i.events <- event:
		return nil
	case <-i.done:
		return io.EOF
	}
}

func (i *interactiveIO) Close() error {
	i.mu.Lock()
	if !i.closed {
		i.closed = true
		close(i.done)
	}
	i.mu.Unlock()
	return nil
}

func useBubbleTea(config Config) bool {
	if config.NoPrompt {
		return false
	}
	in := config.In
	if in == nil {
		in = os.Stdin
	}
	out := config.Out
	if out == nil {
		out = os.Stdout
	}
	inFile, inOK := in.(*os.File)
	outFile, outOK := out.(*os.File)
	return inOK && outOK && term.IsTerminal(int(inFile.Fd())) && term.IsTerminal(int(outFile.Fd()))
}

func runBubbleTea(ctx context.Context, config Config) error {
	app, input, err := NewInteractive(ctx, config)
	if err != nil {
		return err
	}
	defer app.Close()

	model := newBubbleModel(ctx, app, input)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = program.Run()
	return err
}

type bubbleCommandDoneMsg struct {
	err error
}

type bubbleEventClosedMsg struct{}

type bubbleModel struct {
	ctx   context.Context
	app   *App
	input *interactiveIO

	commandInput textinput.Model
	viewport     viewport.Model
	output       []string
	prompt       string
	status       string
	executing    bool
	cancel       context.CancelFunc
	history      []string
	historyIndex int
	width        int
	height       int
}

var (
	bubbleTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7D7AFF"})
	bubbleMutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#777777", Dark: "#777777"})
	bubblePromptStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#04B575", Dark: "#04B575"})
	bubbleErrorStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#FF4672", Dark: "#ED567A"})
)

func newBubbleModel(ctx context.Context, app *App, input *interactiveIO) bubbleModel {
	commandInput := textinput.New()
	commandInput.Prompt = "› "
	commandInput.Placeholder = "type /help, a capability, or setup apply <file>"
	commandInput.CharLimit = 4096
	commandInput.ShowSuggestions = true
	commandInput.PromptStyle = bubblePromptStyle
	commandInput.Focus()

	model := bubbleModel{
		ctx:          ctx,
		app:          app,
		input:        input,
		commandInput: commandInput,
		viewport:     viewport.New(80, 16),
		status:       "ready · deterministic mode",
		width:        80,
		height:       24,
	}
	model.refreshSuggestions()
	return model
}

func (m bubbleModel) Init() tea.Cmd {
	return tea.Batch(m.commandInput.Focus(), waitBubbleEvent(m.input.events))
}

func (m bubbleModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil
	case bubbleEvent:
		switch msg.kind {
		case bubbleOutputEvent:
			m.appendOutput(msg.text)
		case bubblePromptEvent:
			m.prompt = msg.text
			m.commandInput.Reset()
			m.commandInput.Focus()
			m.status = "input required"
		}
		return m, waitBubbleEvent(m.input.events)
	case bubbleCommandDoneMsg:
		m.executing = false
		m.prompt = ""
		m.input.setContext(m.ctx)
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		if msg.err != nil {
			m.status = "command failed"
			m.appendOutput(bubbleErrorStyle.Render("error: " + msg.err.Error()))
		} else {
			m.status = "ready · deterministic mode"
		}
		m.commandInput.Reset()
		m.commandInput.Focus()
		return m, nil
	case bubbleEventClosedMsg:
		return m, tea.Quit
	case tea.MouseMsg:
		var command tea.Cmd
		m.viewport, command = m.viewport.Update(msg)
		return m, command
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m bubbleModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.executing && m.cancel != nil {
			m.cancel()
			m.status = "canceling command…"
			return m, nil
		}
		return m, tea.Quit
	case "ctrl+l":
		m.output = nil
		m.syncViewport()
		return m, nil
	case "ctrl+u":
		m.viewport.HalfPageUp()
		return m, nil
	case "ctrl+d":
		m.viewport.HalfPageDown()
		return m, nil
	case "pgup":
		m.viewport.PageUp()
		return m, nil
	case "pgdown":
		m.viewport.PageDown()
		return m, nil
	case "up":
		if !m.executing && m.prompt == "" && len(m.history) > 0 {
			if m.historyIndex > 0 {
				m.historyIndex--
			}
			m.commandInput.SetValue(m.history[m.historyIndex])
			m.refreshSuggestions()
			return m, nil
		}
	case "down":
		if !m.executing && m.prompt == "" && len(m.history) > 0 {
			if m.historyIndex < len(m.history)-1 {
				m.historyIndex++
				m.commandInput.SetValue(m.history[m.historyIndex])
			} else {
				m.historyIndex = len(m.history)
				m.commandInput.Reset()
			}
			m.refreshSuggestions()
			return m, nil
		}
	case "enter":
		if m.prompt != "" {
			m.input.answer(m.commandInput.Value())
			m.commandInput.Reset()
			m.prompt = ""
			m.status = "working…"
			return m, nil
		}
		if m.executing {
			return m, nil
		}
		line := strings.TrimSpace(m.commandInput.Value())
		if line == "" {
			return m, nil
		}
		if line == "/exit" || line == "/quit" || line == "exit" || line == "quit" {
			return m, tea.Quit
		}
		if len(m.history) == 0 || m.history[len(m.history)-1] != line {
			m.history = append(m.history, line)
			if len(m.history) > 100 {
				m.history = m.history[len(m.history)-100:]
			}
		}
		m.historyIndex = len(m.history)
		m.commandInput.Reset()
		m.executing = true
		m.status = "working…"
		commandContext, cancel := context.WithCancel(m.ctx)
		m.cancel = cancel
		m.input.setContext(commandContext)
		return m, func() tea.Msg {
			err := m.app.handle(commandContext, line)
			return bubbleCommandDoneMsg{err: err}
		}
	}

	if m.executing && m.prompt == "" {
		return m, nil
	}
	var command tea.Cmd
	m.commandInput, command = m.commandInput.Update(msg)
	m.refreshSuggestions()
	return m, command
}

func (m *bubbleModel) refreshSuggestions() {
	if m.app == nil {
		return
	}
	m.commandInput.SetSuggestions(m.app.completions(m.commandInput.Value()))
}

func (m *bubbleModel) appendOutput(value string) {
	if value == "" {
		return
	}
	m.output = append(m.output, strings.Split(value, "\n")...)
	if len(m.output) > 2000 {
		m.output = m.output[len(m.output)-2000:]
	}
	m.syncViewport()
}

func (m *bubbleModel) syncViewport() {
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(strings.Join(m.output, "\n"))
	if wasAtBottom || len(m.output) == 0 {
		m.viewport.GotoBottom()
	}
}

func (m *bubbleModel) resize() {
	if m.width < 20 {
		m.width = 20
	}
	if m.height < 8 {
		m.height = 8
	}
	m.commandInput.Width = m.width - 4
	m.viewport.Width = m.width
	m.viewport.Height = m.height - 6
	if m.viewport.Height < 1 {
		m.viewport.Height = 1
	}
	m.syncViewport()
}

func (m bubbleModel) View() string {
	if m.width == 0 {
		return ""
	}
	header := bubbleTitleStyle.Render("OPUTE HOST AGENT") + "  " + bubbleMutedStyle.Render("typed capabilities · catalog "+m.app.catalog.Snapshot.Revision)
	footer := bubbleMutedStyle.Render("Ctrl+C quit · Ctrl+L clear · PgUp/PgDn scroll")
	status := m.status
	if m.prompt != "" {
		status = bubblePromptStyle.Render(m.prompt)
	} else if strings.HasPrefix(status, "command failed") {
		status = bubbleErrorStyle.Render(status)
	}
	return fmt.Sprintf("%s\n%s\n\n%s\n%s\n%s", header, m.viewport.View(), status, m.commandInput.View(), footer)
}

func waitBubbleEvent(events <-chan bubbleEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return bubbleEventClosedMsg{}
		}
		return event
	}
}
