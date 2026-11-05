package tui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zalando/go-keyring"
)

func TestMain(m *testing.M) {
	keyring.MockInit()

	// os.UserConfigDir reads this, so no test can touch real credentials.
	dir, err := os.MkdirTemp("", "treadle-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func sizedModel(width, height int) model {
	next, _ := initialModel().Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(model)
}

func screenLines(m model) []string {
	return strings.Split(ansi.Strip(m.View().Content), "\n")
}

func keyPress(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(text)[0], Text: text}
}

func TestMarksAreRectangular(t *testing.T) {
	for name, mark := range map[string][]string{
		"treadle":       treadleMark,
		"heddle":        heddleMark,
		"treadle small": treadleMarkSmall,
		"heddle small":  heddleMarkSmall,
	} {
		for y, row := range mark {
			if len(row) != len(mark[0]) {
				t.Errorf("%s row %d has width %d, want %d", name, y, len(row), len(mark[0]))
			}
		}
	}
}

func TestLockupKeepsItsSizeInEveryState(t *testing.T) {
	states := []threadState{threadIdle, threadConnecting, threadFailed}
	for _, lockup := range []brandLockup{fullLockup, smallLockup} {
		for _, state := range states {
			for frame := range 20 {
				rows := lockup.render(state, frame)
				if len(rows) != lockup.rows() {
					t.Fatalf("state %d frame %d: got %d rows, want %d", state, frame, len(rows), lockup.rows())
				}
				for _, row := range rows {
					if got := lipgloss.Width(row); got != lockup.width() {
						t.Fatalf("state %d frame %d: row width %d, want %d", state, frame, got, lockup.width())
					}
				}
			}
		}
	}
}

func TestConnectScreenDrawsWhatIsClickable(t *testing.T) {
	for _, size := range [][2]int{{72, 17}, {80, 19}, {80, 20}, {80, 24}, {100, 29}, {100, 30}, {110, 36}, {200, 60}} {
		m := sizedModel(size[0], size[1])
		lines := screenLines(m)
		l := m.connectLayout

		if len(lines) != size[1] {
			t.Errorf("%v: screen has %d lines", size, len(lines))
		}
		if !strings.Contains(lines[l.host.y], "HOST") {
			t.Errorf("%v: no HOST label on row %d: %q", size, l.host.y, lines[l.host.y])
		}
		if !strings.Contains(lines[l.key.y], "API KEY") {
			t.Errorf("%v: no API KEY label on row %d: %q", size, l.key.y, lines[l.key.y])
		}
		labelRow := l.button.y + l.button.h/2
		if !strings.Contains(lines[labelRow], "Connect") {
			t.Errorf("%v: no button label on row %d: %q", size, labelRow, lines[labelRow])
		}
		if !strings.Contains(lines[l.browser.y], "Sign in with browser") {
			t.Errorf("%v: no browser sign-in on row %d: %q", size, l.browser.y, lines[l.browser.y])
		}
		if strings.TrimSpace(lines[l.statusY]) == "" {
			t.Errorf("%v: status row %d is empty", size, l.statusY)
		}
		if l.statusY >= size[1]-footerHeight {
			t.Errorf("%v: status row %d collides with the footer", size, l.statusY)
		}
	}
}

func TestClickingTheKeyFieldFocusesIt(t *testing.T) {
	m := sizedModel(110, 36)

	m, _ = m.handleConnectClick(m.connectLayout.key.x+1, m.connectLayout.key.y+1)

	if !m.apiKeyInput.Focused() || m.hostInput.Focused() {
		t.Fatalf("key focused = %v, host focused = %v", m.apiKeyInput.Focused(), m.hostInput.Focused())
	}
}

func TestClickingTheButtonConnects(t *testing.T) {
	m := sizedModel(110, 36)

	m, cmd := m.handleConnectClick(m.connectLayout.button.x+1, m.connectLayout.button.y)

	if !m.connecting || cmd == nil {
		t.Fatalf("connecting = %v, cmd = %v", m.connecting, cmd)
	}
}

func TestTypingQInAFieldDoesNotQuit(t *testing.T) {
	m := sizedModel(110, 36)
	m.hostInput.SetValue("")

	next, cmd := m.Update(keyPress("q"))

	if got := next.(model).hostInput.Value(); got != "q" {
		t.Fatalf("host = %q, want %q", got, "q")
	}
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("typing q in the host field quit the app")
		}
	}
}

func TestQQuitsOutsideTextFields(t *testing.T) {
	m := sizedModel(110, 36)
	m.screen = screenApp
	m.hostInput.Blur()

	_, cmd := m.Update(keyPress("q"))

	if cmd == nil {
		t.Fatal("q returned no command")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Fatal("q did not quit")
	}
}

func TestDialErrorKeepsTheReason(t *testing.T) {
	err := errors.New("dial tcp 127.0.0.1:4000: connect: connection refused")

	got := describeDialError("localhost:4000", err)

	if want := "Can't reach localhost:4000: connection refused"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func appModel(width, height int, workflows ...workflow) model {
	m := sizedModel(width, height)
	m.screen = screenApp
	m.connected = true
	m.activeHost = "http://localhost:4000"
	m.activeAPIKey = "hd_live_abcdefgh_secret"
	m.apiKeyInput.SetValue(m.activeAPIKey)
	m.hostInput.Blur()
	m.workflows = workflows
	return m
}

func TestRejectedKeyReturnsToLogin(t *testing.T) {
	m := appModel(110, 36)
	m.loadingWorkflows = true

	next, _ := m.Update(workflowsLoadedMsg{err: errUnauthorized})
	got := next.(model)

	if got.screen != screenConnect {
		t.Fatalf("screen = %q", got.screen)
	}
	if !got.apiKeyInput.Focused() {
		t.Fatal("API key field is not focused")
	}
	if !strings.Contains(strings.Join(screenLines(got), "\n"), "The server rejected this API key.") {
		t.Fatal("login does not explain the rejection")
	}
}

func login(m model, host, token string) model {
	next, _ := m.Update(connectionResultMsg{host: host, apiKey: token})
	return next.(model)
}

func TestRejectedKeyDoesNotReplaceTheSavedOne(t *testing.T) {
	keyring.MockInit()
	good := savedSession{Host: "http://localhost:4000", Token: "the-good-key", Method: loginAPIKey}
	if err := saveSession(good); err != nil {
		t.Fatal(err)
	}

	m := sizedModel(110, 36)
	m = login(m, "http://localhost:4000", "a-bad-key")
	m.Update(workflowsLoadedMsg{err: errUnauthorized})

	if saved, _ := loadSession(); saved != good {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestASavedLoginTheServerRefusesIsForgotten(t *testing.T) {
	keyring.MockInit()
	if err := saveSession(savedSession{Host: "http://localhost:4000", Token: "revoked", Method: loginAPIKey}); err != nil {
		t.Fatal(err)
	}

	m := sizedModel(110, 36)
	m = login(m, "http://localhost:4000", "revoked")
	m.Update(workflowsLoadedMsg{err: errUnauthorized})

	if _, ok := loadSession(); ok {
		t.Fatal("the refused login is still saved")
	}
}

func TestAcceptedLoginSurvivesARestart(t *testing.T) {
	keyring.MockInit()

	m := sizedModel(110, 36)
	m = login(m, "http://localhost:4000", "a-good-key")
	m.Update(workflowsLoadedMsg{})

	restarted := initialModel()
	if restarted.savedSession.Token != "a-good-key" || !restarted.connecting {
		t.Fatalf("session = %+v, connecting = %v", restarted.savedSession, restarted.connecting)
	}
	if got := restarted.apiKeyInput.Value(); got != "a-good-key" {
		t.Fatalf("key field = %q", got)
	}
	if restarted.Init() == nil {
		t.Fatal("restart does not reconnect")
	}
}

func TestLoginPersistsWithoutAKeyring(t *testing.T) {
	keyring.MockInitWithError(errors.New("The name is not activatable"))
	t.Cleanup(keyring.MockInit)
	t.Cleanup(removeCredentialsFile)

	session := savedSession{Host: "http://localhost:4000", Token: "a-good-key", Method: loginAPIKey}
	if err := saveSession(session); err != nil {
		t.Fatal(err)
	}

	if got, ok := loadSession(); !ok || got != session {
		t.Fatalf("got %+v, %v", got, ok)
	}

	path, err := credentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("credentials file mode = %o", mode)
	}
}

func TestSessionsFromOlderVersionsStillLoad(t *testing.T) {
	keyring.MockInit()
	keyring.Set("treadle", "host", "http://localhost:4000")
	keyring.Set("treadle", "api_key", "old-key")

	got, ok := loadSession()

	if !ok || got.Token != "old-key" || got.Method != loginAPIKey {
		t.Fatalf("got %+v, %v", got, ok)
	}
}

func TestLogOutForgetsTheLogin(t *testing.T) {
	keyring.MockInit()
	m := sizedModel(110, 36)
	m = login(m, "http://localhost:4000", "a-good-key")
	next, _ := m.Update(workflowsLoadedMsg{})

	next, _ = next.Update(keyPress("L"))
	got := next.(model)

	if got.screen != screenConnect || got.apiKeyInput.Value() != "" {
		t.Fatalf("screen = %q, key = %q", got.screen, got.apiKeyInput.Value())
	}
	if _, ok := loadSession(); ok {
		t.Fatal("the login is still saved")
	}
}

func TestSavingANewWorkflowCreatesItOnTheServer(t *testing.T) {
	var method, path string
	var body workflowRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(workflowResponse{Data: apiFlow{ID: 42, Name: "Created"}})
	}))
	defer server.Close()

	m := editorModel(110, 36)
	m.activeHost = server.URL

	cmd := m.saveCurrentWorkflow()
	if cmd == nil {
		t.Fatalf("no save ran: %q", m.editorStatusMessage)
	}
	next, _ := m.Update(cmd())

	if method != http.MethodPost || path != "/api/v1/workflows" {
		t.Fatalf("%s %s", method, path)
	}
	if len(body.Workflow.Nodes) != 2 {
		t.Fatalf("sent %d nodes", len(body.Workflow.Nodes))
	}
	if got := next.(model).editorWorkflowID; got != 42 {
		t.Fatalf("workflow id = %d", got)
	}
}

func TestSavingAnExistingWorkflowUpdatesIt(t *testing.T) {
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		json.NewEncoder(w).Encode(workflowResponse{Data: apiFlow{ID: 7}})
	}))
	defer server.Close()

	m := editorModel(110, 36)
	m.activeHost = server.URL
	m.editorWorkflowID = 7
	m.editorDirty = true

	m.saveCurrentWorkflow()()

	if method != http.MethodPatch || path != "/api/v1/workflows/7" {
		t.Fatalf("%s %s", method, path)
	}
}

func TestClickingARowSelectsTheWorkflowDrawnThere(t *testing.T) {
	m := appModel(110, 36, workflow{id: 1, name: "First"}, workflow{id: 2, name: "Second"})
	lines := screenLines(m)

	row := m.workflowListRect.y + 1
	if !strings.Contains(lines[row], "Second") {
		t.Fatalf("row %d is %q", row, lines[row])
	}

	m, _ = m.handleAppClick(m.workflowListRect.x+2, row)

	if m.selectedWorkflow != 1 {
		t.Fatalf("selected = %d", m.selectedWorkflow)
	}
}

func TestAppPaneStates(t *testing.T) {
	failed := appModel(110, 36)
	failed.workflowsError = "workflows api returned 500 Internal Server Error"

	loading := appModel(110, 36)
	loading.loadingWorkflows = true

	selected := appModel(110, 36, workflow{name: "Sync leads", status: "Active", updated: "just now", created: "today"})

	newOne := appModel(110, 36, workflow{name: "Sync leads"})
	newOne.selectedWorkflow = 1

	for want, m := range map[string]model{
		"No workflows yet":        appModel(110, 36),
		"Couldn't load workflows": failed,
		"Loading workflows":       loading,
		"Connections":             selected,
		"Start a new workflow":    newOne,
	} {
		if !strings.Contains(strings.Join(screenLines(m), "\n"), want) {
			t.Errorf("screen does not show %q", want)
		}
	}
}

func TestAppScreenFillsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{72, 17}, {80, 24}, {110, 36}, {200, 60}} {
		m := appModel(size[0], size[1], workflow{name: "A long workflow name that needs to be cut short here", status: "Failed"})

		lines := screenLines(m)

		if len(lines) != size[1] {
			t.Fatalf("%v: screen has %d lines", size, len(lines))
		}
		if !strings.Contains(lines[size[1]-1], "quit") {
			t.Fatalf("%v: footer is not on the last line: %q", size, lines[size[1]-1])
		}
		inputRow := lines[m.commandInputRect.y]
		if !strings.Contains(inputRow, "Type a command") {
			t.Fatalf("%v: command input is not on row %d: %q", size, m.commandInputRect.y, inputRow)
		}
		for i, line := range lines {
			if width := lipgloss.Width(line); width > size[0] {
				t.Fatalf("%v: line %d is %d wide: %q", size, i, width, line)
			}
		}
	}
}

func editorModel(width, height int) model {
	m := appModel(width, height)
	m.beginNewWorkflowEditor()
	m.reflow()
	return m
}

func TestEditorScreenFillsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{72, 17}, {80, 24}, {110, 36}, {200, 60}} {
		m := editorModel(size[0], size[1])

		lines := screenLines(m)

		if len(lines) != size[1] {
			t.Fatalf("%v: screen has %d lines", size, len(lines))
		}
		if !strings.Contains(lines[size[1]-1], "c connect") {
			t.Fatalf("%v: footer is not on the last line: %q", size, lines[size[1]-1])
		}
		for i, line := range lines {
			if width := lipgloss.Width(line); width > size[0] {
				t.Fatalf("%v: line %d is %d wide: %q", size, i, width, line)
			}
		}
	}
}

func TestNodesAreDrawnWhereTheyAreClickable(t *testing.T) {
	m := editorModel(110, 36)
	lines := screenLines(m)

	for _, node := range m.editorNodes {
		r := m.nodeRects[node.id]
		row := []rune(lines[r.y+1])
		label := string(row[r.x+4 : r.x+4+len(node.label)])
		if label != node.label {
			t.Errorf("node %q: found %q at (%d, %d)", node.label, label, r.x+4, r.y+1)
		}
	}
}

func TestPaletteRowsMatchTheirClickTargets(t *testing.T) {
	m := editorModel(110, 36)
	lines := screenLines(m)

	checked := 0
	for row := range m.paletteRect.h {
		kind, ok := m.paletteNodeKindAtRow(row - 2)
		if !ok {
			continue
		}
		checked++
		def, _ := m.nodeDefinition(kind)
		if line := lines[m.paletteRect.y+row]; !strings.Contains(line, def.Title) {
			t.Errorf("row %d should show %q: %q", row, def.Title, line)
		}
	}
	if checked == 0 {
		t.Fatal("no palette rows found")
	}
}

func TestSaveLabelStaysInItsClickArea(t *testing.T) {
	m := editorModel(110, 36)
	m.editorDirty = true
	m.editorStatusMessage = "Unsaved changes"

	r := m.workflowSaveButtonRect()
	row := []rune(screenLines(m)[r.y])
	if got := string(row[r.x : r.x+r.w]); got != "Save" {
		t.Fatalf("found %q at the save button", got)
	}
}

func TestPaletteIconsAreOneCellWide(t *testing.T) {
	for _, icon := range []string{"play", "link", "variable", "code", "globe", "arrow", "chat", "check", "database", "mail", "file", "key", "user", "clock", "trash", "stop", "other"} {
		if got := lipgloss.Width(mapIconToTerminal(icon)); got != 1 {
			t.Errorf("%s is %d cells wide", icon, got)
		}
	}
}

var serverNodes = []apiNodeDef{
	{Type: "Start", Title: "Start", Category: "flow_control", Outputs: []any{"exec"}},
	{Type: "Webhook", Title: "Webhook", Category: "trigger", Outputs: []any{"exec"}},
	{Type: "Condition", Title: "Condition", Category: "flow_control", Inputs: []any{"exec"}, Outputs: []any{"true", "false"}},
	{Type: "HTTP Request", Title: "HTTP Request", Category: "integration", Inputs: []any{"exec"}, Outputs: []any{"success", "error"}},
	{Type: "Logger", Title: "Logger", Category: "utility", Inputs: []any{"exec"}, Outputs: []any{"exec"}},
	{Type: "Output", Title: "Output", Category: "flow_control", Inputs: []any{"exec"}},
}

// canvasModel starts with an empty canvas and the node set the server sends.
func canvasModel() model {
	m := editorModel(137, 40)
	m.availableNodes = serverNodes
	m.editorNodes = nil
	m.editorConnections = nil
	m.selectedNode = -1
	m.rebuildNodeRects()
	return m
}

func (m *model) place(kind string, x, y int) editorNode {
	m.addEditorNodeAt(kind, x, y)
	m.selectedNode = -1
	return m.editorNodes[len(m.editorNodes)-1]
}

func (m model) dragWire(from, to [2]int) model {
	next, _ := m.Update(tea.MouseClickMsg{X: from[0], Y: from[1], Button: tea.MouseLeft})
	next, _ = next.Update(tea.MouseMotionMsg{X: to[0], Y: to[1], Button: tea.MouseLeft})
	next, _ = next.Update(tea.MouseReleaseMsg{X: to[0], Y: to[1], Button: tea.MouseLeft})
	return next.(model)
}

func (m model) portScreen(node editorNode, output int) [2]int {
	r := m.nodeRects[node.id]
	return [2]int{r.x + r.w - 1, r.y + outputRow(node, output, len(m.nodeOutputs(node.kind))) - node.y}
}

func (m model) bodyScreen(node editorNode) [2]int {
	r := m.nodeRects[node.id]
	return [2]int{r.x + 2, r.y + 1}
}

func TestWiresTouchBothNodesWhereverTheyAre(t *testing.T) {
	offsets := []int{-40, -24, -14, -6, -1, 0, 1, 6, 14, 24, 40}
	for _, dx := range offsets {
		for _, dy := range []int{-14, -6, -4, -3, -1, 0, 1, 3, 4, 6, 14} {
			m := canvasModel()
			source := m.place("Logger", 48, 15)
			target := m.place("Output", 48+dx, 15+dy)
			if target.x != 48+dx || target.y != 15+dy {
				t.Fatalf("target landed at (%d, %d)", target.x, target.y)
			}

			conn := editorConnection{source: source.id, sourceOutput: "exec", target: target.id, targetInput: "exec"}
			path, ok := m.connectionPath(conn)
			if !ok {
				t.Fatal("no path")
			}

			if got, want := path[0], (canvasPoint{source.x + source.w, source.y + 1}); got != want {
				t.Errorf("dx=%d dy=%d: starts at %v, want %v", dx, dy, got, want)
			}
			if got, want := path[len(path)-1], (canvasPoint{target.x - 1, target.y + 1}); got != want {
				t.Errorf("dx=%d dy=%d: ends at %v, want %v", dx, dy, got, want)
			}
			for i := 0; i+1 < len(path); i++ {
				if path[i].x != path[i+1].x && path[i].y != path[i+1].y {
					t.Errorf("dx=%d dy=%d: diagonal segment %v to %v", dx, dy, path[i], path[i+1])
				}
			}
		}
	}
}

func TestWireDrawsAnUnbrokenLineIntoItsArrow(t *testing.T) {
	m := canvasModel()
	source := m.place("Logger", 50, 16)
	target := m.place("Output", 20, 24)
	m.editorConnections = []editorConnection{{source: source.id, sourceOutput: "exec", target: target.id, targetInput: "exec"}}

	grid := newCanvasGrid(120, 36)
	m.drawConnections(grid)
	path, _ := m.connectionPath(m.editorConnections[0])

	for i := 0; i+1 < len(path); i++ {
		dx, dy := sign(path[i+1].x-path[i].x), sign(path[i+1].y-path[i].y)
		for x, y := path[i].x, path[i].y; x != path[i+1].x || y != path[i+1].y; x, y = x+dx, y+dy {
			if grid[y][x].ch == ' ' {
				t.Fatalf("gap in the wire at (%d, %d)", x, y)
			}
		}
	}
	last := path[len(path)-1]
	if grid[last.y][last.x].ch != '▶' {
		t.Fatalf("wire ends in %q", grid[last.y][last.x].ch)
	}
}

func TestDraggingFromAPortConnectsThroughThatOutput(t *testing.T) {
	m := canvasModel()
	condition := m.place("Condition", 10, 4)
	output := m.place("Output", 60, 4)

	m = m.dragWire(m.portScreen(condition, 1), m.bodyScreen(output))

	if len(m.editorConnections) != 1 {
		t.Fatalf("connections = %v", m.editorConnections)
	}
	want := editorConnection{source: condition.id, sourceOutput: "false", target: output.id, targetInput: "exec"}
	if m.editorConnections[0] != want {
		t.Fatalf("got %+v, want %+v", m.editorConnections[0], want)
	}
	if m.editorMode != editorModeNormal || m.wireActive {
		t.Fatalf("mode = %q, wire = %v", m.editorMode, m.wireActive)
	}
}

func TestClickingAPortThenATargetConnects(t *testing.T) {
	m := canvasModel()
	logger := m.place("Logger", 10, 4)
	output := m.place("Output", 60, 4)

	port := m.portScreen(logger, 0)
	m = m.dragWire(port, port)
	if m.editorMode != editorModeConnect {
		t.Fatalf("mode = %q after clicking the port", m.editorMode)
	}

	body := m.bodyScreen(output)
	next, _ := m.Update(tea.MouseClickMsg{X: body[0], Y: body[1], Button: tea.MouseLeft})
	m = next.(model)

	if len(m.editorConnections) != 1 {
		t.Fatalf("connections = %v", m.editorConnections)
	}
}

func TestDroppingAWireOnEmptyCanvasCancels(t *testing.T) {
	m := canvasModel()
	logger := m.place("Logger", 10, 4)

	m = m.dragWire(m.portScreen(logger, 0), [2]int{m.canvasRect.x + 80, m.canvasRect.y + 25})

	if len(m.editorConnections) != 0 || m.editorMode != editorModeNormal {
		t.Fatalf("connections = %v, mode = %q", m.editorConnections, m.editorMode)
	}
}

func TestConnectingTheSamePairAgainRemovesTheLink(t *testing.T) {
	m := canvasModel()
	logger := m.place("Logger", 10, 4)
	output := m.place("Output", 60, 4)

	m = m.dragWire(m.portScreen(logger, 0), m.bodyScreen(output))
	m = m.dragWire(m.portScreen(logger, 0), m.bodyScreen(output))

	if len(m.editorConnections) != 0 {
		t.Fatalf("connections = %v", m.editorConnections)
	}
}

func TestNodesWithoutInputsRefuseConnections(t *testing.T) {
	m := canvasModel()
	logger := m.place("Logger", 10, 4)
	webhook := m.place("Webhook", 60, 4)

	m = m.dragWire(m.portScreen(logger, 0), m.bodyScreen(webhook))

	if len(m.editorConnections) != 0 {
		t.Fatalf("connections = %v", m.editorConnections)
	}
	if !strings.Contains(m.editorStatusMessage, "no inputs") {
		t.Fatalf("status = %q", m.editorStatusMessage)
	}
}

func TestDraggingANodeBodyStillMovesIt(t *testing.T) {
	m := canvasModel()
	logger := m.place("Logger", 10, 4)

	body := m.bodyScreen(logger)
	m = m.dragWire(body, [2]int{body[0] + 20, body[1] + 6})

	if len(m.editorConnections) != 0 {
		t.Fatalf("connections = %v", m.editorConnections)
	}
	if m.editorNodes[0].x != 30 || m.editorNodes[0].y != 10 {
		t.Fatalf("node at (%d, %d)", m.editorNodes[0].x, m.editorNodes[0].y)
	}
}

func TestAddingANodeContinuesFromTheSelectedOne(t *testing.T) {
	m := canvasModel()
	start := m.place("Start", 4, 4)
	m.selectedNode = 0

	m.addEditorNode("Condition")
	m.addEditorNode("Logger")
	m.addEditorNode("Output")

	if len(m.editorConnections) != 3 {
		t.Fatalf("connections = %+v", m.editorConnections)
	}
	if got := m.editorConnections[0]; got.source != start.id || got.sourceOutput != "exec" {
		t.Fatalf("first link = %+v", got)
	}
	if got := m.editorConnections[1].sourceOutput; got != "true" {
		t.Fatalf("second link leaves through %q", got)
	}
	for i, node := range m.editorNodes {
		for _, other := range m.editorNodes[i+1:] {
			if node.x < other.x+other.w && other.x < node.x+node.w && node.y < other.y+other.h && other.y < node.y+node.h {
				t.Errorf("%s overlaps %s", node.label, other.label)
			}
		}
	}
}

func TestAddingANodeWithNothingSelectedLeavesItUnconnected(t *testing.T) {
	m := canvasModel()
	m.place("Start", 4, 4)

	m.addEditorNode("Logger")

	if len(m.editorConnections) != 0 {
		t.Fatalf("connections = %+v", m.editorConnections)
	}
}

func TestClickingTheCanvasClearsTheSelection(t *testing.T) {
	m := canvasModel()
	m.place("Start", 4, 4)
	m.selectedNode = 0

	next, _ := m.Update(tea.MouseClickMsg{X: m.canvasRect.x + 80, Y: m.canvasRect.y + 25, Button: tea.MouseLeft})

	if got := next.(model).selectedNode; got != -1 {
		t.Fatalf("selected = %d", got)
	}
}

func TestNodeIDsStayUniqueAfterADelete(t *testing.T) {
	m := canvasModel()
	m.place("Logger", 10, 4)
	m.place("Logger", 40, 4)
	m.place("Logger", 70, 4)
	m.selectedNode = 1
	m.deleteSelectedEditorNode()

	m.addEditorNodeAt("Logger", 10, 20)

	seen := map[string]bool{}
	for _, node := range m.editorNodes {
		if seen[node.id] {
			t.Fatalf("duplicate id %q", node.id)
		}
		seen[node.id] = true
	}
}

func TestLoadingAndSavingKeepsBranchPorts(t *testing.T) {
	m := canvasModel()
	flow := apiFlow{
		Nodes: []apiNode{
			{ID: "a", Type: "Condition", Label: "Condition"},
			{ID: "b", Type: "Output", Label: "Output", Position: apiPosition{X: 300}},
		},
		Connections: []apiConnection{{Source: "a", SourceOutput: "false", Target: "b", TargetInput: "exec"}},
	}

	m.editorNodes, m.editorConnections = m.mapAPIFlowToEditor(flow)
	payload := m.workflowRequestPayload().Workflow

	if got := payload.Connections[0]; got.SourceOutput != "false" || got.TargetInput != "exec" {
		t.Fatalf("connection = %+v", got)
	}
	if got := payload.Nodes[0].Type; got != "Condition" {
		t.Fatalf("type = %q", got)
	}
}

func TestBranchingNodesShowEveryOutput(t *testing.T) {
	m := canvasModel()
	m.place("Condition", 10, 4)

	screen := strings.Join(screenLines(m), "\n")

	for _, output := range []string{"true", "false"} {
		if !strings.Contains(screen, output) {
			t.Errorf("screen does not show the %q output", output)
		}
	}
}

func spreadNodes(m *model) []editorNode {
	return []editorNode{m.place("Logger", 10, 4), m.place("Logger", 40, 4), m.place("Logger", 70, 20)}
}

func (m model) press(p [2]int, mod tea.KeyMod) model {
	next, _ := m.Update(tea.MouseClickMsg{X: p[0], Y: p[1], Button: tea.MouseLeft, Mod: mod})
	next, _ = next.Update(tea.MouseReleaseMsg{X: p[0], Y: p[1], Button: tea.MouseLeft, Mod: mod})
	return next.(model)
}

func (m model) wheel(button tea.MouseButton, mod tea.KeyMod) model {
	x, y := m.canvasRect.x+40, m.canvasRect.y+15
	next, _ := m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: button, Mod: mod})
	return next.(model)
}

func TestDraggingOnEmptyCanvasSelectsEveryNodeTouched(t *testing.T) {
	m := canvasModel()
	nodes := spreadNodes(&m)

	r := m.nodeRects[nodes[1].id]
	m = m.dragWire([2]int{m.canvasRect.x + 3, m.canvasRect.y + 3}, [2]int{r.x + 2, r.y + 2})

	if got := m.selectedIndices(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("selected = %v", got)
	}
	if m.marqueeActive {
		t.Fatal("the box is still open")
	}
}

func TestTheSelectionBoxIsDrawnWhileDragging(t *testing.T) {
	m := canvasModel()
	from := [2]int{m.canvasRect.x + 20, m.canvasRect.y + 25}

	next, _ := m.Update(tea.MouseClickMsg{X: from[0], Y: from[1], Button: tea.MouseLeft})
	next, _ = next.Update(tea.MouseMotionMsg{X: from[0] + 12, Y: from[1] + 5, Button: tea.MouseLeft})

	if !strings.Contains(strings.Join(screenLines(next.(model)), "\n"), "┌╌") {
		t.Fatal("no selection box on screen")
	}
}

func TestDraggingASelectedNodeMovesTheWholeSelection(t *testing.T) {
	m := canvasModel()
	nodes := spreadNodes(&m)
	m.setSelection([]int{0, 2})

	body := m.bodyScreen(nodes[0])
	m = m.dragWire(body, [2]int{body[0] + 5, body[1] + 3})

	if got := m.editorNodes[0]; got.x != 15 || got.y != 7 {
		t.Fatalf("first node at (%d, %d)", got.x, got.y)
	}
	if got := m.editorNodes[2]; got.x != 75 || got.y != 23 {
		t.Fatalf("last node at (%d, %d)", got.x, got.y)
	}
	if got := m.editorNodes[1]; got.x != 40 || got.y != 4 {
		t.Fatalf("unselected node moved to (%d, %d)", got.x, got.y)
	}
}

func TestCtrlClickTogglesNodesInTheSelection(t *testing.T) {
	m := canvasModel()
	nodes := spreadNodes(&m)

	m = m.press(m.bodyScreen(nodes[0]), 0)
	m = m.press(m.bodyScreen(nodes[2]), tea.ModCtrl)
	if got := m.selectedIndices(); len(got) != 2 {
		t.Fatalf("selected = %v", got)
	}

	m = m.press(m.bodyScreen(nodes[0]), tea.ModCtrl)
	if got := m.selectedIndices(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("selected = %v", got)
	}
}

func TestDeleteRemovesTheSelectionButKeepsStart(t *testing.T) {
	m := canvasModel()
	start := m.place("Start", 4, 4)
	first := m.place("Logger", 30, 4)
	second := m.place("Logger", 60, 4)
	m.editorConnections = []editorConnection{{source: start.id, target: first.id}, {source: first.id, target: second.id}}
	m.selectAllNodes()

	m.deleteSelectedEditorNode()

	if len(m.editorNodes) != 1 || m.editorNodes[0].id != start.id {
		t.Fatalf("nodes = %+v", m.editorNodes)
	}
	if len(m.editorConnections) != 0 {
		t.Fatalf("connections = %+v", m.editorConnections)
	}
}

func TestWheelScrollsTheCanvasBothWays(t *testing.T) {
	m := canvasModel()
	node := m.place("Logger", 40, 10)
	before := m.nodeRects[node.id]

	m = m.wheel(tea.MouseWheelDown, 0)
	if got := m.nodeRects[node.id]; got.y != before.y-wheelRows || got.x != before.x {
		t.Fatalf("after scrolling down: %+v, was %+v", got, before)
	}

	m = m.wheel(tea.MouseWheelRight, 0)
	if got := m.nodeRects[node.id]; got.x != before.x-wheelColumns {
		t.Fatalf("after scrolling right: %+v, was %+v", got, before)
	}

	m = m.wheel(tea.MouseWheelUp, tea.ModShift)
	if got := m.nodeRects[node.id]; got.x != before.x {
		t.Fatalf("after shift-scrolling back: %+v, was %+v", got, before)
	}
}

func TestScrollingDoesNotChangeTheWorkflow(t *testing.T) {
	m := canvasModel()
	m.place("Logger", 40, 10)
	m.editorDirty = false

	m = m.wheel(tea.MouseWheelDown, 0)

	if m.editorNodes[0].y != 10 || m.editorDirty {
		t.Fatalf("y = %d, dirty = %v", m.editorNodes[0].y, m.editorDirty)
	}
}

func TestCtrlWheelZoomsAroundTheCursor(t *testing.T) {
	m := canvasModel()
	content := m.canvasContentRect()
	anchorX, anchorY := m.canvasRect.x+40-content.x, m.canvasRect.y+15-content.y
	beforeX, beforeY := m.toWorld(anchorX, anchorY)

	m = m.wheel(tea.MouseWheelDown, tea.ModCtrl)

	if got := m.zoom(); got != 0.75 {
		t.Fatalf("zoom = %v", got)
	}
	afterX, afterY := m.toWorld(anchorX, anchorY)
	if abs(afterX-beforeX) > 1 || abs(afterY-beforeY) > 1 {
		t.Fatalf("point under the cursor moved from (%d, %d) to (%d, %d)", beforeX, beforeY, afterX, afterY)
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func TestZoomKeysStopAtTheirLimits(t *testing.T) {
	m := canvasModel()

	for range 10 {
		next, _ := m.Update(keyPress("-"))
		m = next.(model)
	}
	if m.zoom() != zoomLevels[0] {
		t.Fatalf("zoom = %v", m.zoom())
	}

	for range 10 {
		next, _ := m.Update(keyPress("+"))
		m = next.(model)
	}
	if m.zoom() != zoomLevels[len(zoomLevels)-1] {
		t.Fatalf("zoom = %v", m.zoom())
	}

	next, _ := m.Update(keyPress("0"))
	if got := next.(model).zoom(); got != 1 {
		t.Fatalf("zoom = %v", got)
	}
}

func TestInteractionsWorkAtEveryZoomAndScroll(t *testing.T) {
	for index := range zoomLevels {
		m := canvasModel()
		logger := m.place("Logger", 10, 4)
		output := m.place("Output", 60, 12)
		m.zoomIndex = index
		m.panBy(-7, -3)

		m = m.dragWire(m.portScreen(logger, 0), m.bodyScreen(output))
		if len(m.editorConnections) != 1 {
			t.Fatalf("zoom %v: connections = %+v", zoomLevels[index], m.editorConnections)
		}

		lines := []rune(screenLines(m)[m.nodeRects[output.id].y])
		if lines[m.nodeRects[output.id].x] != '╭' {
			t.Fatalf("zoom %v: node is not drawn where it is clickable", zoomLevels[index])
		}

		m.clearSelection()
		body := m.bodyScreen(logger)
		start := m.editorNodes[0]
		m = m.dragWire(body, [2]int{body[0] + 6, body[1] + 3})
		z := zoomLevels[index]
		if got := m.editorNodes[0]; got.x != start.x+scaled(6, 1/z) || got.y != start.y+scaled(3, 1/z) {
			t.Fatalf("zoom %v: node moved to (%d, %d) from (%d, %d)", z, got.x, got.y, start.x, start.y)
		}
	}
}

func TestNodesScrolledOutOfViewCantBeClicked(t *testing.T) {
	m := canvasModel()
	node := m.place("Logger", 10, 4)
	m.panBy(100, 0)
	r := m.nodeRects[node.id]

	if got := m.nodeIndexAt(r.x+1, r.y+1); got != -1 {
		t.Fatalf("hit node %d behind the palette", got)
	}
}

func TestFitShowsEveryNode(t *testing.T) {
	m := canvasModel()
	m.place("Logger", -200, -40)
	m.place("Logger", 90, 12)
	m.place("Output", 400, 90)

	m.fitView()

	content := m.canvasContentRect()
	for _, node := range m.editorNodes {
		r := m.nodeRects[node.id]
		if r.x < content.x || r.y < content.y || r.x+r.w > content.x+content.w || r.y+r.h > content.y+content.h {
			if m.zoom() != zoomLevels[0] {
				t.Errorf("node %s at %+v is outside %+v at zoom %v", node.id, r, content, m.zoom())
			}
		}
	}
}

func TestPositionsSurviveAnOpenAndSave(t *testing.T) {
	m := canvasModel()
	flow := apiFlow{ID: 1, Nodes: []apiNode{
		{ID: "a", Type: "Start", Label: "Start", Position: apiPosition{X: -120, Y: 40}},
		{ID: "b", Type: "Output", Label: "Output", Position: apiPosition{X: 2400, Y: 1600}},
	}}

	m.beginWorkflowEditorFromAPI(flow)
	payload := m.workflowRequestPayload().Workflow

	if got := payload.Nodes[0].Position; got.X != -120 || got.Y != 40 {
		t.Fatalf("first node saved at %+v", got)
	}
	if got := payload.Nodes[1].Position; got.X != 2400 || got.Y != 1600 {
		t.Fatalf("second node saved at %+v", got)
	}
}

func TestAddingANodePastTheEdgeScrollsToIt(t *testing.T) {
	m := canvasModel()
	m.place("Start", 90, 4)
	m.selectedNode = 0

	for range 4 {
		m.addEditorNode("Logger")
	}

	content := m.canvasContentRect()
	r := m.nodeRects[m.editorNodes[len(m.editorNodes)-1].id]
	if r.x+r.w > content.x+content.w {
		t.Fatalf("new node at %+v is outside %+v", r, content)
	}
}

func TestUnmovedNodesKeepTheirExactPosition(t *testing.T) {
	m := canvasModel()
	m.beginWorkflowEditorFromAPI(apiFlow{ID: 1, Nodes: []apiNode{
		{ID: "a", Type: "Start", Label: "Start", Position: apiPosition{X: 133.5, Y: 47.25}},
		{ID: "b", Type: "Output", Label: "Output", Position: apiPosition{X: 500, Y: 100}},
	}})

	m.editorNodes[1].x += 10
	payload := m.workflowRequestPayload().Workflow

	if got := payload.Nodes[0].Position; got.X != 133.5 || got.Y != 47.25 {
		t.Fatalf("unmoved node saved at %+v", got)
	}
	if got := payload.Nodes[1].Position; got.X != 600 {
		t.Fatalf("moved node saved at %+v", got)
	}
}

func TestNodesApartHereStayApartInTheWebEditor(t *testing.T) {
	const webNodeSize = 88

	m := canvasModel()
	start := m.place("Start", 4, 3)
	below := m.place("Output", 4, 3+start.h+1)
	beside := m.place("Output", 4+start.w+1, 3)

	nodes := map[string]apiPosition{}
	for _, node := range m.workflowRequestPayload().Workflow.Nodes {
		nodes[node.ID.(string)] = node.Position
	}

	if gap := nodes[below.id].Y - nodes[start.id].Y; gap <= webNodeSize {
		t.Errorf("stacked nodes are %vpx apart", gap)
	}
	if gap := nodes[beside.id].X - nodes[start.id].X; gap <= webNodeSize {
		t.Errorf("side-by-side nodes are %vpx apart", gap)
	}
}

// oauthServer stands in for Heddle, and checks the PKCE proof like it does.
func oauthServer(t *testing.T, requests *[]string) *httptest.Server {
	t.Helper()
	challenge := ""
	mux := http.NewServeMux()
	var server *httptest.Server

	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(oauthMetadata{
			AuthorizationEndpoint: server.URL + "/oauth/authorize",
			TokenEndpoint:         server.URL + "/api/v1/oauth/token",
		})
	})
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		challenge = r.URL.Query().Get("code_challenge")
	})
	mux.HandleFunc("/api/v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		*requests = append(*requests, r.Form.Get("grant_type"))
		if codeChallenge(r.Form.Get("code_verifier")) != challenge || r.Form.Get("code") != "the-code" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "hd_live_oauthkey_secret", "token_type": "Bearer"})
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// fakeBrowser approves the sign-in: it opens the page, then follows the redirect.
func fakeBrowser(t *testing.T, callbackQuery func(state string) string) {
	t.Helper()
	previous := openBrowser
	t.Cleanup(func() { openBrowser = previous })

	openBrowser = func(target string) error {
		parsed, err := url.Parse(target)
		if err != nil {
			return err
		}
		go func() {
			query := parsed.Query()
			if resp, err := http.Get(target); err == nil {
				resp.Body.Close()
			}
			callback := query.Get("redirect_uri") + "?" + callbackQuery(query.Get("state"))
			if resp, err := http.Get(callback); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func runOAuth(t *testing.T, m model, host string) oauthFinishedMsg {
	t.Helper()
	started, ok := startOAuthCmd(host)().(oauthStartedMsg)
	if !ok {
		t.Fatal("sign-in did not start")
	}
	return (<-started.finished)
}

func TestBrowserSignInEndsWithASavedSession(t *testing.T) {
	keyring.MockInit()
	var requests []string
	server := oauthServer(t, &requests)
	fakeBrowser(t, func(state string) string {
		return url.Values{"code": {"the-code"}, "state": {state}}.Encode()
	})

	m := sizedModel(110, 36)
	m.hostInput.SetValue(server.URL)
	m, _ = m.startOAuth()

	next, _ := m.Update(runOAuth(t, m, server.URL))
	got := next.(model)
	if got.screen != screenApp || got.activeAPIKey != "hd_live_oauthkey_secret" {
		t.Fatalf("screen = %q, token = %q, error = %q", got.screen, got.activeAPIKey, got.connectError)
	}

	next, _ = next.Update(workflowsLoadedMsg{})

	saved, ok := loadSession()
	if !ok || saved.Method != loginOAuth || saved.Token != "hd_live_oauthkey_secret" {
		t.Fatalf("saved = %+v", saved)
	}
	if restarted := initialModel(); !restarted.connecting || restarted.apiKeyInput.Value() != "" {
		t.Fatalf("connecting = %v, key field = %q", restarted.connecting, restarted.apiKeyInput.Value())
	}
}

func TestCallbackWithTheWrongStateIsIgnored(t *testing.T) {
	var requests []string
	server := oauthServer(t, &requests)
	var redirect string
	previous := openBrowser
	t.Cleanup(func() { openBrowser = previous })
	opened := make(chan struct{})
	openBrowser = func(target string) error {
		parsed, _ := url.Parse(target)
		redirect = parsed.Query().Get("redirect_uri")
		close(opened)
		return nil
	}

	started := startOAuthCmd(server.URL)().(oauthStartedMsg)
	<-opened

	resp, err := http.Get(redirect + "?code=the-code&state=forged")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	started.cancel()
	result := <-started.finished
	if result.err == nil || len(requests) != 0 {
		t.Fatalf("err = %v, token requests = %v", result.err, requests)
	}
}

func TestCancellingInTheBrowserIsReported(t *testing.T) {
	keyring.MockInit()
	var requests []string
	server := oauthServer(t, &requests)
	fakeBrowser(t, func(state string) string {
		return url.Values{"error": {"access_denied"}, "state": {state}}.Encode()
	})

	m := sizedModel(110, 36)
	m, _ = m.startOAuth()

	next, _ := m.Update(runOAuth(t, m, server.URL))

	got := next.(model)
	if got.screen != screenConnect || !strings.Contains(got.connectError, "cancelled") {
		t.Fatalf("screen = %q, error = %q", got.screen, got.connectError)
	}
}

func TestOlderServersSayBrowserSignInIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	result, ok := startOAuthCmd(server.URL)().(oauthFinishedMsg)

	if !ok || !errors.Is(result.err, errOAuthUnsupported) {
		t.Fatalf("result = %+v", result)
	}
}

func TestEscCancelsAWaitingSignIn(t *testing.T) {
	m := sizedModel(110, 36)
	m.oauthWaiting = true
	cancelled := false
	m.oauthCancel = func() { cancelled = true }

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if !cancelled || next.(model).oauthWaiting {
		t.Fatalf("cancelled = %v, waiting = %v", cancelled, next.(model).oauthWaiting)
	}
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("esc quit the app")
		}
	}
}

func TestTabReachesTheBrowserButtonAndEnterStartsIt(t *testing.T) {
	m := sizedModel(110, 36)
	for range 3 {
		m = m.focusNextConnectControl(false)
	}
	if !m.browserButtonFocused {
		t.Fatal("browser button is not focused")
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !next.(model).oauthWaiting {
		t.Fatal("enter did not start the browser sign-in")
	}
}

func TestClickingTheBrowserButtonStartsSignIn(t *testing.T) {
	m := sizedModel(110, 36)
	r := m.connectLayout.browser

	m, cmd := m.handleConnectClick(r.x+r.w/2, r.y)

	if !m.oauthWaiting || cmd == nil {
		t.Fatalf("waiting = %v", m.oauthWaiting)
	}
}
