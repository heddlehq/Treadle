package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"treadle/internal/agent"
)

const (
	agentMessagePrefix = "Agent: "
	userMessagePrefix  = "You: "

	// Only for the requests that send something: answers come through the stream.
	agentRequestTimeout = 30 * time.Second

	agentReconnectDelay = time.Second

	// The rule on top and the input.
	aiChatChromeHeight = 2

	minCanvasHeightWithChat = 5
)

// agentStreamMsg is something new in the conversation of flowID.
type agentStreamMsg struct {
	flowID int
	update agent.Update
	err    error
	next   tea.Cmd
}

type agentSentMsg struct {
	flowID int
	err    error
}

type agentFlowLoadedMsg struct {
	flow apiFlow
	err  error
}

func reloadAfterAgentCmd(host, apiKey string, workflowID int) tea.Cmd {
	load := loadWorkflowCmd(host, apiKey, workflowID)
	return func() tea.Msg {
		loaded := load().(workflowLoadedMsg)
		return agentFlowLoadedMsg{flow: loaded.flow, err: loaded.err}
	}
}

func (m model) agentAPI() (*agent.Client, error) {
	baseURL, err := normalizeBaseURL(m.activeHost)
	if err != nil {
		return nil, err
	}
	return &agent.Client{BaseURL: baseURL, Token: m.activeAPIKey}, nil
}

// followAgent keeps reading the conversation of the open flow, reconnecting if the
// connection drops. It lives on the server, so this also picks up work in progress.
func (m *model) followAgent() tea.Cmd {
	flowID := m.editorWorkflowID
	if flowID == 0 || m.agentFollowing == flowID {
		return nil
	}

	client, err := m.agentAPI()
	if err != nil {
		return nil
	}

	if m.agentStopFollowing != nil {
		m.agentStopFollowing()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.agentStopFollowing = cancel
	m.agentFollowing = flowID

	updates := make(chan agentStreamMsg)
	go streamAgent(ctx, client, flowID, updates)
	return waitForAgent(updates)
}

func streamAgent(ctx context.Context, client *agent.Client, flowID int, updates chan<- agentStreamMsg) {
	defer close(updates)

	// Opening a flow starts a new chat, like the web editor. The server keeps the old one,
	// and stays in it while the agent is working.
	_ = client.New(ctx, flowID)

	deliver := func(msg agentStreamMsg) {
		select {
		case updates <- msg:
		case <-ctx.Done():
		}
	}

	for ctx.Err() == nil {
		err := client.Follow(ctx, flowID, func(update agent.Update) {
			deliver(agentStreamMsg{flowID: flowID, update: update})
		})
		if errors.Is(err, agent.ErrUnauthorized) {
			deliver(agentStreamMsg{flowID: flowID, err: err})
			return
		}

		select {
		case <-time.After(agentReconnectDelay):
		case <-ctx.Done():
		}
	}
}

func waitForAgent(updates <-chan agentStreamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-updates
		if !ok {
			return nil
		}
		msg.next = waitForAgent(updates)
		return msg
	}
}

// askFromList takes a message from the workflow list to the editor, about the selected
// workflow or a new one, and sends it from there.
func (m model) askFromList(prompt string) (model, tea.Cmd) {
	m.agentQueuedPrompt = prompt

	next, cmd := m.openSelectedWorkflow()
	if next.screen != screenWorkflowEditor {
		return next, cmd
	}

	sent, send := next.sendQueuedPrompt()
	return sent, tea.Batch(cmd, send)
}

func (m model) sendQueuedPrompt() (model, tea.Cmd) {
	if m.agentQueuedPrompt == "" {
		return m, nil
	}

	m, focus := m.openAIChat()
	m.aiPromptInput.SetValue(m.agentQueuedPrompt)
	m.agentQueuedPrompt = ""

	m, send := m.submitAIBuilderPrompt()
	return m, tea.Batch(focus, send)
}

// submitAIBuilderPrompt sends the message in the input. The agent reads the saved flow,
// so what is only on this canvas is saved first.
func (m model) submitAIBuilderPrompt() (model, tea.Cmd) {
	prompt := strings.TrimSpace(m.aiPromptInput.Value())
	if prompt == "" || m.agentBusy || m.agentTranscript.Permission != nil {
		return m, nil
	}

	m.aiPromptInput.SetValue("")
	m.agentPrompt = prompt
	m.agentBusy = true

	if m.editorDirty || m.editorWorkflowID == 0 {
		m.agentWaitingForSave = true
		cmd := m.saveCurrentWorkflow()
		if cmd == nil && !m.editorSaving {
			m.abortPendingAgent()
		}
		return m, cmd
	}

	return m.askAgent()
}

func (m *model) abortPendingAgent() {
	m.agentWaitingForSave = false
	m.agentBusy = false
	m.agentPrompt = ""
	m.noteChat("The flow couldn't be saved, so nothing was sent.")
}

// askAgent hands the message to the server, which answers through the stream.
func (m model) askAgent() (model, tea.Cmd) {
	client, err := m.agentAPI()
	if err != nil {
		m.agentBusy = false
		m.noteChat(err.Error())
		return m, nil
	}

	flowID, prompt := m.editorWorkflowID, m.agentPrompt
	m.agentPrompt = ""
	m.agentSending = true
	follow := m.followAgent()

	send := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), agentRequestTimeout)
		defer cancel()
		return agentSentMsg{flowID: flowID, err: client.Send(ctx, flowID, prompt)}
	}
	return m, tea.Batch(follow, send)
}

func (m model) handleAgentSent(msg agentSentMsg) (tea.Model, tea.Cmd) {
	if msg.flowID != m.editorWorkflowID {
		return m, nil
	}
	m.agentSending = false

	if msg.err != nil {
		if errors.Is(msg.err, agent.ErrUnauthorized) {
			return m.returnToConnect()
		}
		m.agentBusy = false
		m.noteChat(msg.err.Error())
	}
	return m, nil
}

func (m model) handleAgentStream(msg agentStreamMsg) (tea.Model, tea.Cmd) {
	// From a flow that is no longer open.
	if msg.flowID != m.agentFollowing {
		return m, nil
	}
	if msg.err != nil {
		return m.returnToConnect()
	}

	var reload tea.Cmd
	if msg.update.Snapshot != nil {
		m.agentTranscript = *msg.update.Snapshot
		m.agentBusy = m.agentTranscript.Status == agent.StatusRunning || m.agentSending
	} else if event := msg.update.Event; event != nil {
		m.agentTranscript.Apply(*event)
		m, reload = m.afterAgentEvent(*event)
	}

	m.reflow()
	m.scrollAIChatToEnd()
	return m, tea.Batch(reload, msg.next)
}

func (m model) afterAgentEvent(event agent.Event) (model, tea.Cmd) {
	switch event.Type {
	case "status":
		m.agentBusy = event.Status == agent.StatusRunning
		m.agentUnread = event.Status == agent.StatusIdle && !m.aiChatOpen
	case "permission":
		// Nobody can answer what they can't see.
		if event.Permission != nil {
			m.aiChatOpen = true
			m.agentUnread = false
		}
	case "tool_finished":
		if m.agentChangedTheFlow(event.ID) {
			return m.reloadAfterAgent()
		}
	}
	return m, nil
}

func (m model) agentChangedTheFlow(toolID string) bool {
	for _, item := range m.agentTranscript.Items {
		if item.ID == toolID {
			return item.Name == "update_flow" && item.Status == agent.ToolOK
		}
	}
	return false
}

func (m model) reloadAfterAgent() (model, tea.Cmd) {
	if m.editorWorkflowID == 0 {
		return m, nil
	}
	if m.editorDirty {
		m.editorStatusMessage = "Saving now overwrites what the agent changed"
		return m, nil
	}
	return m, reloadAfterAgentCmd(m.activeHost, m.activeAPIKey, m.editorWorkflowID)
}

// decideAgentRun answers the question. The run happens on the server, once it has this.
func (m model) decideAgentRun(decision string) (model, tea.Cmd) {
	client, err := m.agentAPI()
	if err != nil {
		m.noteChat(err.Error())
		return m, nil
	}

	flowID := m.editorWorkflowID
	m.agentTranscript.Permission = nil
	m.reflow()
	m.scrollAIChatToEnd()

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), agentRequestTimeout)
		defer cancel()
		return agentSentMsg{flowID: flowID, err: client.Decide(ctx, flowID, decision)}
	}
}

// stopAgent ends what the agent is doing, on the server.
func (m model) stopAgent() (model, tea.Cmd) {
	client, err := m.agentAPI()
	if err != nil {
		return m, nil
	}

	flowID := m.editorWorkflowID
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), agentRequestTimeout)
		defer cancel()
		return agentSentMsg{flowID: flowID, err: client.Stop(ctx, flowID)}
	}
}

// handleAgentFlowLoaded brings in what the agent saved, keeping the view where it is.
// Edits made while it worked win, and the status bar says so.
func (m model) handleAgentFlowLoaded(msg agentFlowLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.editorStatusMessage = "Reload failed: " + truncatePlain(msg.err.Error(), 40)
		return m, nil
	}
	if m.screen != screenWorkflowEditor || msg.flow.ID != m.editorWorkflowID || len(msg.flow.Nodes) == 0 {
		return m, nil
	}
	if m.editorDirty {
		m.editorStatusMessage = "Saving now overwrites what the agent changed"
		return m, nil
	}

	next := m
	next.editorNodes, next.editorConnections = m.mapAPIFlowToEditor(msg.flow)
	next.editorWorkflowName = fallbackString(msg.flow.Name, m.editorWorkflowName)
	if reflect.DeepEqual(m.workflowRequestPayload(), next.workflowRequestPayload()) {
		return m, nil
	}

	next.undoStack, next.redoStack = nil, nil
	next.selectOnly(0)
	next.rebuildNodeRects()
	next.fitView()
	next.editorStatusMessage = "Updated by the agent"
	return next, loadWorkflowsCmd(m.activeHost, m.activeAPIKey)
}

// aiChatHeight fits the conversation, so an empty chat only takes the input and a long
// one a third of the editor, and leaves it all to the canvas when there isn't room.
func (m model) aiChatHeight(width, bodyHeight int) int {
	if !m.aiChatOpen {
		return 0
	}

	lines := len(m.aiChatLines(max(width-4, 1)))
	height := aiChatChromeHeight + min(lines, bodyHeight/3)
	if bodyHeight-aiChatChromeHeight-bodyHeight/3 < minCanvasHeightWithChat {
		return 0
	}
	return height
}

// openAIChat shows the bar and puts the cursor in it, if the terminal has the room.
func (m model) openAIChat() (model, tea.Cmd) {
	m.aiChatOpen = true
	m.reflow()
	if m.aiChatRect.h == 0 {
		m.aiChatOpen = false
		m.reflow()
		m.editorStatusMessage = "The terminal is too small for the chat"
		return m, nil
	}
	m.agentUnread = false
	m.scrollAIChatToEnd()
	follow := m.followAgent()
	return m, tea.Batch(m.aiPromptInput.Focus(), follow)
}

func (m model) closeAIChat() model {
	m.aiPromptInput.Blur()
	m.aiChatOpen = false
	m.reflow()
	return m
}

func (m *model) resetAgentChat() {
	if m.agentStopFollowing != nil {
		m.agentStopFollowing()
		m.agentStopFollowing = nil
	}
	m.agentFollowing = 0
	m.agentBusy = false
	m.agentSending = false
	m.agentWaitingForSave = false
	m.agentPrompt = ""
	m.agentUnread = false
	m.agentTranscript = agent.Transcript{}
	m.aiChatScroll = 0
	m.aiChatOpen = false
	m.aiPromptInput.Blur()
	m.reflow()
}

// noteChat shows something that happened here, which the server doesn't know about.
func (m *model) noteChat(message string) {
	m.agentTranscript.Apply(agent.Event{Type: "failed", Message: message})
	m.reflow()
	m.scrollAIChatToEnd()
}

func (m model) aiChatVisibleLines() int {
	return max(m.aiChatRect.h-aiChatChromeHeight, 0)
}

func (m model) aiChatLineCount() int {
	return len(m.aiChatLines(max(m.aiChatRect.w-4, 1)))
}

func (m *model) scrollAIChatToEnd() {
	m.aiChatScroll = clampScroll(math.MaxInt32, m.aiChatLineCount(), m.aiChatVisibleLines())
}

func (m *model) scrollAIChat(delta int) {
	m.aiChatScroll = clampScroll(m.aiChatScroll+delta, m.aiChatLineCount(), m.aiChatVisibleLines())
}

// aiChatLines is what the bar shows, wrapped to width. Scrolling counts these and
// not the messages, or a long reply could never be read to its end.
func (m model) aiChatLines(width int) []string {
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)

	agentLabel := lipgloss.NewStyle().Foreground(accentColor).Render("Agent") + muted.Render(": ")
	userLabel := lipgloss.NewStyle().Foreground(successColor).Render("You") + muted.Render(": ")

	var lines []string
	for _, item := range m.agentTranscript.Items {
		switch item.Role {
		case agent.RoleUser:
			lines = append(lines, labeledLines(userLabel, item.Text, len(userMessagePrefix), width)...)
		case agent.RoleAssistant:
			lines = append(lines, labeledLines(agentLabel, item.Text, len(agentMessagePrefix), width)...)
		case agent.RoleTool:
			lines = append(lines, toolLine(item, width))
		default:
			lines = append(lines, labeledLines(agentLabel, item.Text, len(agentMessagePrefix), width)...)
		}
	}

	if permission := m.agentTranscript.Permission; permission != nil {
		lines = append(lines, permissionCard(permission.FlowName, width)...)
	}
	return lines
}

// permissionCard stands out from the conversation: the agent is stopped until it is
// answered, and what it asks for has real effects.
func permissionCard(flowName string, width int) []string {
	title := lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)

	key := func(letter, label string) string {
		return title.Render(letter) + " " + muted.Render(label)
	}

	card := strings.Join([]string{
		title.Render(fmt.Sprintf("Run %q?", flowName)),
		muted.Render("The agent wants to test it, which can have real side effects."),
		"",
		key("y", "run") + "    " + key("a", "always in this chat") + "    " + key("n", "don't run"),
	}, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(0, 1).
		Width(max(width, 20))

	return strings.Split(box.Render(card), "\n")
}

// toolLine is one call of the agent: what it is doing, and how that went.
func toolLine(item agent.Item, width int) string {
	mark, color := "◌", mutedTextColor
	switch item.Status {
	case agent.ToolOK:
		mark, color = "✓", successColor
	case agent.ToolFailed:
		mark, color = "✗", errorColor
	}

	text := toolTitle(item.Name)
	if summary := toolSummary(item); summary != "" {
		text += " · " + summary
	}

	line := "  " + lipgloss.NewStyle().Foreground(color).Render(mark) + " "
	return line + lipgloss.NewStyle().Foreground(mutedTextColor).Render(truncatePlain(text, max(width-4, 8)))
}

func toolTitle(name string) string {
	switch name {
	case "list_flows":
		return "Listing flows"
	case "get_flow":
		return "Reading the flow"
	case "list_nodes":
		return "Checking the nodes"
	case "list_executions":
		return "Checking the runs"
	case "get_execution":
		return "Reading a run"
	case "update_flow":
		return "Changing the flow"
	case "create_flow":
		return "Creating a flow"
	case "test_flow":
		return "Testing the flow"
	case "run_flow":
		return "Running the flow"
	}
	return name
}

// toolSummary shows how a test ended, which is what matters about it.
func toolSummary(item agent.Item) string {
	if item.Name != "test_flow" || item.Output == "" {
		return ""
	}

	var result struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(item.Output), &result) != nil {
		return ""
	}
	if result.Error.Message != "" {
		return strings.ReplaceAll(result.Error.Message, "\n", " ")
	}
	return result.Status
}

func labeledLines(label, text string, indent, width int) []string {
	contentWidth := max(width-indent, 4)
	padding := strings.Repeat(" ", indent)

	var lines []string
	for _, line := range wrapWords(strings.TrimRight(text, "\n"), contentWidth) {
		if len(lines) == 0 {
			lines = append(lines, label+line)
		} else {
			lines = append(lines, padding+line)
		}
	}
	return lines
}

// wrapWords breaks at spaces, and only cuts a word longer than the whole line.
func wrapWords(text string, width int) []string {
	return strings.Split(ansi.Wrap(text, width, ""), "\n")
}
