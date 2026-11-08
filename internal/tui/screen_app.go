package tui

import (
	"errors"
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const workflowListTop = 3

func (m model) updateApp(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.composerInput.Focused() {
			if msg.Code == tea.KeyEnter {
				cmd := m.submitCommand()
				return m, cmd
			}
			switch msg.String() {
			case "esc":
				m.composerInput.Blur()
				return m, nil
			}

			var cmd tea.Cmd
			m.composerInput, cmd = m.composerInput.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "/":
			return m, m.composerInput.Focus()
		case "up":
			m.moveWorkflow(-1)
			return m, nil
		case "down":
			m.moveWorkflow(1)
			return m, nil
		case "n":
			m.createWorkflow()
			return m, nil
		case "r":
			return m.reloadWorkflows()
		case "L":
			return m.logOut()
		}
		if msg.Code == tea.KeyEnter {
			return m.openSelectedWorkflow()
		}
	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft {
			return m.handleAppClick(mouse.X, mouse.Y)
		}
	case tea.MouseWheelMsg:
		m.handleAppScroll(msg.Mouse())
		return m, nil
	}

	var cmd tea.Cmd
	m.composerInput, cmd = m.composerInput.Update(msg)
	return m, cmd
}

func (m model) handleWorkflowsLoaded(msg workflowsLoadedMsg) (tea.Model, tea.Cmd) {
	m.loadingWorkflows = false
	if errors.Is(msg.err, errUnauthorized) {
		return m.returnToConnect()
	}
	if msg.err != nil {
		m.workflows = nil
		m.availableNodes = nil
		m.selectedWorkflow = 0
		m.workflowScroll = 0
		m.workflowsError = msg.err.Error()
		m.appendActivity("Failed to load workflows: " + truncatePlain(msg.err.Error(), 56))
		return m, nil
	}

	m.rememberLogin()

	m.workflowsError = ""
	m.workflows = msg.workflows
	m.availableNodes = msg.nodes
	if len(m.workflows) == 0 {
		m.selectedWorkflow = 0
		m.workflowScroll = 0
		m.appendActivity("No workflows returned by server.")
		return m, nil
	}

	m.selectedWorkflow = 0
	m.workflowScroll = clampScroll(0, len(m.workflows)+1, max(m.workflowListRect.h, 1))
	m.appendActivity(fmt.Sprintf("Loaded %d workflows.", len(m.workflows)))
	return m, nil
}

// rememberLogin runs once the server has accepted the credentials, so a bad key
// never replaces a good one.
func (m *model) rememberLogin() {
	session := savedSession{Host: m.activeHost, Token: m.activeAPIKey, Method: m.activeMethod}
	if session == m.savedSession {
		return
	}
	if err := saveSession(session); err != nil {
		m.commandOutput = []string{"Couldn't save your login: " + err.Error()}
		return
	}
	m.savedSession = session
}

func (m model) logOut() (model, tea.Cmd) {
	clearSession()
	m.savedSession = savedSession{}
	m.screen = screenConnect
	m.connected = false
	m.connectError = ""
	m.activeAPIKey = ""
	m.workflows = nil
	m.workflowsError = ""
	m.apiKeyInput.SetValue("")
	m.composerInput.Blur()
	return m, m.apiKeyInput.Focus()
}

func (m model) returnToConnect() (tea.Model, tea.Cmd) {
	// A saved login the server now refuses would only fail again on the next start.
	if m.activeAPIKey == m.savedSession.Token {
		clearSession()
		m.savedSession = savedSession{}
	}

	m.screen = screenConnect
	m.connected = false
	switch {
	case m.activeMethod == loginOAuth:
		m.connectError = "Your sign-in expired. Sign in again."
	case strings.TrimSpace(m.apiKeyInput.Value()) == "":
		m.connectError = "This server needs an API key."
	default:
		m.connectError = "The server rejected this API key."
	}
	m.activeAPIKey = ""
	m.composerInput.Blur()
	m.hostInput.Blur()
	return m, m.apiKeyInput.Focus()
}

func (m model) reloadWorkflows() (model, tea.Cmd) {
	if m.loadingWorkflows {
		return m, nil
	}
	m.loadingWorkflows = true
	m.workflowsError = ""
	return m, tea.Batch(m.spinner.Tick, loadWorkflowsCmd(m.activeHost, m.activeAPIKey))
}

func (m model) renderAppScreen() string {
	sidebar := m.renderSidebarLines()
	main := m.renderMainLines()
	separator := lipgloss.NewStyle().Foreground(panelBorder).Render("│")

	rows := m.renderAppHeader()
	for i := range m.sidebarRect.h {
		rows = append(rows, padRightBlank(sidebar[i], m.sidebarRect.w)+separator+main[i])
	}
	rows = append(rows, m.renderCommandBar()...)

	hints := "↑↓ select  ⏎ open  n new  r reload  / agent  L log out  q quit"
	if m.composerInput.Focused() {
		hints = "⏎ send  esc cancel"
	}
	rows = append(rows, renderHints(hints, m.footerRect.w))

	return strings.Join(rows, "\n")
}

func (m model) renderAppHeader() []string {
	dotColor := successColor
	if m.workflowsError != "" {
		dotColor = errorColor
	}
	host := strings.TrimPrefix(strings.TrimPrefix(m.activeHost, "http://"), "https://")
	status := lipgloss.NewStyle().Foreground(dotColor).Render("●") +
		lipgloss.NewStyle().Foreground(mutedTextColor).Render(" "+truncatePlain(host, max(m.width/2, 8)))

	left := "  " + renderBrandTitle()
	right := status + "  "
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)

	return []string{
		left + blank(gap) + right,
		lipgloss.NewStyle().Foreground(panelBorder).Render(m.ruleWithJunction(m.sidebarRect.w, "─", "┬")),
	}
}

// ruleWithJunction draws a full-width rule that meets a vertical separator.
func (m model) ruleWithJunction(column int, rule, junction string) string {
	left := min(column, max(m.width-1, 0))
	return strings.Repeat(rule, left) + junction + strings.Repeat(rule, max(m.width-left-1, 0))
}

func statusColor(status string) color.Color {
	switch strings.ToLower(status) {
	case "active":
		return successColor
	case "running":
		return infoColor
	case "waiting":
		return warmColor
	case "failed":
		return errorColor
	default:
		return mutedTextColor
	}
}

// renderListRow draws one sidebar row. Selection fills the whole row, with the
// weft colour as its edge.
func renderListRow(marker string, markerColor color.Color, label string, width int, selected bool) string {
	label = truncatePlain(label, max(width-5, 1))

	style := lipgloss.NewStyle()
	edge := "  "
	if selected {
		style = style.Background(neutralColor)
		edge = style.Foreground(accentColor).Render("▌ ")
	}

	row := edge + style.Foreground(markerColor).Render(marker) + style.Render(" ")
	if selected {
		row += style.Bold(true).Foreground(textColor).Render(label)
		return row + style.Render(strings.Repeat(" ", max(width-4-lipgloss.Width(label), 0)))
	}
	return row + style.Foreground(textColor).Render(label)
}

func (m model) renderSidebarLines() []string {
	width := m.sidebarRect.w
	title := lipgloss.NewStyle().Foreground(mutedTextColor).Bold(true).Render("WORKFLOWS")
	if count := len(m.workflows); count > 0 {
		title += lipgloss.NewStyle().Foreground(mutedTextColor).Render(fmt.Sprintf("  %d", count))
	}
	lines := []string{"", "  " + title, ""}

	visibleHeight := m.workflowListRect.h
	offset := clampScroll(m.workflowScroll, len(m.workflows)+1, visibleHeight)
	for row := range visibleHeight {
		index := offset + row
		selected := index == m.selectedWorkflow
		switch {
		case index < len(m.workflows):
			wf := m.workflows[index]
			lines = append(lines, renderListRow("●", statusColor(wf.status), wf.name, width, selected))
		case index == len(m.workflows):
			lines = append(lines, renderListRow("+", accentColor, "New workflow", width, selected))
		default:
			lines = append(lines, "")
		}
	}

	return lines[:m.sidebarRect.h]
}

func (m model) renderMainLines() []string {
	height := m.mainRect.h
	helpHeight := 0
	if len(m.commandOutput) > 0 {
		helpHeight = len(m.commandOutput) + 1
	}

	var lines []string
	wf, selected := m.selectedWorkflowItem()
	contentHeight := max(height-helpHeight, 1)
	switch {
	case m.loadingWorkflows:
		lines = m.renderPaneMessage(contentHeight, false, m.spinner.View()+" Loading workflows", "")
	case m.workflowsError != "":
		detail := lipgloss.NewStyle().Foreground(errorColor).Render(truncatePlain(m.workflowsError, max(m.mainRect.w-6, 8)))
		lines = m.renderPaneMessage(contentHeight, false, "Couldn't load workflows", detail, "r  try again")
	case selected:
		lines = m.renderWorkflowDetails(wf)
	case len(m.workflows) == 0:
		lines = m.renderPaneMessage(contentHeight, true, "No workflows yet", "", "n  create your first one")
	default:
		lines = m.renderPaneMessage(contentHeight, true, "Start a new workflow", "", "⏎  open the editor")
	}

	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	lines = lines[:contentHeight]

	if helpHeight > 0 {
		lines = append(lines, "")
		for _, line := range m.commandOutput {
			lines = append(lines, "  "+lipgloss.NewStyle().Foreground(mutedTextColor).Render(truncatePlain(line, max(m.mainRect.w-4, 8))))
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}

func (m model) renderWorkflowDetails(wf workflow) []string {
	width := max(m.mainRect.w-4, 8)
	muted := lipgloss.NewStyle().Foreground(mutedTextColor).Render
	text := lipgloss.NewStyle().Foreground(textColor).Render

	row := func(label, value string) string {
		return "  " + muted(fmt.Sprintf("%-13s", label)) + value
	}

	return []string{
		"",
		"  " + lipgloss.NewStyle().Bold(true).Foreground(textColor).Render(truncatePlain(wf.name, width)),
		"  " + lipgloss.NewStyle().Foreground(statusColor(wf.status)).Render("● "+wf.status),
		"",
		row("Nodes", text(fmt.Sprintf("%d", wf.nodes))),
		row("Connections", text(fmt.Sprintf("%d", wf.connections))),
		row("Updated", text(wf.updated)),
		row("Created", text(wf.created)),
		"",
		renderHints("⏎ open in the editor", width),
	}
}

// renderPaneMessage centres a message in the main pane. The logo only appears
// when it fits at full size: smaller versions lose the shape of the mark.
func (m model) renderPaneMessage(height int, withMark bool, title, detail string, hints ...string) []string {
	width := m.mainRect.w
	center := func(line string) string {
		return strings.Repeat(" ", max((width-lipgloss.Width(line))/2, 0)) + line
	}

	var block []string
	if withMark && height >= len(treadleMark)/2+6 {
		for _, row := range renderMark(treadleMark) {
			block = append(block, center(row))
		}
		block = append(block, "")
	}

	block = append(block, center(lipgloss.NewStyle().Bold(true).Foreground(textColor).Render(title)))
	if detail != "" {
		block = append(block, center(detail))
	}
	for _, hint := range hints {
		name, description, _ := strings.Cut(hint, "  ")
		line := lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(name) +
			lipgloss.NewStyle().Foreground(mutedTextColor).Render("  "+description)
		block = append(block, "", center(line))
	}

	lines := make([]string, max((height-len(block))/2, 0))
	return append(lines, block...)
}

func (m model) renderCommandBar() []string {
	ruleColor := panelBorder
	rule := m.ruleWithJunction(m.sidebarRect.w, "─", "┴")
	promptColor := mutedTextColor
	if m.composerInput.Focused() {
		ruleColor = accentColor
		rule = m.ruleWithJunction(m.sidebarRect.w, "━", "┷")
		promptColor = accentColor
	}

	return []string{
		lipgloss.NewStyle().Foreground(ruleColor).Render(rule),
		"  " + lipgloss.NewStyle().Foreground(promptColor).Render("› ") + m.composerInput.View(),
	}
}

func (m *model) moveWorkflow(delta int) {
	next := m.selectedWorkflow + delta
	if next < 0 {
		next = 0
	}
	maxIndex := len(m.workflows)
	if next > maxIndex {
		next = maxIndex
	}
	m.selectedWorkflow = next
	m.ensureSelectedWorkflowVisible()
}

func (m *model) createWorkflow() {
	m.beginNewWorkflowEditor()
}

func (m *model) openSelectedWorkflow() (model, tea.Cmd) {
	if m.selectedWorkflow == len(m.workflows) {
		m.createWorkflow()
		return *m, nil
	}

	wf, ok := m.selectedWorkflowItem()
	if !ok {
		return *m, nil
	}

	if strings.TrimSpace(m.activeHost) == "" {
		m.beginWorkflowEditor(wf.name)
		m.appendActivity("Opened workflow without server details: " + wf.name)
		return *m, nil
	}

	m.loadingWorkflows = true
	m.workflowsError = ""
	m.appendActivity("Loading workflow: " + wf.name)
	return *m, tea.Batch(m.spinner.Tick, loadWorkflowCmd(m.activeHost, m.activeAPIKey, wf.id))
}

func (m *model) submitCommand() tea.Cmd {
	value := strings.TrimSpace(m.composerInput.Value())
	if value == "" {
		return nil
	}

	switch strings.ToLower(value) {
	case "help":
		m.commandOutput = []string{
			"Available commands:",
			"• help",
			"• create workflow",
			"• open selected workflow",
			"• run selected workflow",
			"• search workflow <name>",
		}
	case "create workflow":
		m.composerInput.SetValue("")
		m.beginNewWorkflowEditor()
		return nil
	case "open selected workflow":
		m.composerInput.SetValue("")
		next, cmd := m.openSelectedWorkflow()
		*m = next
		return cmd
	default:
		m.composerInput.SetValue("")
		m.composerInput.Blur()
		m.commandOutput = nil
		next, cmd := m.askFromList(value)
		*m = next
		return cmd
	}

	m.appendActivity("Command submitted: " + value)
	m.composerInput.SetValue("")
	return nil
}

func (m model) handleWorkflowLoaded(msg workflowLoadedMsg) (tea.Model, tea.Cmd) {
	m.loadingWorkflows = false
	if msg.err != nil {
		m.workflowsError = msg.err.Error()
		m.appendActivity("Failed to open workflow: " + truncatePlain(msg.err.Error(), 56))
		m.agentQueuedPrompt = ""
		return m, nil
	}

	m.beginWorkflowEditorFromAPI(msg.flow)
	m.appendActivity("Opened workflow: " + fallbackString(msg.flow.Name, fmt.Sprintf("Flow %d", msg.flow.ID)))
	return m.sendQueuedPrompt()
}

func (m model) handleWorkflowSaved(msg workflowSavedMsg) (tea.Model, tea.Cmd) {
	m.editorSaving = false
	runAfterSave := m.editorRunAfterSave
	m.editorRunAfterSave = false
	if msg.err != nil {
		m.editorStatusMessage = "Save failed: " + truncatePlain(msg.err.Error(), 40)
		m.appendActivity("Failed to save workflow: " + truncatePlain(msg.err.Error(), 56))
		if m.agentWaitingForSave {
			m.abortPendingAgent()
		}
		return m, nil
	}

	m.editorWorkflowID = msg.flow.ID
	m.editorWorkflowName = fallbackString(msg.flow.Name, m.editorWorkflowName)
	m.clearEditorDirty("All changes saved")
	m.appendActivity("Saved workflow: " + m.editorWorkflowName)

	reload := loadWorkflowsCmd(m.activeHost, m.activeAPIKey)
	var run tea.Cmd
	if runAfterSave {
		run = m.startExecution()
	}
	if !m.agentWaitingForSave {
		return m, tea.Batch(reload, run)
	}

	m.agentWaitingForSave = false
	next, ask := m.askAgent()
	return next, tea.Batch(reload, run, ask)
}

func (m model) selectedWorkflowItem() (workflow, bool) {
	if len(m.workflows) == 0 || m.selectedWorkflow < 0 || m.selectedWorkflow >= len(m.workflows) {
		return workflow{}, false
	}
	return m.workflows[m.selectedWorkflow], true
}

func (m *model) ensureSelectedWorkflowVisible() {
	visibleHeight := max(m.workflowListRect.h, 1)
	if m.selectedWorkflow < m.workflowScroll {
		m.workflowScroll = m.selectedWorkflow
	}
	if m.selectedWorkflow >= m.workflowScroll+visibleHeight {
		m.workflowScroll = clampScroll(m.selectedWorkflow-visibleHeight+1, len(m.workflows)+1, visibleHeight)
	}
}
