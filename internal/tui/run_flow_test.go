package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func init() {
	executionPollInterval = time.Millisecond
	executionWaitPollInterval = time.Millisecond
}

// heddleStub answers like the server: a run starts queued and each read moves it
// to the next status in steps.
type heddleStub struct {
	mu       sync.Mutex
	saves    int
	created  map[string]any
	requests []string
	steps    []apiExecution
}

func (s *heddleStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/workflows/7/executions":
		json.NewDecoder(r.Body).Decode(&s.created)
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(executionResponse{Data: apiExecution{ID: "blue-orbit-run-ab12", Status: "queued"}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/workflows/7/executions/"):
		next := s.steps[0]
		if len(s.steps) > 1 {
			s.steps = s.steps[1:]
		}
		next.ID = "blue-orbit-run-ab12"
		json.NewEncoder(w).Encode(executionResponse{Data: next})
	case r.URL.Path == "/api/v1/workflows" || r.URL.Path == "/api/v1/workflows/7":
		s.saves++
		json.NewEncoder(w).Encode(workflowResponse{Data: apiFlow{ID: 7, Name: "Flow"}})
	default:
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}
}

func runModel(t *testing.T, stub *heddleStub) model {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)

	m := editorModel(110, 36)
	m.activeHost = server.URL
	m.editorWorkflowID = 7
	return m
}

// settle plays a command and the ones it leads to, the way the program would.
func playRun(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	for range 20 {
		if cmd == nil {
			return m
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			var next []tea.Cmd
			for _, c := range batch {
				if c == nil {
					continue
				}
				updated, follow := m.Update(c())
				m = updated.(model)
				next = append(next, follow)
			}
			cmd = tea.Batch(next...)
			continue
		}
		updated, follow := m.Update(msg)
		m = updated.(model)
		cmd = follow
	}
	t.Fatal("the run never settled")
	return m
}

func TestRunningStartsAManualExecutionAndFollowsIt(t *testing.T) {
	stub := &heddleStub{steps: []apiExecution{
		{Status: "running"},
		{Status: "succeeded", Result: map[string]any{"result": 6.0}},
	}}
	m := runModel(t, stub)

	m = playRun(t, m, m.runCurrentWorkflow())

	if stub.created["input"].(map[string]any)["trigger"] != "manual" {
		t.Fatalf("input: %v", stub.created)
	}
	if stub.saves != 0 {
		t.Fatalf("saved %d times with nothing to save", stub.saves)
	}
	if m.editorRunInFlight {
		t.Fatal("still running")
	}
	if !strings.HasPrefix(m.editorStatusMessage, "Execution finished") {
		t.Fatalf("status: %q", m.editorStatusMessage)
	}
}

func TestRunningSavesPendingChangesFirst(t *testing.T) {
	stub := &heddleStub{steps: []apiExecution{{Status: "succeeded"}}}
	m := runModel(t, stub)
	m.editorDirty = true

	m = playRun(t, m, m.runCurrentWorkflow())

	if stub.saves == 0 {
		t.Fatal("ran without saving")
	}
	var saved, ran int
	for i, request := range stub.requests {
		if strings.HasPrefix(request, "PATCH") {
			saved = i
		}
		if request == "POST /api/v1/workflows/7/executions" {
			ran = i
		}
	}
	if saved > ran {
		t.Fatalf("ran before saving: %v", stub.requests)
	}
	if m.editorDirty {
		t.Fatal("still marked as changed")
	}
}

func TestRunningANewWorkflowCreatesItFirst(t *testing.T) {
	stub := &heddleStub{steps: []apiExecution{{Status: "succeeded"}}}
	m := runModel(t, stub)
	m.editorWorkflowID = 0

	m = playRun(t, m, m.runCurrentWorkflow())

	if m.editorWorkflowID != 7 || !strings.HasPrefix(m.editorStatusMessage, "Execution finished") {
		t.Fatalf("id %d, status %q", m.editorWorkflowID, m.editorStatusMessage)
	}
}

func TestAFailedSaveDoesNotRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/executions") {
			t.Error("ran a flow that failed to save")
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	m := editorModel(110, 36)
	m.activeHost = server.URL
	m.editorWorkflowID = 7
	m.editorDirty = true

	m = playRun(t, m, m.runCurrentWorkflow())

	if !strings.HasPrefix(m.editorStatusMessage, "Save failed") {
		t.Fatalf("status: %q", m.editorStatusMessage)
	}
	if m.editorRunAfterSave {
		t.Fatal("a later save would start the run")
	}
}

func TestAFailedRunSaysSo(t *testing.T) {
	stub := &heddleStub{steps: []apiExecution{{
		Status: "failed",
		Error:  map[string]any{"message": "boom", "node_id": "start"},
	}}}
	m := runModel(t, stub)

	m = playRun(t, m, m.runCurrentWorkflow())

	if !strings.HasPrefix(m.editorStatusMessage, "Execution failed") {
		t.Fatalf("status: %q", m.editorStatusMessage)
	}

	m, _ = m.updateWorkflowEditorKey(keyPress("o"))
	view := m.renderWorkflowEditor()
	for _, text := range []string{"Execution Output", "boom", "Start"} {
		if !strings.Contains(view, text) {
			t.Fatalf("no %q in:\n%s", text, view)
		}
	}
}

func TestARunThatCannotStartSaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	m := editorModel(110, 36)
	m.activeHost = server.URL
	m.editorWorkflowID = 7

	m = playRun(t, m, m.runCurrentWorkflow())

	if !strings.HasPrefix(m.editorStatusMessage, "Can't run") || m.editorRunInFlight {
		t.Fatalf("status %q, in flight %v", m.editorStatusMessage, m.editorRunInFlight)
	}
}

func TestARunIsNotStartedTwice(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorRunInFlight = true

	if cmd := m.runCurrentWorkflow(); cmd != nil {
		t.Fatal("started a second run")
	}
}

func TestLosingTheServerDuringARunStopsFollowingIt(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorRunInFlight = true
	m.editorExecution = &apiExecution{ID: "x", Status: "running"}

	for range executionPollRetries {
		updated, _ := m.handleExecutionPolled(executionPolledMsg{workflowID: 7, err: http.ErrHandlerTimeout})
		m = updated.(model)
	}

	if m.editorRunInFlight || !strings.HasPrefix(m.editorStatusMessage, "Can't read") {
		t.Fatalf("in flight %v, status %q", m.editorRunInFlight, m.editorStatusMessage)
	}
}

func TestAnswersForAnotherWorkflowAreIgnored(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorRunInFlight = true
	m.editorExecution = &apiExecution{ID: "x", Status: "running"}
	m.editorWorkflowID = 8

	updated, cmd := m.handleExecutionPolled(executionPolledMsg{
		workflowID: 7,
		execution:  apiExecution{ID: "x", Status: "failed"},
	})
	if cmd != nil || updated.(model).editorExecution.Status != "running" {
		t.Fatal("took the answer of another workflow")
	}
}

func TestOpeningAnotherWorkflowForgetsTheRun(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorRunInFlight = true
	m.editorExecution = &apiExecution{ID: "x", Status: "running"}

	m.beginWorkflowEditor("Other")

	if m.editorRunInFlight || m.editorExecution != nil {
		t.Fatal("kept the run of the previous workflow")
	}
}

func TestCtrlRRuns(t *testing.T) {
	stub := &heddleStub{steps: []apiExecution{{Status: "succeeded"}}}
	m := runModel(t, stub)

	next, cmd := m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if cmd == nil || !next.editorRunInFlight {
		t.Fatalf("no run started: %q", next.editorStatusMessage)
	}
}

func TestClickingRunRuns(t *testing.T) {
	m := runModel(t, &heddleStub{steps: []apiExecution{{Status: "succeeded"}}})
	button := m.workflowRunButtonRect()

	next, cmd := m.handleWorkflowEditorClick(tea.Mouse{X: button.x, Y: button.y, Button: tea.MouseLeft})

	if cmd == nil || !next.editorRunInFlight {
		t.Fatal("clicking Run started nothing")
	}
}

func TestTopBarDrawsRunWhereItIsClickable(t *testing.T) {
	m := runModel(t, &heddleStub{})
	button := m.workflowRunButtonRect()

	line := screenLines(m)[0]
	runes := []rune(line)
	if button.x+button.w > len(runes) || string(runes[button.x:button.x+button.w]) != "Run" {
		t.Fatalf("Run at %d in %q", button.x, line)
	}
}

func TestResultModalShowsTheOutput(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorExecution = &apiExecution{ID: "x", Status: "succeeded", Result: map[string]any{"answer": 42.0}}
	m.openExecutionResult()

	view := m.renderWorkflowEditor()
	if !strings.Contains(view, "Execution Output") || !strings.Contains(view, `"answer": 42`) {
		t.Fatalf("view:\n%s", view)
	}
}

func TestResultModalSaysWhenThereIsNoOutput(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorExecution = &apiExecution{ID: "x", Status: "succeeded", Result: map[string]any{}}
	m.openExecutionResult()

	if view := m.renderWorkflowEditor(); !strings.Contains(view, "No output produced") {
		t.Fatalf("view:\n%s", view)
	}
}

func TestResultModalInspectsTheFullContext(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorExecution = &apiExecution{
		ID:     "x",
		Status: "succeeded",
		Logs:   []map[string]any{{"node_id": "start", "status": "success"}},
	}
	m.openExecutionResult()

	m, _ = m.updateWorkflowEditorKey(keyPress("i"))
	view := m.renderWorkflowEditor()
	if !strings.Contains(view, "Full Context") || !strings.Contains(view, `"node_id": "start"`) {
		t.Fatalf("view:\n%s", view)
	}

	m, _ = m.updateWorkflowEditorKey(keyPress("i"))
	if strings.Contains(m.renderWorkflowEditor(), `"node_id"`) {
		t.Fatal("didn't go back to the output")
	}
}

func TestResultModalScrollsAndCloses(t *testing.T) {
	long := map[string]any{}
	for _, key := range strings.Split("a b c d e f g h i j k l m n o p q r s t u v w x y z aa ab ac ad ae af ag ah ai aj ak al am an", " ") {
		long[key] = key
	}
	m := runModel(t, &heddleStub{})
	m.editorExecution = &apiExecution{ID: "x", Status: "succeeded", Result: long}
	m.openExecutionResult()

	before := m.renderWorkflowEditor()
	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.renderWorkflowEditor() == before {
		t.Fatal("didn't scroll")
	}

	for range 200 {
		m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if !strings.Contains(m.renderWorkflowEditor(), `"z": "z"`) {
		t.Fatal("couldn't reach the end")
	}

	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.editorOverlay != editorOverlayNone || m.screen != screenWorkflowEditor {
		t.Fatalf("overlay %q, screen %v", m.editorOverlay, m.screen)
	}
}

func TestARunAtAWaitIsFollowedUntilItEnds(t *testing.T) {
	stub := &heddleStub{steps: []apiExecution{
		{Status: "running"},
		{Status: "waiting", Waiting: &apiWaiting{NodeID: "start", ForCall: true}},
		{Status: "waiting", Waiting: &apiWaiting{NodeID: "start", ForCall: true}},
		{Status: "succeeded", Result: map[string]any{"ok": true}},
	}}
	m := runModel(t, stub)

	m = playRun(t, m, m.runCurrentWorkflow())

	if m.editorRunInFlight || !strings.HasPrefix(m.editorStatusMessage, "Execution finished") {
		t.Fatalf("in flight %v, status %q", m.editorRunInFlight, m.editorStatusMessage)
	}
}

func TestAWaitingRunSaysWhereItStoppedAndWhatResumesIt(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorRunInFlight = true
	m.editorExecution = &apiExecution{ID: "x", Status: "running"}

	updated, cmd := m.handleExecutionPolled(executionPolledMsg{
		workflowID: 7,
		execution: apiExecution{ID: "x", Status: "waiting", Waiting: &apiWaiting{
			NodeID:  "start",
			Until:   "2030-01-02T10:30:00Z",
			ForCall: true,
		}},
	})
	m = updated.(model)

	if !m.editorRunInFlight || cmd == nil {
		t.Fatal("stopped following a run that will go on")
	}
	if m.editorStatusMessage != "Execution waiting at Start, press o to inspect" {
		t.Fatalf("status: %q", m.editorStatusMessage)
	}

	m, _ = m.updateWorkflowEditorKey(keyPress("o"))
	view := m.renderWorkflowEditor()
	for _, text := range []string{"WAITING", "Stopped at Start", "Until a call to its resume URL, or", "No output yet"} {
		if !strings.Contains(view, text) {
			t.Fatalf("no %q in:\n%s", text, view)
		}
	}
}

func TestWaitingForSaysWhatResumesARun(t *testing.T) {
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	cases := map[string]*apiWaiting{
		"Until 10:30:00":                 {Until: "2026-11-08T10:30:00.123456Z"},
		"Until Nov 9 10:30":              {Until: "2026-11-09T10:30:00Z"},
		"Until a call to its resume URL": {ForCall: true},
		"Until it is resumed":            nil,
	}
	for want, waiting := range cases {
		if got := waitingFor(waiting, now); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestThereIsNoResultToOpenUntilARunEnds(t *testing.T) {
	m := runModel(t, &heddleStub{})
	m.editorExecution = &apiExecution{ID: "x", Status: "running"}

	m, _ = m.updateWorkflowEditorKey(keyPress("o"))

	if m.editorOverlay == editorOverlayResult {
		t.Fatal("opened the result of a run still going")
	}
}

func TestEveryNodeIconHasATerminalMark(t *testing.T) {
	icons := map[string]string{
		"hero-bolt-solid":               "↗",
		"hero-play-solid":               "▶",
		"hero-share-solid":              "↔",
		"hero-flag-solid":               "✓",
		"hero-variable-solid":           "x",
		"hero-code-bracket-solid":       "ƒ",
		"hero-globe-alt-solid":          "◉",
		"hero-document-text-solid":      "≡",
		"hero-pause-circle-solid":       "‖",
		"hero-arrows-pointing-in-solid": "⋈",
		"hero-arrows-right-left-solid":  "↔",
	}
	for icon, mark := range icons {
		if got := mapIconToTerminal(icon); got != mark {
			t.Errorf("%s: got %s, want %s", icon, got, mark)
		}
	}
}
