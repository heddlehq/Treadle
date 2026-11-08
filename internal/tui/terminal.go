package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

// The Terminal runs Claude Code next to the canvas, as Anthropic ships it, on this
// machine. Whoever uses it signs in through its own flow, with their own account, so
// Treadle never offers or touches that login, never sends anything on its own and
// never reads what the program shows: it carries the keys in and draws the screen.
// The name stays out of ours too: this is the Terminal, which runs Claude Code.
//
// It works on the open flow through the MCP server of the Heddle Treadle is
// connected to, with the same key, the way the Terminal of the web editor does.

const (
	terminalProgram = "claude"
	// terminalLeave is the one key the Terminal doesn't pass on: it goes back to the
	// canvas. Claude Code uses esc, tab and most ctrl keys itself.
	terminalLeave     = "ctrl+]"
	terminalMinWidth  = 60
	terminalWideBelow = 120
	terminalRepaint   = 16 * time.Millisecond
	terminalWheelRows = 3
	terminalHistory   = 5000
	terminalInstall   = "https://code.claude.com/docs/en/setup"
)

// lookTerminalProgram finds Claude Code. Tests replace it.
var lookTerminalProgram = func() (string, error) { return exec.LookPath(terminalProgram) }

type terminalSession struct {
	emu     *vt.SafeEmulator
	pty     xpty.Pty
	cmd     *exec.Cmd
	updates chan struct{}
	exited  chan error
	cols    int
	rows    int
	once    sync.Once
}

type terminalOutputMsg struct{ session *terminalSession }

type terminalExitedMsg struct {
	session *terminalSession
	err     error
}

// startTerminal runs a program on a new pty, cols wide and rows tall.
func startTerminal(command []string, env []string, cols, rows int) (*terminalSession, error) {
	if len(command) == 0 {
		return nil, errors.New("nothing to run")
	}
	cols, rows = max(cols, 2), max(rows, 2)

	pty, err := xpty.NewPty(cols, rows)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = env
	if dir, err := os.Getwd(); err == nil {
		cmd.Dir = dir
	}
	if err := pty.Start(cmd); err != nil {
		_ = pty.Close()
		return nil, err
	}

	s := &terminalSession{
		emu:     vt.NewSafeEmulator(cols, rows),
		pty:     pty,
		cmd:     cmd,
		updates: make(chan struct{}, 1),
		exited:  make(chan error, 1),
		cols:    cols,
		rows:    rows,
	}
	s.emu.SetScrollbackSize(terminalHistory)

	go s.readScreen()
	// What the emulator answers, keys included, goes to the program.
	go func() { _, _ = io.Copy(pty, s.emu) }()
	go func() { s.exited <- xpty.WaitProcess(context.Background(), cmd) }()

	return s, nil
}

func (s *terminalSession) readScreen() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			_, _ = s.emu.Write(buf[:n])
			select {
			case s.updates <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// listen waits for the screen to change or the program to end. Changes that come
// close together are drawn once.
func (s *terminalSession) listen() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-s.updates:
			time.Sleep(terminalRepaint)
			select {
			case <-s.updates:
			default:
			}
			return terminalOutputMsg{session: s}
		case err := <-s.exited:
			return terminalExitedMsg{session: s, err: err}
		}
	}
}

func (s *terminalSession) resize(cols, rows int) {
	cols, rows = max(cols, 2), max(rows, 2)
	if cols == s.cols && rows == s.rows {
		return
	}
	s.cols, s.rows = cols, rows
	s.emu.Resize(cols, rows)
	_ = s.pty.Resize(cols, rows)
}

func (s *terminalSession) close() {
	s.once.Do(func() {
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.pty.Close()
		_ = s.emu.Close()
	})
}

// lines draws the screen, or what scrolled off it when offset is above zero, as
// cols wide styled rows.
func (s *terminalSession) lines(offset int) []string {
	history := s.emu.ScrollbackLen()
	offset = clamp(offset, 0, history)

	rows := make([]string, 0, s.rows)
	for y := -offset; y < s.rows-offset; y++ {
		line := make(uv.Line, s.cols)
		for x := range line {
			var cell *uv.Cell
			if y < 0 {
				cell = s.emu.ScrollbackCellAt(x, history+y)
			} else {
				cell = s.emu.CellAt(x, y)
			}
			if cell != nil {
				line[x] = *cell
			} else {
				line[x] = uv.EmptyCell
			}
		}
		rendered := line.Render() + ansi.ResetStyle
		rows = append(rows, rendered+blank(s.cols-lipgloss.Width(rendered)))
	}
	return rows
}

// terminalCommand is Claude Code with the Heddle this is connected to as its only MCP
// server, and a word on which flow is open. The key goes in the environment, where
// other processes can't read it, not in the arguments.
func (m model) terminalCommand(program string) ([]string, []string, error) {
	base, err := normalizeBaseURL(m.activeHost)
	if err != nil {
		return nil, nil, err
	}

	config, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"heddle": map[string]any{
				"type":    "http",
				"url":     base + "/mcp",
				"headers": map[string]string{"Authorization": "Bearer ${HEDDLE_MCP_TOKEN}"},
			},
		},
	})
	if err != nil {
		return nil, nil, err
	}

	instructions := fmt.Sprintf(`You are running inside Treadle, a terminal editor of Heddle flows, and the person has the flow %q (id %d) open next to this terminal. "The flow" means that one. Work on it through the tools of the heddle MCP server: read it with get_flow, change it with update_flow, and prove a change with test_flow. The canvas refreshes by itself. When something is wrong, start from its latest run, with list_executions and get_execution.`,
		m.editorWorkflowName, m.editorWorkflowID)

	command := []string{program, "--strict-mcp-config", "--mcp-config", string(config), "--append-system-prompt", instructions}
	env := append(os.Environ(), "HEDDLE_MCP_TOKEN="+m.activeAPIKey, "TERM=xterm-256color", "COLORTERM=truecolor")
	return command, env, nil
}

// openTerminal starts the Terminal, or goes back to it.
func (m model) openTerminal() (model, tea.Cmd) {
	m.closeEditorOverlay()
	if m.terminal != nil {
		m.terminalFocus = true
		return m, nil
	}
	if m.editorWorkflowID == 0 {
		m.editorStatusMessage = "Can't open the Terminal before the flow is saved: ctrl+s saves it"
		return m, nil
	}

	program, err := lookTerminalProgram()
	if err != nil {
		m.editorStatusMessage = "Can't open the Terminal: it runs Claude Code, which isn't installed. See " + terminalInstall
		return m, nil
	}
	command, env, err := m.terminalCommand(program)
	if err != nil {
		m.editorStatusMessage = "Can't open the Terminal: " + err.Error()
		return m, nil
	}

	m.terminalOpen = true
	m.reflow()
	cols, rows := m.terminalSize()
	session, err := startTerminal(command, env, cols, rows)
	if err != nil {
		m.terminalOpen = false
		m.reflow()
		m.editorStatusMessage = "Can't open the Terminal: " + err.Error()
		return m, nil
	}

	m.terminal = session
	m.terminalFocus = true
	m.terminalScroll = 0
	return m, session.listen()
}

func (m *model) closeTerminal() {
	if m.terminal != nil {
		m.terminal.close()
	}
	m.terminal = nil
	m.terminalOpen = false
	m.terminalFocus = false
	m.terminalWide = false
	m.terminalScroll = 0
	m.reflow()
}

func (m model) handleTerminalOutput(msg terminalOutputMsg) (tea.Model, tea.Cmd) {
	if msg.session != m.terminal || m.terminal == nil {
		return m, nil
	}
	return m, m.terminal.listen()
}

func (m model) handleTerminalExited(msg terminalExitedMsg) (tea.Model, tea.Cmd) {
	if msg.session != m.terminal {
		return m, nil
	}
	m.closeTerminal()
	m.editorStatusMessage = "The Terminal closed"
	return m, nil
}

// updateTerminalKey gives the program every key but the one that leaves it, and the
// two that scroll back through what went off its screen.
func (m model) updateTerminalKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case terminalLeave:
		m.terminalFocus = false
		m.terminalScroll = 0
		return m, nil
	case "shift+pgup":
		m.scrollTerminal(m.terminal.rows - 1)
		return m, nil
	case "shift+pgdown":
		m.scrollTerminal(-(m.terminal.rows - 1))
		return m, nil
	}
	m.terminalScroll = 0
	m.terminal.emu.SendKey(uv.KeyPressEvent(uv.Key(msg)))
	return m, nil
}

func (m *model) scrollTerminal(delta int) {
	if m.terminal == nil {
		return
	}
	m.terminalScroll = clamp(m.terminalScroll+delta, 0, m.terminal.emu.ScrollbackLen())
}

// terminalSize is what the program gets: the pane but its title row.
func (m model) terminalSize() (int, int) {
	return max(m.terminalRect.w, 2), max(m.terminalRect.h-1, 2)
}

// layoutTerminal takes the right of the editor for the Terminal, or all of it when
// asked or when the screen is too narrow to share. It returns the width left for the
// canvas.
func (m *model) layoutTerminal(canvasX, canvasWidth, bodyY, bodyHeight int) int {
	if !m.terminalOpen {
		m.terminalRect = rect{}
		return canvasWidth
	}

	if m.terminalWide || m.width < terminalWideBelow {
		m.terminalRect = rect{x: 0, y: bodyY, w: m.width, h: bodyHeight}
	} else {
		width := clamp(m.width*45/100, terminalMinWidth, m.width-terminalMinWidth)
		canvasWidth = max(canvasWidth-width-sectionGapSize, 1)
		m.terminalRect = rect{x: canvasX + canvasWidth + sectionGapSize, y: bodyY, w: width, h: bodyHeight}
	}

	if m.terminal != nil {
		m.terminal.resize(m.terminalSize())
	}
	return canvasWidth
}

// terminalCoversEditor is the Terminal drawn over the whole editor.
func (m model) terminalCoversEditor() bool {
	return m.terminalOpen && m.terminalRect.x == 0
}

func (m model) renderTerminalPane() []string {
	width := m.terminalRect.w
	ruleColor, ruleChar := panelBorder, "─"
	if m.terminalFocus {
		ruleColor, ruleChar = accentColor, "━"
	}
	rule := lipgloss.NewStyle().Foreground(ruleColor).Render
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)

	title := "Terminal · Claude Code"
	hint := "t focus"
	switch {
	case m.terminalScroll > 0:
		hint = fmt.Sprintf("%d lines up · shift+pgdn down", m.terminalScroll)
	case m.terminalFocus:
		hint = terminalLeave + " back to the canvas"
	}
	lead := strings.Repeat(ruleChar, 2) + " "
	fill := max(width-lipgloss.Width(lead+title+hint)-3, 1)
	head := rule(lead) + muted.Render(title) + " " + rule(strings.Repeat(ruleChar, fill)) + " " + muted.Render(hint)

	rows := []string{ansi.Truncate(head, width, "")}
	if m.terminal == nil {
		for len(rows) < m.terminalRect.h {
			rows = append(rows, blank(width))
		}
		return rows
	}
	for _, line := range m.terminal.lines(m.terminalScroll) {
		rows = append(rows, line)
	}
	for len(rows) < m.terminalRect.h {
		rows = append(rows, blank(width))
	}
	return rows[:m.terminalRect.h]
}

// terminalCursor is where the cursor of the program goes on the screen, when it shows.
func (m model) terminalCursor() *tea.Cursor {
	if m.terminal == nil || !m.terminalFocus || m.terminalScroll > 0 || m.terminal.emu.CursorHidden() {
		return nil
	}
	if m.screen != screenWorkflowEditor || m.editorOverlay != editorOverlayNone || m.editorMode == editorModeModal {
		return nil
	}
	position := m.terminal.emu.CursorPosition()
	return tea.NewCursor(m.terminalRect.x+position.X, m.terminalRect.y+1+position.Y)
}

// Shutdown stops what the editor started, when Treadle quits.
func Shutdown(final tea.Model) {
	if m, ok := final.(model); ok && m.terminal != nil {
		m.terminal.close()
	}
}
