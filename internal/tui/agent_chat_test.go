package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"treadle/internal/agent"
)

// fakeHeddle answers the agent and workflow APIs, and records what it was asked. Tests
// play the events of the stream themselves, with stream.
type fakeHeddle struct {
	mu        sync.Mutex
	failure   string
	status    int
	flow      apiFlow
	messages  []string
	decisions []string
	stops     int
	url       string
}

func (s *fakeHeddle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}

	var body map[string]string
	switch {
	case strings.HasSuffix(r.URL.Path, "/agent/events"):
		w.WriteHeader(http.StatusNotFound)
	case strings.HasSuffix(r.URL.Path, "/agent/messages"):
		if s.failure != "" {
			w.WriteHeader(http.StatusConflict)
			fmt.Fprintf(w, `{"error":{"message":%q}}`, s.failure)
			return
		}
		json.NewDecoder(r.Body).Decode(&body)
		s.messages = append(s.messages, body["content"])
		w.WriteHeader(http.StatusAccepted)
	case strings.HasSuffix(r.URL.Path, "/agent/decision"):
		json.NewDecoder(r.Body).Decode(&body)
		s.decisions = append(s.decisions, body["decision"])
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(r.URL.Path, "/agent/stop"):
		s.stops++
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(r.URL.Path, "/api/v1/workflows/"):
		json.NewEncoder(w).Encode(workflowResponse{Data: s.flow})
	default:
		json.NewEncoder(w).Encode(pageResponse[apiFlow]{})
	}
}

func (s *fakeHeddle) messageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

func heddle(t *testing.T) *fakeHeddle {
	t.Helper()

	fake := &fakeHeddle{flow: apiFlow{ID: 7, Name: "Flow"}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fake.url = server.URL
	return fake
}

// stream plays events as if they came from the server.
func stream(t *testing.T, m model, events ...agent.Event) model {
	t.Helper()

	for _, event := range events {
		next, cmd := m.Update(agentStreamMsg{flowID: m.agentFollowing, update: agent.Update{Event: &event}})
		m = settle(t, next.(model), cmd)
	}
	return m
}

func reply(text string) []agent.Event {
	return []agent.Event{
		{Type: "status", Status: agent.StatusRunning},
		{Type: "text_delta", Text: text},
		{Type: "status", Status: agent.StatusIdle},
	}
}

func asking() []agent.Event {
	return []agent.Event{
		{Type: "status", Status: agent.StatusRunning},
		{Type: "permission", Permission: &agent.Permission{FlowName: "Flow"}},
	}
}

func chatModel(t *testing.T, fake *fakeHeddle) model {
	t.Helper()

	m := sizedModel(140, 40)
	m.activeHost = fake.url
	m.activeAPIKey = "heddle-token"
	m.connected = true
	m.beginWorkflowEditorFromAPI(apiFlow{ID: 7, Name: "Flow"})

	next, _ := m.openAIChat()
	return next
}

// settle runs a command and everything that follows from it.
func settle(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		return m
	}

	// The stream only ends with the connection, so nothing here can wait for it.
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		return m
	}

	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, item := range batch {
			m = settle(t, m, item)
		}
		return m
	}

	// Only what the chat started: the cursor blink never ends.
	switch msg.(type) {
	case agentSentMsg, agentFlowLoadedMsg, workflowLoadedMsg:
		next, nextCmd := m.Update(msg)
		return settle(t, next.(model), nextCmd)
	}
	return m
}

func send(t *testing.T, m model, prompt string) (model, tea.Cmd) {
	t.Helper()
	m.aiPromptInput.SetValue(prompt)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next.(model), cmd
}

func say(t *testing.T, m model, prompt string) model {
	t.Helper()
	next, cmd := send(t, m, prompt)
	return settle(t, next, cmd)
}

func chatText(m model) string {
	return ansi.Strip(strings.Join(m.aiChatLines(200), "\n"))
}

func onScreen(m model, text string) bool {
	return strings.Contains(strings.Join(screenLines(m), "\n"), text)
}

func TestChatStartsEmpty(t *testing.T) {
	if m := chatModel(t, heddle(t)); len(m.agentTranscript.Items) != 0 {
		t.Fatalf("items = %+v", m.agentTranscript.Items)
	}
}

func TestChatSendsTheMessageToTheServer(t *testing.T) {
	fake := heddle(t)

	m := say(t, chatModel(t, fake), "add a logger")

	if fake.messages[0] != "add a logger" {
		t.Fatalf("messages = %+v", fake.messages)
	}
	if !m.agentBusy {
		t.Fatal("it should wait for the server to say it finished")
	}
}

func TestChatShowsTheReplyAsItComes(t *testing.T) {
	m := say(t, chatModel(t, heddle(t)), "hi")

	m = stream(t, m, agent.Event{Type: "user_message", Text: "hi"}, agent.Event{Type: "text_delta", Text: "On "})
	m = stream(t, m, agent.Event{Type: "text_delta", Text: "it."}, agent.Event{Type: "status", Status: agent.StatusIdle})

	if !strings.Contains(chatText(m), "You: hi") || !strings.Contains(chatText(m), "Agent: On it.") {
		t.Fatalf("chat = %q", chatText(m))
	}
	if m.agentBusy {
		t.Fatal("should be done")
	}
}

func TestChatShowsTheToolsOfTheAgent(t *testing.T) {
	m := say(t, chatModel(t, heddle(t)), "ta dando erro")

	m = stream(t, m, agent.Event{Type: "tool_started", ID: "1", Name: "get_flow"})
	if !strings.Contains(chatText(m), "◌ Reading the flow") {
		t.Fatalf("chat = %q", chatText(m))
	}

	m = stream(t, m, agent.Event{Type: "tool_finished", ID: "1", OK: true, Output: "{}"})
	if !strings.Contains(chatText(m), "✓ Reading the flow") {
		t.Fatalf("chat = %q", chatText(m))
	}
}

func TestChatShowsHowATestEnded(t *testing.T) {
	m := say(t, chatModel(t, heddle(t)), "test it")

	m = stream(t, m,
		agent.Event{Type: "tool_started", ID: "1", Name: "test_flow"},
		agent.Event{Type: "tool_finished", ID: "1", OK: true, Output: `{"status":"failed","error":{"message":"key :x not found"}}`},
	)

	if !strings.Contains(chatText(m), "Testing the flow · key :x not found") {
		t.Fatalf("chat = %q", chatText(m))
	}
}

func TestChatPicksUpWorkInProgress(t *testing.T) {
	m := chatModel(t, heddle(t))

	snapshot := agent.Transcript{
		Status: agent.StatusRunning,
		Items:  []agent.Item{{ID: "item-1", Role: agent.RoleUser, Text: "from the browser"}},
	}
	next, _ := m.Update(agentStreamMsg{flowID: m.agentFollowing, update: agent.Update{Snapshot: &snapshot}})
	m = next.(model)

	if !m.agentBusy || !strings.Contains(chatText(m), "from the browser") {
		t.Fatalf("busy = %v, chat = %q", m.agentBusy, chatText(m))
	}
}

func TestChatIsBusyWhileTheServerThinks(t *testing.T) {
	m, cmd := send(t, chatModel(t, heddle(t)), "hi")

	if !m.agentBusy || cmd == nil {
		t.Fatal("should be waiting")
	}
	if !strings.Contains(strings.Join(m.renderAIChatBar(), "\n"), "working") {
		t.Fatal("nothing says it's working")
	}
}

func TestChatShowsWhatTheServerSaysIsWrong(t *testing.T) {
	fake := heddle(t)
	fake.failure = "The agent is still working."

	m := say(t, chatModel(t, fake), "hi")

	if m.agentBusy {
		t.Fatal("should stop waiting")
	}
	if !strings.Contains(chatText(m), "The agent is still working.") {
		t.Fatalf("chat = %q", chatText(m))
	}
}

func TestChatReturnsToConnectWhenTheTokenIsRefused(t *testing.T) {
	fake := heddle(t)
	fake.status = http.StatusUnauthorized

	m := say(t, chatModel(t, fake), "hi")

	if m.screen != screenConnect {
		t.Fatalf("screen = %v", m.screen)
	}
}

func TestChatIgnoresMessagesWhileBusy(t *testing.T) {
	fake := heddle(t)
	m := say(t, chatModel(t, fake), "first")

	next, cmd := send(t, m, "second")

	if cmd != nil || fake.messageCount() != 1 {
		t.Fatal("sent a second message")
	}
	if next.aiPromptInput.Value() != "second" {
		t.Fatal("lost what was typed")
	}
}

func TestChatDropsEventsFromAnotherFlow(t *testing.T) {
	m := chatModel(t, heddle(t))
	event := agent.Event{Type: "text_delta", Text: "late"}

	next, _ := m.Update(agentStreamMsg{flowID: 99, update: agent.Update{Event: &event}})

	if strings.Contains(chatText(next.(model)), "late") {
		t.Fatal("showed a reply about another flow")
	}
}

func TestStopAsksTheServer(t *testing.T) {
	fake := heddle(t)
	m := say(t, chatModel(t, fake), "go")

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	settle(t, next.(model), cmd)

	if fake.stops != 1 {
		t.Fatalf("stops = %d", fake.stops)
	}
}

func TestChatSavesUnsavedChangesFirst(t *testing.T) {
	fake := heddle(t)
	m := chatModel(t, fake)
	m.markEditorDirty()

	m, cmd := send(t, m, "hi")

	if cmd == nil || !m.editorSaving {
		t.Fatal("should be saving")
	}
	if fake.messageCount() != 0 {
		t.Fatal("the agent would read a stale flow")
	}

	next, ask := m.Update(workflowSavedMsg{flow: apiFlow{ID: 7, Name: "Flow"}})
	settle(t, next.(model), ask)

	if fake.messageCount() != 1 {
		t.Fatalf("chats = %d", fake.messageCount())
	}
}

func TestChatStopsIfTheSaveFails(t *testing.T) {
	fake := heddle(t)
	m := chatModel(t, fake)
	m.markEditorDirty()
	m, _ = send(t, m, "hi")

	next, _ := m.Update(workflowSavedMsg{err: errors.New("boom")})
	m = next.(model)

	if fake.messageCount() != 0 || m.agentBusy {
		t.Fatal("nothing should go out")
	}
	if !strings.Contains(chatText(m), "couldn't be saved") {
		t.Fatalf("chat = %q", chatText(m))
	}
	if m.agentPrompt != "" {
		t.Fatalf("prompt = %q", m.agentPrompt)
	}
}

func TestTheAgentAsksBeforeRunning(t *testing.T) {
	m := stream(t, chatModel(t, heddle(t)), asking()...)

	if !strings.Contains(chatText(m), `Run "Flow"?`) {
		t.Fatalf("chat = %q", chatText(m))
	}
}

func TestAnswersGoToTheServer(t *testing.T) {
	for key, decision := range map[string]string{"y": "once", "a": "always", "n": "deny"} {
		fake := heddle(t)
		m := stream(t, chatModel(t, fake), asking()...)

		next, cmd := m.Update(keyPress(key))
		m = settle(t, next.(model), cmd)

		if len(fake.decisions) != 1 || fake.decisions[0] != decision {
			t.Fatalf("%s: decisions = %+v", key, fake.decisions)
		}
		if m.agentTranscript.Permission != nil {
			t.Fatalf("%s: still asking", key)
		}
	}
}

func TestAnsweringDoesNotBreakTheScreen(t *testing.T) {
	for _, key := range []string{"y", "a", "n"} {
		m := stream(t, chatModel(t, heddle(t)), asking()...)

		next, cmd := m.Update(keyPress(key))
		m = settle(t, next.(model), cmd)

		_ = m.View()
	}
}

func TestAFrameSurvivesAConversationThatChangedSinceTheLayout(t *testing.T) {
	m := stream(t, chatModel(t, heddle(t)), asking()...)

	m.agentTranscript.Permission = nil

	_ = m.View()
}

func TestChatInputCantAnswerWhileAsking(t *testing.T) {
	fake := heddle(t)
	m := stream(t, chatModel(t, fake), asking()...)
	m.agentBusy = false

	_, cmd := send(t, m, "something else")

	if cmd != nil || fake.messageCount() != 0 {
		t.Fatal("a message slipped past the question")
	}
}

func TestTheQuestionOpensTheChat(t *testing.T) {
	m := chatModel(t, heddle(t)).closeAIChat()

	m = stream(t, m, asking()...)

	if !onScreen(m, `Run "Flow"?`) {
		t.Fatal("nobody would see the question")
	}
}

func agentFlow() apiFlow {
	return apiFlow{ID: 7, Name: "Renamed", Nodes: []apiNode{
		{ID: "a", Type: "Start", Label: "Start", Position: apiPosition{X: 100, Y: 100}, Controls: map[string]any{}},
	}}
}

func TestCanvasShowsWhatTheAgentSaved(t *testing.T) {
	fake := heddle(t)
	fake.flow = agentFlow()

	m := say(t, chatModel(t, fake), "rename it")
	m = stream(t, m,
		agent.Event{Type: "tool_started", ID: "1", Name: "update_flow"},
		agent.Event{Type: "tool_finished", ID: "1", OK: true, Output: "{}"},
	)

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
	m := chatModel(t, heddle(t))
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
	m := chatModel(t, heddle(t))
	before := len(m.editorNodes)

	flow := agentFlow()
	flow.ID = 99
	next, _ := m.Update(agentFlowLoadedMsg{flow: flow})

	if len(next.(model).editorNodes) != before {
		t.Fatal("replaced by another flow")
	}
}

func TestLongRepliesCanBeReadToTheEnd(t *testing.T) {
	m := say(t, chatModel(t, heddle(t)), "hi")
	m = stream(t, m, reply(strings.Repeat("line\n", 200)+"the end")...)

	if !strings.Contains(strings.Join(m.renderAIChatBar(), "\n"), "the end") {
		t.Fatal("the end of the reply is out of reach")
	}
}

func TestChatBreaksLinesBetweenWords(t *testing.T) {
	m := sizedModel(140, 40)
	m.agentTranscript.Items = []agent.Item{{Role: agent.RoleAssistant, Text: "The flow is saved and ready to run whenever you confirm it"}}

	joined := ""
	for _, line := range m.aiChatLines(24) {
		text := ansi.Strip(line)
		if width := lipgloss.Width(text); width > 24 {
			t.Fatalf("%q is %d wide", text, width)
		}
		joined += strings.TrimSpace(text) + " "
	}
	if !strings.Contains(joined, "whenever you confirm it") {
		t.Fatalf("cut a word: %q", joined)
	}
}

func TestChatCutsAWordOnlyWhenItCantFit(t *testing.T) {
	m := sizedModel(140, 40)
	m.agentTranscript.Items = []agent.Item{{Role: agent.RoleAssistant, Text: strings.Repeat("x", 80)}}

	for _, line := range m.aiChatLines(24) {
		if width := lipgloss.Width(ansi.Strip(line)); width > 24 {
			t.Fatalf("%d wide", width)
		}
	}
}

func editorWithoutChat(width, height int) model {
	m := sizedModel(width, height)
	m.beginWorkflowEditorFromAPI(apiFlow{ID: 7, Name: "Flow"})
	return m
}

func TestTheCanvasKeepsTheWholeScreenUntilTheChatIsAsked(t *testing.T) {
	m := editorWithoutChat(140, 40)

	if m.aiChatOpen || m.aiChatRect.h != 0 || m.canvasRect.h != m.paletteRect.h {
		t.Fatalf("open = %v chat = %+v canvas = %+v", m.aiChatOpen, m.aiChatRect, m.canvasRect)
	}
	if m.canvasRect.x+m.canvasRect.w != m.width {
		t.Fatalf("canvas = %+v", m.canvasRect)
	}
	if !onScreen(m, "/ agent") {
		t.Fatal("nobody would find the chat")
	}
}

func TestSlashOpensAThinBarUnderTheCanvas(t *testing.T) {
	m := editorWithoutChat(140, 40)
	wide, tall := m.canvasRect.w, m.canvasRect.h

	next, _ := m.Update(keyPress("/"))
	m = next.(model)

	if !m.aiPromptInput.Focused() {
		t.Fatal("should be focused")
	}
	if !onScreen(m, "Ask the agent") {
		t.Fatal("the input isn't on screen")
	}
	if m.canvasRect.w != wide {
		t.Fatalf("canvas went from %d to %d wide", wide, m.canvasRect.w)
	}
	if m.canvasRect.h != tall-aiChatChromeHeight {
		t.Fatalf("canvas went from %d to %d tall", tall, m.canvasRect.h)
	}
}

func TestTheBarGrowsWithTheConversationUpToAThird(t *testing.T) {
	m := chatModel(t, heddle(t))
	body := m.paletteRect.h

	m = say(t, m, "hi")
	m = stream(t, m, reply(strings.Repeat("a long reply ", 200))...)

	if m.aiChatRect.h != aiChatChromeHeight+body/3 {
		t.Fatalf("chat = %d of %d", m.aiChatRect.h, body)
	}
	if m.canvasRect.h+m.aiChatRect.h != body {
		t.Fatalf("canvas = %d chat = %d body = %d", m.canvasRect.h, m.aiChatRect.h, body)
	}
	if !onScreen(m, "a long reply") {
		t.Fatal("the reply isn't on screen")
	}
}

func TestAClickOnTheBarDoesNotReachTheCanvas(t *testing.T) {
	m := chatModel(t, heddle(t))

	if inside(m.aiPromptInputRect.x, m.aiPromptInputRect.y, m.canvasRect) {
		t.Fatalf("input = %+v canvas = %+v", m.aiPromptInputRect, m.canvasRect)
	}
}

func TestATinyTerminalKeepsTheCanvas(t *testing.T) {
	next, _ := editorWithoutChat(72, 8).Update(keyPress("/"))
	m := next.(model)

	if m.aiChatOpen || m.aiPromptInput.Focused() {
		t.Fatal("no room for the bar")
	}
}

func TestEscClosesTheBarBeforeLeavingTheEditor(t *testing.T) {
	m := chatModel(t, heddle(t))
	m.aiPromptInput.Blur()

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)

	if m.aiChatOpen || m.screen != screenWorkflowEditor {
		t.Fatalf("open = %v screen = %v", m.aiChatOpen, m.screen)
	}
}

func TestTheFooterSaysWhatTheAgentDidWhileTheBarWasClosed(t *testing.T) {
	m, cmd := send(t, chatModel(t, heddle(t)), "hi")
	m = m.closeAIChat()

	if !onScreen(m, "/ agent working") {
		t.Fatal("nothing says it's working")
	}

	m = settle(t, m, cmd)
	m = stream(t, m, reply("done")...)
	if !onScreen(m, "/ agent replied") {
		t.Fatal("nothing says it replied")
	}

	next, _ := m.Update(keyPress("/"))
	m = next.(model)
	if !onScreen(m, "done") || onScreen(m, "replied") {
		t.Fatal("opening should show the reply and clear the notice")
	}
}

func TestTheFooterShowsTheKeysOfTheQuestion(t *testing.T) {
	m := stream(t, chatModel(t, heddle(t)), asking()...)

	if !onScreen(m, "y run") {
		t.Fatal("the keys aren't on screen")
	}
}

func TestAnotherFlowStartsWithTheBarClosed(t *testing.T) {
	m := stream(t, say(t, chatModel(t, heddle(t)), "hi"), reply("hello")...)

	m.beginWorkflowEditorFromAPI(apiFlow{ID: 8, Name: "Other"})

	if m.aiChatOpen || m.aiPromptInput.Focused() || m.aiChatRect.h != 0 {
		t.Fatalf("open = %v chat = %+v", m.aiChatOpen, m.aiChatRect)
	}
}

func listModel(t *testing.T, fake *fakeHeddle) model {
	t.Helper()

	m := appModel(140, 40, workflow{id: 7, name: "Flow"})
	m.activeHost = fake.url
	m.activeAPIKey = "heddle-token"
	return m
}

func askFromList(t *testing.T, m model, prompt string) model {
	t.Helper()

	next, _ := m.Update(keyPress("/"))
	m = next.(model)
	m.composerInput.SetValue(prompt)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return settle(t, next.(model), cmd)
}

func TestTheListSendsAMessageToTheSelectedWorkflow(t *testing.T) {
	fake := heddle(t)

	m := askFromList(t, listModel(t, fake), "add a logger")

	if m.screen != screenWorkflowEditor {
		t.Fatalf("screen = %v", m.screen)
	}
	if fake.messageCount() != 1 {
		t.Fatalf("messages = %+v", fake.messages)
	}
	m = stream(t, m, reply("On it.")...)
	if !onScreen(m, "On it.") {
		t.Fatal("the reply isn't on screen")
	}
}

func TestTheListStillRunsItsCommands(t *testing.T) {
	fake := heddle(t)

	m := askFromList(t, listModel(t, fake), "help")

	if fake.messageCount() != 0 || m.screen != screenApp {
		t.Fatal("help went to the agent")
	}
}

func TestAFailedOpenDropsTheMessage(t *testing.T) {
	m := listModel(t, heddle(t))
	m.agentQueuedPrompt = "hi"

	next, _ := m.Update(workflowLoadedMsg{err: errors.New("boom")})

	if next.(model).agentQueuedPrompt != "" {
		t.Fatal("it would be sent to the next workflow opened")
	}
}

func TestChatFitsTheScreen(t *testing.T) {
	for _, size := range [][2]int{{72, 12}, {72, 16}, {100, 30}, {140, 40}, {220, 60}} {
		next, _ := editorWithoutChat(size[0], size[1]).Update(keyPress("/"))
		m := next.(model)

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
	m := stream(t, say(t, chatModel(t, heddle(t)), "hi"), reply("hello")...)

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)

	if m.aiChatOpen || m.aiChatRect.w != 0 || m.screen != screenWorkflowEditor {
		t.Fatalf("open = %v screen = %v", m.aiChatOpen, m.screen)
	}

	next, _ = m.Update(keyPress("/"))
	if !onScreen(next.(model), "hello") {
		t.Fatal("the conversation was lost")
	}
}
