package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"treadle/internal/agent"
)

type agentCall struct {
	opts   agent.Options
	events chan agent.Event
}

// fakeAgent replaces the process, and hands each message to the test.
func fakeAgent(t *testing.T) chan agentCall {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TREADLE_CLAUDE_BIN", self)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")

	calls := make(chan agentCall, 4)
	original := startAgent
	startAgent = func(_ context.Context, opts agent.Options) (<-chan agent.Event, error) {
		events := make(chan agent.Event, 16)
		calls <- agentCall{opts: opts, events: events}
		return events, nil
	}
	t.Cleanup(func() { startAgent = original })
	return calls
}

func chatModel(t *testing.T) model {
	t.Helper()

	m := sizedModel(140, 40)
	m.activeHost = "localhost:4000"
	m.activeAPIKey = "heddle-token"
	m.beginWorkflowEditorFromAPI(apiFlow{ID: 7, Name: "Flow"})

	next, _ := m.openAIChat()
	return next
}

func send(t *testing.T, m model, prompt string) (model, tea.Cmd) {
	t.Helper()
	m.aiPromptInput.SetValue(prompt)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next.(model), cmd
}

// drain runs the agent's messages through the model until the run ends.
func drain(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()

	for cmd != nil {
		msg := cmd()
		event, ok := msg.(agentEventMsg)
		if !ok {
			return m
		}
		next, nextCmd := m.Update(event)
		m = next.(model)
		if event.closed {
			return m
		}
		cmd = nextCmd
	}
	return m
}

func chatText(m model) string {
	return strings.Join(m.aiChatMessages, "\n")
}

func TestChatStartsEmpty(t *testing.T) {
	fakeAgent(t)

	if m := chatModel(t); len(m.aiChatMessages) != 0 {
		t.Fatalf("messages = %q", m.aiChatMessages)
	}
}

func TestChatExplainsWhatIsMissing(t *testing.T) {
	t.Setenv("TREADLE_CLAUDE_BIN", "/no/such/claude")

	m, cmd := send(t, chatModel(t), "hi")

	if cmd != nil || m.agentBusy {
		t.Fatal("nothing should run")
	}
	if !strings.Contains(chatText(m), "TREADLE_CLAUDE_BIN") {
		t.Fatalf("chat = %q", chatText(m))
	}
}

func TestChatSendsTheMessageToTheServersMCP(t *testing.T) {
	calls := fakeAgent(t)

	m, _ := send(t, chatModel(t), "add a logger")

	call := <-calls
	if call.opts.Prompt != "add a logger" {
		t.Fatalf("prompt = %q", call.opts.Prompt)
	}
	if call.opts.MCPURL != "http://localhost:4000/mcp" || call.opts.Token != "heddle-token" {
		t.Fatalf("opts = %+v", call.opts)
	}
	if call.opts.AllowRun {
		t.Fatal("running must wait for a person")
	}
	if !strings.Contains(call.opts.System, "id 7") {
		t.Fatalf("system = %q", call.opts.System)
	}
	if !m.agentBusy {
		t.Fatal("should be busy")
	}
}

func TestChatStreamsTheReplyIntoOneMessage(t *testing.T) {
	calls := fakeAgent(t)
	m, cmd := send(t, chatModel(t), "hi")

	call := <-calls
	call.events <- agent.Event{Type: agent.EventText, Text: "On "}
	call.events <- agent.Event{Type: agent.EventText, Text: "it."}
	close(call.events)

	m = drain(t, m, cmd)

	if m.agentBusy {
		t.Fatal("should be done")
	}
	if last := m.aiChatMessages[len(m.aiChatMessages)-1]; last != "Agent: On it." {
		t.Fatalf("last = %q", last)
	}
}

func TestChatSeparatesTheAgentsTurns(t *testing.T) {
	calls := fakeAgent(t)
	m, cmd := send(t, chatModel(t), "hi")

	call := <-calls
	call.events <- agent.Event{Type: agent.EventTurn}
	call.events <- agent.Event{Type: agent.EventText, Text: "Checking."}
	call.events <- agent.Event{Type: agent.EventTurn}
	call.events <- agent.Event{Type: agent.EventText, Text: "Done"}
	call.events <- agent.Event{Type: agent.EventText, Text: " now."}
	close(call.events)
	m = drain(t, m, cmd)

	if last := m.aiChatMessages[len(m.aiChatMessages)-1]; last != "Agent: Checking.\n\nDone now." {
		t.Fatalf("last = %q", last)
	}
}

func TestChatBreaksLinesBetweenWords(t *testing.T) {
	m := sizedModel(140, 40)
	m.aiChatMessages = []string{"Agent: The flow is saved and ready to run whenever you confirm it"}

	for _, line := range m.aiChatLines(24) {
		text := ansi.Strip(line)
		if width := lipgloss.Width(text); width > 24 {
			t.Fatalf("%q is %d wide", text, width)
		}
		for _, cut := range []string{"sa\n", " conf", "wheneve"} {
			if strings.HasSuffix(text, strings.TrimSuffix(cut, "\n")) {
				t.Fatalf("cut a word: %q", text)
			}
		}
	}
}

func TestChatCutsAWordOnlyWhenItCantFit(t *testing.T) {
	m := sizedModel(140, 40)
	m.aiChatMessages = []string{"Agent: " + strings.Repeat("x", 80)}

	for _, line := range m.aiChatLines(24) {
		if width := lipgloss.Width(ansi.Strip(line)); width > 24 {
			t.Fatalf("%d wide", width)
		}
	}
}

func TestChatContinuesTheConversation(t *testing.T) {
	calls := fakeAgent(t)
	m, cmd := send(t, chatModel(t), "first")

	first := <-calls
	close(first.events)
	m = drain(t, m, cmd)

	m, _ = send(t, m, "second")
	second := <-calls

	if !second.opts.Resume || second.opts.SessionID != first.opts.SessionID {
		t.Fatalf("first = %+v second = %+v", first.opts, second.opts)
	}
}

func TestChatStartsOverAfterAFailedFirstMessage(t *testing.T) {
	calls := fakeAgent(t)
	m, cmd := send(t, chatModel(t), "first")

	first := <-calls
	first.events <- agent.Event{Type: agent.EventError, Text: "invalid api key"}
	close(first.events)
	m = drain(t, m, cmd)

	if !strings.Contains(chatText(m), "invalid api key") {
		t.Fatalf("chat = %q", chatText(m))
	}

	m, _ = send(t, m, "second")
	second := <-calls

	if second.opts.Resume || second.opts.SessionID == first.opts.SessionID {
		t.Fatalf("second = %+v", second.opts)
	}
}

func TestChatIgnoresMessagesWhileBusy(t *testing.T) {
	calls := fakeAgent(t)
	m, _ := send(t, chatModel(t), "first")
	<-calls

	m, cmd := send(t, m, "second")

	if cmd != nil || len(calls) != 0 {
		t.Fatal("a second run started")
	}
	if m.aiPromptInput.Value() != "second" {
		t.Fatal("the message should wait in the input")
	}
}

func TestChatDropsEventsFromAnotherFlow(t *testing.T) {
	calls := fakeAgent(t)
	m, cmd := send(t, chatModel(t), "hi")

	call := <-calls
	call.events <- agent.Event{Type: agent.EventText, Text: "late"}

	m.beginWorkflowEditorFromAPI(apiFlow{ID: 8, Name: "Other"})

	next, _ := m.Update(cmd())
	if got := next.(model).aiChatMessages; len(got) != 0 {
		t.Fatalf("messages = %q", got)
	}
}

func TestChatSavesUnsavedChangesFirst(t *testing.T) {
	calls := fakeAgent(t)
	m := chatModel(t)
	m.markEditorDirty()

	m, cmd := send(t, m, "hi")

	if cmd == nil || !m.editorSaving {
		t.Fatal("should be saving")
	}
	if len(calls) != 0 {
		t.Fatal("the agent would read a stale flow")
	}

	next, _ := m.Update(workflowSavedMsg{flow: apiFlow{ID: 7, Name: "Flow"}})
	m = next.(model)

	if call := <-calls; call.opts.Prompt != "hi" {
		t.Fatalf("prompt = %q", call.opts.Prompt)
	}
	if m.agentPendingPrompt != "" {
		t.Fatal("still pending")
	}
}

func TestChatStopsIfTheSaveFails(t *testing.T) {
	calls := fakeAgent(t)
	m := chatModel(t)
	m.markEditorDirty()
	m, _ = send(t, m, "hi")

	next, _ := m.Update(workflowSavedMsg{err: errors.New("boom")})
	m = next.(model)

	if len(calls) != 0 || m.agentBusy {
		t.Fatal("nothing should run")
	}
	if !strings.Contains(chatText(m), "couldn't be saved") {
		t.Fatalf("chat = %q", chatText(m))
	}
}

func denyRun(t *testing.T, calls chan agentCall) model {
	t.Helper()

	m, cmd := send(t, chatModel(t), "run it")
	call := <-calls
	call.events <- agent.Event{Type: agent.EventRunDenied, FlowID: "7"}
	close(call.events)
	return drain(t, m, cmd)
}

func TestRunWaitsForConfirmation(t *testing.T) {
	calls := fakeAgent(t)

	m := denyRun(t, calls)

	if !m.agentAwaitingRun {
		t.Fatal("should be asking")
	}
	if !strings.Contains(chatText(m), "Run flow 7?") {
		t.Fatalf("chat = %q", chatText(m))
	}
	if len(calls) != 0 {
		t.Fatal("ran without confirmation")
	}
}

func TestRunGoesAheadOnYes(t *testing.T) {
	calls := fakeAgent(t)
	m := denyRun(t, calls)

	m.Update(keyPress("y"))

	call := <-calls
	if !call.opts.AllowRun || !call.opts.Resume {
		t.Fatalf("opts = %+v", call.opts)
	}
	if !strings.Contains(call.opts.Prompt, "flow 7") {
		t.Fatalf("prompt = %q", call.opts.Prompt)
	}
}

func TestRunStopsOnNo(t *testing.T) {
	calls := fakeAgent(t)
	m := denyRun(t, calls)

	next, _ := m.Update(keyPress("n"))

	if next.(model).agentAwaitingRun || len(calls) != 0 {
		t.Fatal("should have stopped")
	}
}

func TestConfirmationIsOnlyForOneMessage(t *testing.T) {
	calls := fakeAgent(t)
	m := denyRun(t, calls)

	next, cmd := m.Update(keyPress("y"))
	m = next.(model)
	approved := <-calls
	close(approved.events)
	m = drain(t, m, cmd)

	m, _ = send(t, m, "and again")

	if call := <-calls; call.opts.AllowRun {
		t.Fatal("the confirmation carried over")
	}
}

func TestChatInputCantAnswerWhileAsking(t *testing.T) {
	calls := fakeAgent(t)
	m := denyRun(t, calls)

	m, cmd := send(t, m, "something else")

	if cmd != nil || len(calls) != 0 {
		t.Fatal("a message slipped past the question")
	}
}

func flowServer(t *testing.T, flow apiFlow) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/workflows/") {
			json.NewEncoder(w).Encode(workflowResponse{Data: flow})
			return
		}
		json.NewEncoder(w).Encode(flowsResponse{})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func agentFlow() apiFlow {
	return apiFlow{ID: 7, Name: "Renamed", Nodes: []apiNode{
		{ID: "a", Type: "Start", Label: "Start", Position: apiPosition{X: 100, Y: 100}, Controls: map[string]any{}},
	}}
}

func TestCanvasShowsWhatTheAgentSaved(t *testing.T) {
	calls := fakeAgent(t)
	m := chatModel(t)
	m.activeHost = flowServer(t, agentFlow())

	m, cmd := send(t, m, "rename it")
	close((<-calls).events)

	msg := cmd()
	next, reload := m.Update(msg)
	m = next.(model)

	loaded := reload()
	if _, ok := loaded.(agentFlowLoadedMsg); !ok {
		t.Fatalf("msg = %T", loaded)
	}
	next, _ = m.Update(loaded)
	m = next.(model)

	if len(m.editorNodes) != 1 || m.editorNodes[0].id != "a" || m.editorWorkflowName != "Renamed" {
		t.Fatalf("nodes = %+v name = %q", m.editorNodes, m.editorWorkflowName)
	}
	if m.editorDirty {
		t.Fatal("what came from the server isn't unsaved")
	}
	if m.editorStatusMessage != "Updated by the agent" {
		t.Fatalf("status = %q", m.editorStatusMessage)
	}
}

func TestCanvasKeepsEditsMadeWhileTheAgentWorked(t *testing.T) {
	fakeAgent(t)
	m := chatModel(t)
	before := len(m.editorNodes)
	m.markEditorDirty()

	next, _ := m.Update(agentFlowLoadedMsg{flow: agentFlow()})
	m = next.(model)

	if len(m.editorNodes) != before {
		t.Fatal("local edits were lost")
	}
	if !strings.Contains(m.editorStatusMessage, "overwrites") {
		t.Fatalf("status = %q", m.editorStatusMessage)
	}
}

func TestCanvasIgnoresAnotherFlow(t *testing.T) {
	fakeAgent(t)
	m := chatModel(t)
	before := len(m.editorNodes)

	flow := agentFlow()
	flow.ID = 99
	next, _ := m.Update(agentFlowLoadedMsg{flow: flow})

	if len(next.(model).editorNodes) != before {
		t.Fatal("replaced by another flow")
	}
}

func TestLongRepliesCanBeReadToTheEnd(t *testing.T) {
	calls := fakeAgent(t)
	m, cmd := send(t, chatModel(t), "hi")

	call := <-calls
	call.events <- agent.Event{Type: agent.EventText, Text: strings.Repeat("line\n", 200) + "the end"}
	close(call.events)
	m = drain(t, m, cmd)

	if !strings.Contains(m.renderAIChatPanel(), "the end") {
		t.Fatal("the end of the reply is out of reach")
	}
}

func editorWithoutChat(t *testing.T, width, height int) model {
	t.Helper()
	fakeAgent(t)

	m := sizedModel(width, height)
	m.beginWorkflowEditorFromAPI(apiFlow{ID: 7, Name: "Flow"})
	return m
}

func TestChatStaysOutOfTheWayUntilOpened(t *testing.T) {
	m := editorWithoutChat(t, 140, 40)

	if strings.Contains(strings.Join(screenLines(m), "\n"), "Claude Agent") {
		t.Fatal("the panel takes room nobody asked for")
	}
}

func TestSlashOpensTheChatOnScreen(t *testing.T) {
	m := editorWithoutChat(t, 140, 40)
	canvasBefore := m.canvasRect.w

	next, _ := m.Update(keyPress("/"))
	m = next.(model)

	if !m.aiPromptInput.Focused() {
		t.Fatal("should be focused")
	}
	if !strings.Contains(strings.Join(screenLines(m), "\n"), "Claude Agent") {
		t.Fatal("the panel isn't on screen")
	}
	if m.canvasRect.w >= canvasBefore {
		t.Fatal("the canvas should make room")
	}
}

func TestChatFitsTheScreen(t *testing.T) {
	for _, size := range [][2]int{{72, 16}, {100, 30}, {140, 40}, {220, 60}} {
		m := editorWithoutChat(t, size[0], size[1])
		next, _ := m.Update(keyPress("/"))
		m = next.(model)

		lines := screenLines(m)
		if len(lines) != size[1] {
			t.Fatalf("%v: %d lines", size, len(lines))
		}
		for i, line := range lines {
			if width := lipgloss.Width(line); width > size[0] {
				t.Fatalf("%v: line %d is %d wide: %q", size, i, width, line)
			}
		}
	}
}

func TestEscClosesTheChatAndKeepsTheConversation(t *testing.T) {
	calls := fakeAgent(t)
	m, cmd := send(t, chatModel(t), "hi")
	call := <-calls
	call.events <- agent.Event{Type: agent.EventText, Text: "hello"}
	close(call.events)
	m = drain(t, m, cmd)

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)

	if m.aiChatOpen || m.aiChatRect.w != 0 || m.screen != screenWorkflowEditor {
		t.Fatalf("open = %v screen = %v", m.aiChatOpen, m.screen)
	}

	next, _ = m.Update(keyPress("/"))
	if !strings.Contains(strings.Join(screenLines(next.(model)), "\n"), "hello") {
		t.Fatal("the conversation was lost")
	}
}

func TestTheQuestionOpensTheChat(t *testing.T) {
	calls := fakeAgent(t)
	m, cmd := send(t, chatModel(t), "run it")
	m = m.closeAIChat()

	call := <-calls
	call.events <- agent.Event{Type: agent.EventRunDenied, FlowID: "7"}
	close(call.events)
	m = drain(t, m, cmd)

	if !strings.Contains(strings.Join(screenLines(m), "\n"), "Run flow 7?") {
		t.Fatal("nobody would see the question")
	}
}
