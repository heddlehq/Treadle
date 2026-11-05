package tui

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"treadle/internal/agent"
)

const (
	agentMessagePrefix = "Agent: "
	userMessagePrefix  = "You: "

	// Top border, title, divider, the input with its border, and the bottom border.
	aiChatChromeHeight = 7
	aiChatInputHeight  = 3

	minChatWidth           = 30
	maxChatWidth           = 52
	minCanvasWidthWithChat = 20
)

var (
	agentRoot, stopAgents = context.WithCancel(context.Background())

	// startAgent is replaced in tests.
	startAgent = agent.Start
)

// StopAgent kills a running agent, which would otherwise outlive the program.
func StopAgent() {
	stopAgents()
}

type agentEventMsg struct {
	run    int
	events <-chan agent.Event
	event  agent.Event
	closed bool
}

type agentFlowLoadedMsg struct {
	flow apiFlow
	err  error
}

func waitForAgent(run int, events <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		return agentEventMsg{run: run, events: events, event: event, closed: !ok}
	}
}

func reloadAfterAgentCmd(host, apiKey string, workflowID int) tea.Cmd {
	load := loadWorkflowCmd(host, apiKey, workflowID)
	return func() tea.Msg {
		loaded := load().(workflowLoadedMsg)
		return agentFlowLoadedMsg{flow: loaded.flow, err: loaded.err}
	}
}

// submitAIBuilderPrompt sends the message in the input. The agent reads the saved flow,
// so what is only on this canvas is saved first.
func (m model) submitAIBuilderPrompt() (model, tea.Cmd) {
	prompt := strings.TrimSpace(m.aiPromptInput.Value())
	if prompt == "" || m.agentBusy || m.agentAwaitingRun {
		return m, nil
	}

	m.aiPromptInput.SetValue("")
	m.appendChat(userMessagePrefix + prompt)

	if problem := agent.Problem(); problem != "" {
		m.appendChat(agentMessagePrefix + problem)
		return m, nil
	}

	m.agentBusy = true
	if m.editorDirty || m.editorWorkflowID == 0 {
		m.agentPendingPrompt = prompt
		cmd := m.saveCurrentWorkflow()
		if cmd == nil && !m.editorSaving {
			m.abortPendingAgent()
		}
		return m, cmd
	}

	return m.startAgentRun(prompt, false)
}

func (m *model) abortPendingAgent() {
	m.agentPendingPrompt = ""
	m.agentBusy = false
	m.appendChat(agentMessagePrefix + "The flow couldn't be saved, so nothing was sent.")
}

func (m model) startAgentRun(prompt string, allowRun bool) (model, tea.Cmd) {
	baseURL, err := normalizeBaseURL(m.activeHost)
	if err != nil {
		m.agentBusy = false
		m.appendChat(agentMessagePrefix + err.Error())
		return m, nil
	}

	if m.agentSessionID == "" {
		m.agentSessionID = agent.NewSessionID()
	}

	ctx, cancel := context.WithCancel(agentRoot)
	events, err := startAgent(ctx, agent.Options{
		Binary:    agent.Binary(),
		APIKey:    agent.APIKey(),
		Prompt:    prompt,
		System:    agent.SystemPrompt(m.editorWorkflowID, m.editorWorkflowName),
		MCPURL:    baseURL + "/mcp",
		Token:     m.activeAPIKey,
		SessionID: m.agentSessionID,
		Resume:    m.agentSessionStarted,
		AllowRun:  allowRun,
		Budget:    agent.Budget(),
	})
	if err != nil {
		cancel()
		m.agentBusy = false
		m.appendChat(agentMessagePrefix + "The agent couldn't start: " + err.Error())
		return m, nil
	}

	m.agentRun++
	m.agentCancel = cancel
	m.agentBusy = true
	m.agentReplying = false
	m.agentFailed = false
	m.agentDeniedFlow = ""
	return m, waitForAgent(m.agentRun, events)
}

func (m model) handleAgentEvent(msg agentEventMsg) (tea.Model, tea.Cmd) {
	// From a conversation that is already over.
	if msg.run != m.agentRun {
		return m, nil
	}
	if msg.closed {
		return m.finishAgentRun()
	}

	switch msg.event.Type {
	case agent.EventTurn:
		m.agentNewTurn = true
	case agent.EventText:
		m.appendAgentText(msg.event.Text)
	case agent.EventError:
		m.agentFailed = true
		m.agentReplying = false
		m.appendChat(agentMessagePrefix + msg.event.Text)
	case agent.EventRunDenied:
		m.agentDeniedFlow = fallbackString(msg.event.FlowID, fmt.Sprint(m.editorWorkflowID))
	}
	return m, waitForAgent(msg.run, msg.events)
}

func (m model) finishAgentRun() (tea.Model, tea.Cmd) {
	if m.agentCancel != nil {
		m.agentCancel()
		m.agentCancel = nil
	}
	m.agentBusy = false
	m.agentReplying = false

	switch {
	case !m.agentFailed:
		m.agentSessionStarted = true
	case !m.agentSessionStarted:
		// The id may be taken by the half of a session that failed.
		m.agentSessionID = ""
	}

	if m.agentDeniedFlow != "" && !m.agentFailed {
		m.agentAwaitingRun = true
		m.aiChatOpen = true
		m.reflow()
		m.appendChat(agentMessagePrefix + fmt.Sprintf(
			"Run flow %s? It can have real side effects. [y] run  [n] don't run", m.agentDeniedFlow))
	}

	if m.editorWorkflowID == 0 {
		return m, nil
	}
	if m.editorDirty {
		m.editorStatusMessage = "Saving now overwrites what the agent changed"
		return m, nil
	}
	return m, reloadAfterAgentCmd(m.activeHost, m.activeAPIKey, m.editorWorkflowID)
}

func (m model) decideAgentRun(approved bool) (model, tea.Cmd) {
	m.agentAwaitingRun = false
	if !approved {
		m.appendChat(userMessagePrefix + "Don't run it.")
		return m, nil
	}

	flow := m.agentDeniedFlow
	m.appendChat(userMessagePrefix + "Run it.")
	return m.startAgentRun(fmt.Sprintf("The user confirmed: run flow %s now, once.", flow), true)
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

// openAIChat shows the panel and puts the cursor in it, if the terminal has the room.
func (m model) openAIChat() (model, tea.Cmd) {
	m.aiChatOpen = true
	m.reflow()
	if m.aiChatRect.w == 0 {
		m.aiChatOpen = false
		m.reflow()
		m.editorStatusMessage = "The terminal is too narrow for the chat"
		return m, nil
	}
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
	m.agentReplying = false
	m.agentNewTurn = false
	m.agentFailed = false
	m.agentAwaitingRun = false
	m.agentDeniedFlow = ""
	m.agentPendingPrompt = ""
	m.agentSessionID = ""
	m.agentSessionStarted = false
	m.aiChatMessages = nil
	m.aiChatScroll = 0
}

func (m *model) appendChat(message string) {
	m.aiChatMessages = append(m.aiChatMessages, message)
	m.scrollAIChatToEnd()
}

func (m *model) appendAgentText(text string) {
	if !m.agentReplying {
		m.agentReplying = true
		m.agentNewTurn = false
		m.appendChat(agentMessagePrefix + text)
		return
	}
	if m.agentNewTurn {
		text = "\n\n" + text
	}
	m.agentNewTurn = false
	m.aiChatMessages[len(m.aiChatMessages)-1] += text
	m.scrollAIChatToEnd()
}

func (m model) aiChatVisibleLines() int {
	return max(m.aiChatRect.h-aiChatChromeHeight, 1)
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

// aiChatLines is what the panel shows, wrapped to width. Scrolling counts these and
// not the messages, or a long reply could never be read to its end.
func (m model) aiChatLines(width int) []string {
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)

	if len(m.aiChatMessages) == 0 {
		notice := fallbackString(agent.Problem(), "Ask the agent to build or change this flow.")
		var lines []string
		for _, line := range wrapWords(notice, width) {
			lines = append(lines, muted.Render(line))
		}
		return lines
	}

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
