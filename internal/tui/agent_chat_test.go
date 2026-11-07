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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// fakeHeddle answers MCP and the workflow API, and records what it was asked.
type fakeHeddle struct {
	mu      sync.Mutex
	reply   string
	failure string
	status  int
	flow    apiFlow
	chats   []map[string]any
	runs    []map[string]any
	url     string
}

func (s *fakeHeddle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/workflows/") {
		json.NewEncoder(w).Encode(workflowResponse{Data: s.flow})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		json.NewEncoder(w).Encode(pageResponse[apiFlow]{})
		return
	}

	var body struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	result := func(text string, isError bool) {
		encoded, _ := json.Marshal(text)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"isError":%t,"content":[{"type":"text","text":%s}]}}`,
			body.ID, isError, encoded)
	}

	switch body.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "session")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{}}`, body.ID)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		switch body.Params.Name {
		case "chat":
			s.chats = append(s.chats, body.Params.Arguments)
			if s.failure != "" {
				result(s.failure, true)
				return
			}
			result(s.reply, false)
		case "run_flow":
			s.runs = append(s.runs, body.Params.Arguments)
			result(`{"id":"clear-grid-channel"}`, false)
		}
	}
}

func (s *fakeHeddle) chatCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.chats)
}

func (s *fakeHeddle) runCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs)
}

func reply(text string) string {
	encoded, _ := json.Marshal(agentReply{Reply: text})
	return string(encoded)
}

func runRequest(text string) string {
	encoded, _ := json.Marshal(agentReply{
		Reply:      text,
		RunRequest: &agentRunRequest{FlowID: 7, Name: "Flow", Input: map[string]any{"a": "b"}},
	})
	return string(encoded)
}

func heddle(t *testing.T, answer string) *fakeHeddle {
	t.Helper()

	fake := &fakeHeddle{reply: answer, flow: apiFlow{ID: 7, Name: "Flow"}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	fake.url = server.URL
	return fake
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

	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, item := range batch {
			m = settle(t, m, item)
		}
		return m
	}

	// Only what the chat started: the cursor blink never ends.
	switch msg.(type) {
	case agentReplyMsg, agentRunStartedMsg, agentFlowLoadedMsg, workflowLoadedMsg:
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
	return strings.Join(m.aiChatMessages, "\n")
}

func onScreen(m model, text string) bool {
	return strings.Contains(strings.Join(screenLines(m), "\n"), text)
}

func TestChatStartsEmpty(t *testing.T) {
	if m := chatModel(t, heddle(t, "")); len(m.aiChatMessages) != 0 {
		t.Fatalf("messages = %q", m.aiChatMessages)
	}
}

func TestChatTalksToTheChatTool(t *testing.T) {
	fake := heddle(t, reply("On it."))

	m := say(t, chatModel(t, fake), "add a logger")

	if m.agentBusy {
		t.Fatal("should be done")
	}
	if last := m.aiChatMessages[len(m.aiChatMessages)-1]; last != "Agent: On it." {
		t.Fatalf("last = %q", last)
	}
	if fake.chats[0]["flow_id"] != "7" {
		t.Fatalf("chat = %+v", fake.chats[0])
	}
}

func TestChatIsBusyWhileTheServerThinks(t *testing.T) {
	m, cmd := send(t, chatModel(t, heddle(t, reply("ok"))), "hi")

	if !m.agentBusy || cmd == nil {
		t.Fatal("should be waiting")
	}
	if !strings.Contains(strings.Join(m.renderAIChatBar(), "\n"), "working") {
		t.Fatal("nothing says it's working")
	}
}

func TestChatSendsTheWholeConversation(t *testing.T) {
	fake := heddle(t, reply("Hello."))

	m := say(t, chatModel(t, fake), "first")
	say(t, m, "second")

	messages := fake.chats[1]["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %+v", messages)
	}
	last := messages[2].(map[string]any)
	if last["role"] != "user" || last["content"] != "second" {
		t.Fatalf("last = %+v", last)
	}
	if messages[1].(map[string]any)["role"] != "assistant" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestChatShowsWhatTheServerSaysIsWrong(t *testing.T) {
	fake := heddle(t, "")
	fake.failure = "No LLM provider is configured on this server."

	m := say(t, chatModel(t, fake), "hi")

	if !strings.Contains(chatText(m), "No LLM provider") {
		t.Fatalf("chat = %q", chatText(m))
	}
	if m.agentBusy {
		t.Fatal("should be done")
	}
}

func TestChatForgetsAMessageThatGotNoAnswer(t *testing.T) {
	fake := heddle(t, "")
	fake.failure = "rate limited"

	m := say(t, chatModel(t, fake), "hi")

	if len(m.agentTurns) != 0 {
		t.Fatalf("turns = %+v", m.agentTurns)
	}
}

func TestChatReturnsToConnectWhenTheTokenIsRefused(t *testing.T) {
	fake := heddle(t, "")
	m := chatModel(t, fake)
	fake.status = http.StatusUnauthorized

	m = say(t, m, "hi")

	if m.screen != screenConnect {
		t.Fatalf("screen = %v", m.screen)
	}
}

func TestChatIgnoresMessagesWhileBusy(t *testing.T) {
	fake := heddle(t, reply("ok"))
	m, _ := send(t, chatModel(t, fake), "first")

	m, cmd := send(t, m, "second")

	if cmd != nil {
		t.Fatal("a second message went out")
	}
	if m.aiPromptInput.Value() != "second" {
		t.Fatal("the message should wait in the input")
	}
}

func TestChatDropsARepliesFromAnotherFlow(t *testing.T) {
	fake := heddle(t, reply("late"))
	m, cmd := send(t, chatModel(t, fake), "hi")

	m.beginWorkflowEditorFromAPI(apiFlow{ID: 8, Name: "Other"})
	next, _ := m.Update(agentReplyMsg{run: m.agentRun - 1, reply: agentReply{Reply: "late"}})
	_ = cmd

	if got := next.(model).aiChatMessages; len(got) != 0 {
		t.Fatalf("messages = %q", got)
	}
}

func TestChatSavesUnsavedChangesFirst(t *testing.T) {
	fake := heddle(t, reply("ok"))
	m := chatModel(t, fake)
	m.markEditorDirty()

	m, cmd := send(t, m, "hi")

	if cmd == nil || !m.editorSaving {
		t.Fatal("should be saving")
	}
	if fake.chatCount() != 0 {
		t.Fatal("the agent would read a stale flow")
	}

	next, ask := m.Update(workflowSavedMsg{flow: apiFlow{ID: 7, Name: "Flow"}})
	settle(t, next.(model), ask)

	if fake.chatCount() != 1 {
		t.Fatalf("chats = %d", fake.chatCount())
	}
}

func TestChatStopsIfTheSaveFails(t *testing.T) {
	fake := heddle(t, reply("ok"))
	m := chatModel(t, fake)
	m.markEditorDirty()
	m, _ = send(t, m, "hi")

	next, _ := m.Update(workflowSavedMsg{err: errors.New("boom")})
	m = next.(model)

	if fake.chatCount() != 0 || m.agentBusy {
		t.Fatal("nothing should go out")
	}
	if !strings.Contains(chatText(m), "couldn't be saved") {
		t.Fatalf("chat = %q", chatText(m))
	}
	if len(m.agentTurns) != 0 {
		t.Fatalf("turns = %+v", m.agentTurns)
	}
}

func TestRunWaitsForConfirmation(t *testing.T) {
	fake := heddle(t, runRequest("Ready."))

	m := say(t, chatModel(t, fake), "run it")

	if m.agentRunRequest == nil {
		t.Fatal("should be asking")
	}
	if !strings.Contains(chatText(m), `Run "Flow"?`) {
		t.Fatalf("chat = %q", chatText(m))
	}
	if fake.runCount() != 0 {
		t.Fatal("ran without confirmation")
	}
}

func TestRunGoesThroughMCPOnYes(t *testing.T) {
	fake := heddle(t, runRequest("Ready."))
	m := say(t, chatModel(t, fake), "run it")

	next, cmd := m.Update(keyPress("y"))
	m = settle(t, next.(model), cmd)

	if fake.runCount() != 1 {
		t.Fatalf("runs = %d", fake.runCount())
	}
	run := fake.runs[0]
	if run["flow_id"] != "7" || run["input"].(map[string]any)["a"] != "b" {
		t.Fatalf("run = %+v", run)
	}
	if !strings.Contains(chatText(m), "clear-grid-channel") {
		t.Fatalf("chat = %q", chatText(m))
	}
	if m.agentBusy || m.agentRunRequest != nil {
		t.Fatal("should be over")
	}
}

func TestRunStopsOnNo(t *testing.T) {
	fake := heddle(t, runRequest("Ready."))
	m := say(t, chatModel(t, fake), "run it")

	next, cmd := m.Update(keyPress("n"))
	m = settle(t, next.(model), cmd)

	if fake.runCount() != 0 || m.agentRunRequest != nil {
		t.Fatal("should have stopped")
	}
	if last := m.agentTurns[len(m.agentTurns)-1]; last.Content != "Don't run it." {
		t.Fatalf("the agent wouldn't know: %+v", last)
	}
}

func TestChatInputCantAnswerWhileAsking(t *testing.T) {
	fake := heddle(t, runRequest("Ready."))
	m := say(t, chatModel(t, fake), "run it")

	_, cmd := send(t, m, "something else")

	if cmd != nil || fake.chatCount() != 1 {
		t.Fatal("a message slipped past the question")
	}
}

func TestTheQuestionOpensTheChat(t *testing.T) {
	fake := heddle(t, runRequest("Ready."))
	m, cmd := send(t, chatModel(t, fake), "run it")
	m = m.closeAIChat()

	m = settle(t, m, cmd)

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
	fake := heddle(t, reply("Renamed."))
	fake.flow = agentFlow()

	m := say(t, chatModel(t, fake), "rename it")

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
	m := chatModel(t, heddle(t, ""))
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
	m := chatModel(t, heddle(t, ""))
	before := len(m.editorNodes)

	flow := agentFlow()
	flow.ID = 99
	next, _ := m.Update(agentFlowLoadedMsg{flow: flow})

	if len(next.(model).editorNodes) != before {
		t.Fatal("replaced by another flow")
	}
}

func TestLongRepliesCanBeReadToTheEnd(t *testing.T) {
	fake := heddle(t, reply(strings.Repeat("line\n", 200)+"the end"))

	m := say(t, chatModel(t, fake), "hi")

	if !strings.Contains(strings.Join(m.renderAIChatBar(), "\n"), "the end") {
		t.Fatal("the end of the reply is out of reach")
	}
}

func TestChatBreaksLinesBetweenWords(t *testing.T) {
	m := sizedModel(140, 40)
	m.aiChatMessages = []string{"Agent: The flow is saved and ready to run whenever you confirm it"}

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
	m.aiChatMessages = []string{"Agent: " + strings.Repeat("x", 80)}

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
	m := chatModel(t, heddle(t, reply(strings.Repeat("a long reply ", 200))))
	body := m.paletteRect.h

	m = say(t, m, "hi")

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
	m := chatModel(t, heddle(t, ""))

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
	m := chatModel(t, heddle(t, ""))
	m.aiPromptInput.Blur()

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)

	if m.aiChatOpen || m.screen != screenWorkflowEditor {
		t.Fatalf("open = %v screen = %v", m.aiChatOpen, m.screen)
	}
}

func TestTheFooterSaysWhatTheAgentDidWhileTheBarWasClosed(t *testing.T) {
	m, cmd := send(t, chatModel(t, heddle(t, reply("done"))), "hi")
	m = m.closeAIChat()

	if !onScreen(m, "/ agent working") {
		t.Fatal("nothing says it's working")
	}

	m = settle(t, m, cmd)
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
	m := say(t, chatModel(t, heddle(t, runRequest("Ready."))), "run it")

	if !onScreen(m, "y run") {
		t.Fatal("the keys aren't on screen")
	}
}

func TestAnotherFlowStartsWithTheBarClosed(t *testing.T) {
	m := say(t, chatModel(t, heddle(t, reply("hello"))), "hi")

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
	fake := heddle(t, reply("On it."))

	m := askFromList(t, listModel(t, fake), "add a logger")

	if m.screen != screenWorkflowEditor {
		t.Fatalf("screen = %v", m.screen)
	}
	if fake.chatCount() != 1 || fake.chats[0]["flow_id"] != "7" {
		t.Fatalf("chats = %+v", fake.chats)
	}
	if !onScreen(m, "On it.") {
		t.Fatal("the reply isn't on screen")
	}
}

func TestTheListStillRunsItsCommands(t *testing.T) {
	fake := heddle(t, reply("no"))

	m := askFromList(t, listModel(t, fake), "help")

	if fake.chatCount() != 0 || m.screen != screenApp {
		t.Fatal("help went to the agent")
	}
}

func TestAFailedOpenDropsTheMessage(t *testing.T) {
	m := listModel(t, heddle(t, ""))
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
	m := say(t, chatModel(t, heddle(t, reply("hello"))), "hi")

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
