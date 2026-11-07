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

	"treadle/internal/mcp"
)

const (
	agentMessagePrefix = "Agent: "
	userMessagePrefix  = "You: "

	// The server thinks and calls its tools before it answers, and a confirmation can
	// keep it waiting.
	agentTimeout = 7 * time.Minute

	// The rule on top and the input.
	aiChatChromeHeight = 2

	minCanvasHeightWithChat = 5
)

// chatTurn is one message of the conversation. Heddle keeps no history: all of it goes
// with every message.
type chatTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type agentRunRequest struct {
	FlowID int            `json:"flow_id"`
	Name   string         `json:"name"`
	Input  map[string]any `json:"input"`
}

type agentReply struct {
	Reply      string           `json:"reply"`
	RunRequest *agentRunRequest `json:"run_request"`
}

type agentReplyMsg struct {
	run   int
	reply agentReply
	err   error
}

type agentRunStartedMsg struct {
	run         int
	executionID string
	err         error
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

// mcpClient reuses the connection for as long as the login stays the same.
func (m *model) mcpClient() (*mcp.Client, error) {
	baseURL, err := normalizeBaseURL(m.activeHost)
	if err != nil {
		return nil, err
	}

	url := baseURL + "/mcp"
	if m.agentClient == nil || m.agentClient.URL != url || m.agentClient.Token != m.activeAPIKey {
		m.agentClient = &mcp.Client{URL: url, Token: m.activeAPIKey}
	}
	return m.agentClient, nil
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
	if prompt == "" || m.agentBusy || m.agentRunRequest != nil {
		return m, nil
	}

	m.aiPromptInput.SetValue("")
	m.appendChat(userMessagePrefix + prompt)
	m.agentTurns = append(m.agentTurns, chatTurn{Role: "user", Content: prompt})
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
	m.agentTurns = m.agentTurns[:max(len(m.agentTurns)-1, 0)]
	m.appendChat(agentMessagePrefix + "The flow couldn't be saved, so nothing was sent.")
}

// askAgent sends the conversation to the chat tool of the server.
func (m model) askAgent() (model, tea.Cmd) {
	client, err := m.mcpClient()
	if err != nil {
		m.agentBusy = false
		m.appendChat(agentMessagePrefix + err.Error())
		return m, nil
	}

	arguments := map[string]any{"messages": append([]chatTurn{}, m.agentTurns...)}
	if m.editorWorkflowID != 0 {
		arguments["flow_id"] = fmt.Sprint(m.editorWorkflowID)
	}

	m.agentRun++
	run := m.agentRun
	ctx := m.agentContext()

	return m, func() tea.Msg {
		text, err := client.Call(ctx, "chat", arguments)
		if err != nil {
			return agentReplyMsg{run: run, err: err}
		}

		var reply agentReply
		if err := json.Unmarshal([]byte(text), &reply); err != nil {
			return agentReplyMsg{run: run, err: fmt.Errorf("invalid reply: %w", err)}
		}
		return agentReplyMsg{run: run, reply: reply}
	}
}

func (m *model) agentContext() context.Context {
	if m.agentCancel != nil {
		m.agentCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	m.agentCancel = cancel
	return ctx
}

func (m model) handleAgentReply(msg agentReplyMsg) (tea.Model, tea.Cmd) {
	// From a conversation that is already over.
	if msg.run != m.agentRun {
		return m, nil
	}
	m.agentBusy = false

	if msg.err != nil {
		if errors.Is(msg.err, mcp.ErrUnauthorized) {
			return m.returnToConnect()
		}
		m.agentTurns = m.agentTurns[:max(len(m.agentTurns)-1, 0)]
		m.appendChat(agentMessagePrefix + msg.err.Error())
		return m, nil
	}

	m.agentTurns = append(m.agentTurns, chatTurn{Role: "assistant", Content: msg.reply.Reply})
	if msg.reply.Reply != "" {
		m.appendChat(agentMessagePrefix + msg.reply.Reply)
	}

	if msg.reply.RunRequest != nil {
		m.agentRunRequest = msg.reply.RunRequest
		m.aiChatOpen = true
		m.agentUnread = false
		m.appendChat(agentMessagePrefix + fmt.Sprintf(
			"Run %q? It can have real side effects. [y] run  [n] don't run", msg.reply.RunRequest.Name))
	}

	return m.reloadAfterAgent()
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

// decideAgentRun answers the question. The server never runs a flow from the chat:
// that only happens here, with a call a person asked for.
func (m model) decideAgentRun(approved bool) (model, tea.Cmd) {
	request := m.agentRunRequest
	m.agentRunRequest = nil

	if !approved {
		m.appendChat(userMessagePrefix + "Don't run it.")
		m.agentTurns = append(m.agentTurns, chatTurn{Role: "user", Content: "Don't run it."})
		return m, nil
	}

	m.appendChat(userMessagePrefix + "Run it.")
	m.agentTurns = append(m.agentTurns, chatTurn{Role: "user", Content: "Run it."})

	client, err := m.mcpClient()
	if err != nil {
		m.appendChat(agentMessagePrefix + err.Error())
		return m, nil
	}

	arguments := map[string]any{"flow_id": fmt.Sprint(request.FlowID)}
	if request.Input != nil {
		arguments["input"] = request.Input
	}

	m.agentBusy = true
	m.agentRun++
	run := m.agentRun
	ctx := m.agentContext()

	return m, func() tea.Msg {
		text, err := client.Call(ctx, "run_flow", arguments)
		if err != nil {
			return agentRunStartedMsg{run: run, err: err}
		}

		var execution struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(text), &execution); err != nil {
			return agentRunStartedMsg{run: run, err: fmt.Errorf("invalid reply: %w", err)}
		}
		return agentRunStartedMsg{run: run, executionID: execution.ID}
	}
}

func (m model) handleAgentRunStarted(msg agentRunStartedMsg) (tea.Model, tea.Cmd) {
	if msg.run != m.agentRun {
		return m, nil
	}
	m.agentBusy = false

	text := "The flow couldn't start: "
	if msg.err != nil {
		text += msg.err.Error()
	} else {
		text = "Started execution " + msg.executionID + "."
	}
	m.agentTurns = append(m.agentTurns, chatTurn{Role: "assistant", Content: text})
	m.appendChat(agentMessagePrefix + text)
	return m, nil
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
	return m, m.aiPromptInput.Focus()
}

func (m model) closeAIChat() model {
	m.aiPromptInput.Blur()
	m.aiChatOpen = false
	m.reflow()
	return m
}

func (m *model) resetAgentChat() {
	if m.agentCancel != nil {
		m.agentCancel()
		m.agentCancel = nil
	}
	m.agentRun++
	m.agentBusy = false
	m.agentWaitingForSave = false
	m.agentRunRequest = nil
	m.agentUnread = false
	m.agentTurns = nil
	m.aiChatMessages = nil
	m.aiChatScroll = 0
	m.aiChatOpen = false
	m.aiPromptInput.Blur()
	m.reflow()
}

func (m *model) appendChat(message string) {
	m.aiChatMessages = append(m.aiChatMessages, message)
	m.agentUnread = !m.aiChatOpen
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
	for _, message := range m.aiChatMessages {
		switch {
		case strings.HasPrefix(message, userMessagePrefix):
			lines = append(lines, labeledLines(userLabel, message[len(userMessagePrefix):], len(userMessagePrefix), width)...)
		case strings.HasPrefix(message, agentMessagePrefix):
			lines = append(lines, labeledLines(agentLabel, message[len(agentMessagePrefix):], len(agentMessagePrefix), width)...)
		default:
			lines = append(lines, wrapWords(message, width)...)
		}
	}
	return lines
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
