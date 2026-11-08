package tui

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// terminalModel is an editor with a saved flow, whose Terminal runs a stand-in for
// Claude Code.
func terminalModel(t *testing.T, program string, args ...string) model {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-ins are Unix programs")
	}
	path, err := exec.LookPath(program)
	if err != nil {
		t.Skipf("%s is not here", program)
	}

	original := lookTerminalProgram
	lookTerminalProgram = func() (string, error) { return path, nil }
	t.Cleanup(func() { lookTerminalProgram = original })

	m := editorModel(160, 40)
	m.activeHost = "localhost:4000"
	m.activeAPIKey = "hd_live_secret"
	m.editorWorkflowID = 7
	m.editorWorkflowName = "Daily report"
	return m
}

// waitForScreen feeds the model what the program draws until the Terminal shows want.
func waitForScreen(t *testing.T, m model, cmd tea.Cmd, want string) model {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(terminalText(m), want) {
			return m
		}
		if cmd == nil {
			break
		}
		next, nextCmd := m.Update(cmd())
		m, cmd = next.(model), nextCmd
	}
	t.Fatalf("the Terminal never showed %q:\n%s", want, terminalText(m))
	return m
}

func terminalText(m model) string {
	if m.terminal == nil {
		return ""
	}
	return ansi.Strip(strings.Join(m.terminal.lines(0), "\n"))
}

func TestTheTerminalCarriesKeysAndTheScreen(t *testing.T) {
	m := terminalModel(t, "cat")

	// cat takes no flags of Claude Code, so it runs alone here.
	session, err := startTerminal([]string{"cat"}, nil, 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	m.terminal, m.terminalOpen, m.terminalFocus = session, true, true
	m.reflow()
	t.Cleanup(session.close)

	var cmd tea.Cmd
	for _, key := range "hello" {
		var next tea.Model
		next, _ = m.Update(keyPress(string(key)))
		m = next.(model)
	}
	cmd = m.terminal.listen()
	m = waitForScreen(t, m, cmd, "hello")

	if !strings.Contains(strings.Join(screenLines(m), "\n"), "hello") {
		t.Fatal("the screen of the editor doesn't show the Terminal")
	}
}

func TestEveryKeyGoesToTheTerminalButTheOneThatLeaves(t *testing.T) {
	m := terminalModel(t, "cat")
	session, err := startTerminal([]string{"cat"}, nil, 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	m.terminal, m.terminalOpen, m.terminalFocus = session, true, true
	m.reflow()
	t.Cleanup(session.close)
	nodes := len(m.editorNodes)

	// q quits Treadle and a adds a node, anywhere else.
	for _, key := range []string{"q", "a", "/"} {
		next, cmd := m.Update(keyPress(key))
		m = next.(model)
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Fatalf("%s quit Treadle", key)
			}
		}
	}
	if len(m.editorNodes) != nodes || m.aiChatOpen || m.editorMode != editorModeNormal {
		t.Fatal("a key meant for the Terminal reached the editor")
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	m = next.(model)
	if m.terminalFocus || m.terminal == nil {
		t.Fatalf("focus = %v, open = %v", m.terminalFocus, m.terminal != nil)
	}

	m, _ = m.updateWorkflowEditorKey(keyPress("t"))
	if !m.terminalFocus {
		t.Fatal("t didn't go back to the Terminal")
	}
}

func TestTheTerminalClosesWhenItsProgramEnds(t *testing.T) {
	m := terminalModel(t, "true")

	m, cmd := m.openTerminal()
	if m.terminal == nil {
		t.Fatalf("not open: %s", m.editorStatusMessage)
	}
	for m.terminal != nil && cmd != nil {
		next, nextCmd := m.Update(cmd())
		m, cmd = next.(model), nextCmd
	}

	if m.terminalOpen || m.terminalFocus || m.terminalRect.w != 0 {
		t.Fatalf("open = %v, focus = %v, rect = %+v", m.terminalOpen, m.terminalFocus, m.terminalRect)
	}
	if !strings.Contains(m.editorStatusMessage, "Terminal closed") {
		t.Fatalf("status = %q", m.editorStatusMessage)
	}
}

func TestTheTerminalWorksOnTheOpenFlowThroughHeddle(t *testing.T) {
	m := terminalModel(t, "cat")

	command, env, err := m.terminalCommand("/usr/bin/claude")
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(command, " ")
	if strings.Contains(joined, "hd_live_secret") {
		t.Fatal("the key is in the arguments, where any process can read it")
	}
	if indexOfString(env, "HEDDLE_MCP_TOKEN=hd_live_secret") < 0 {
		t.Fatal("the key is not in the environment")
	}
	if !strings.Contains(joined, `"Daily report" (id 7)`) {
		t.Fatalf("the program isn't told which flow is open: %s", joined)
	}

	var config struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(command[indexOfString(command, "--mcp-config")+1]), &config); err != nil {
		t.Fatal(err)
	}
	server := config.MCPServers["heddle"]
	if server.URL != "http://localhost:4000/mcp" || server.Headers["Authorization"] != "Bearer ${HEDDLE_MCP_TOKEN}" {
		t.Fatalf("server = %+v", server)
	}
}

func TestTheTerminalSaysWhenClaudeCodeIsMissing(t *testing.T) {
	m := editorModel(160, 40)
	m.editorWorkflowID = 7
	original := lookTerminalProgram
	lookTerminalProgram = func() (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { lookTerminalProgram = original })

	m, cmd := m.openTerminal()

	if cmd != nil || m.terminalOpen || !strings.Contains(m.editorStatusMessage, terminalInstall) {
		t.Fatalf("open = %v, status = %q", m.terminalOpen, m.editorStatusMessage)
	}
}

func TestTheTerminalSharesTheScreenOrTakesItAll(t *testing.T) {
	m := editorModel(160, 40)
	m.terminalOpen = true
	m.reflow()

	if m.terminalRect.x == 0 || m.canvasRect.x+m.canvasRect.w >= m.terminalRect.x+1 {
		t.Fatalf("beside: canvas = %+v, terminal = %+v", m.canvasRect, m.terminalRect)
	}

	m.terminalWide = true
	m.reflow()
	if !m.terminalCoversEditor() || m.terminalRect.w != 160 {
		t.Fatalf("wide: terminal = %+v", m.terminalRect)
	}

	narrow := editorModel(100, 30)
	narrow.terminalOpen = true
	narrow.reflow()
	if !narrow.terminalCoversEditor() {
		t.Fatalf("narrow: terminal = %+v", narrow.terminalRect)
	}

	for _, line := range screenLines(m) {
		if width := len([]rune(line)); width > 160 {
			t.Fatalf("a line is %d wide", width)
		}
	}
}

func indexOfString(values []string, value string) int {
	for i, candidate := range values {
		if candidate == value {
			return i
		}
	}
	return -1
}
