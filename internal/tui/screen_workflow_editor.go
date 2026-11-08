package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"treadle/internal/agent"
)

// paletteChrome is what a palette takes besides its items: border, title and hints.
const paletteChrome = 7

const (
	// paletteHeader is the rows above the list in the node sidebar.
	paletteHeader = 2
	// paletteRevealMargin shows the heading above a node reached by going up.
	paletteRevealMargin = 1
)

func (m *model) beginNewWorkflowEditor() {
	m.beginWorkflowEditor(defaultWorkflowName)
}

func (m *model) beginWorkflowEditor(name string) {
	m.stopLive()
	m.resetAgentChat()
	m.screen = screenWorkflowEditor
	m.editorWorkflowID = 0
	m.editorWorkflowName = fallbackString(name, defaultWorkflowName)
	m.editorDirty = false
	m.editorSaving = false
	m.forgetExecution()
	m.editorStatusMessage = ""
	m.editorMode = editorModeNormal
	m.selectOnly(0)
	m.undoStack, m.redoStack, m.pasteCount = nil, nil, 0
	m.viewX, m.viewY, m.zoomIndex = 0, 0, defaultZoomIndex
	m.draggingNode = -1
	m.connectSource = -1
	m.paletteIndex = 0
	m.paletteScroll = 0
	m.editorOverlay = editorOverlayNone
	m.editorMenuSelected = 0
	m.editorMenuTarget = -1
	m.editorCreateAtMenu = false
	m.modalFocusedControl = 0
	m.modalDraft = nil
	m.editorNodes = []editorNode{
		{
			id:       "start",
			kind:     "Start",
			label:    "Start",
			x:        4,
			y:        3,
			w:        14,
			h:        3,
			controls: defaultControls("Start"),
		},
		{
			id:       "output",
			kind:     "Output",
			label:    "Output",
			x:        34,
			y:        3,
			w:        14,
			h:        3,
			controls: defaultControls("Output"),
		},
	}
	m.editorConnections = []editorConnection{{source: "start", sourceOutput: "exec", target: "output", targetInput: "exec"}}
	m.rebuildNodeRects()
}

func (m *model) beginWorkflowEditorFromAPI(flow apiFlow) {
	m.beginWorkflowEditor(fallbackString(flow.Name, defaultWorkflowName))
	m.editorWorkflowID = flow.ID

	editorNodes, editorConnections := m.mapAPIFlowToEditor(flow)
	if len(flow.Nodes) == 0 {
		// Saving sends every comment, so these can't be left out of the new flow.
		m.editorNodes = append(m.editorNodes, editorNodes...)
		m.rebuildNodeRects()
		return
	}

	m.editorNodes = editorNodes
	m.editorConnections = editorConnections
	m.selectOnly(0)
	m.fitView()
}

func (m *model) markEditorDirty() {
	m.editorDirty = true
	m.autosaveHeld = false
	if !m.editorSaving {
		m.editorStatusMessage = "Unsaved changes"
	}
}

func (m *model) clearEditorDirty(message string) {
	m.editorDirty = false
	m.editorSaving = false
	m.editorStatusMessage = message
}

func (m model) mapAPIFlowToEditor(flow apiFlow) ([]editorNode, []editorConnection) {
	editorNodes := make([]editorNode, 0, len(flow.Nodes))
	for i, item := range flow.Nodes {
		kind := m.normalizeNodeKind(item.Type)
		label := fallbackString(item.Label, m.nodeTitleForKind(kind))
		width, height := m.nodeSize(kind, label)
		node := editorNode{
			id:       apiRefString(item.ID),
			kind:     kind,
			label:    label,
			x:        int(math.Round(item.Position.X / pixelsPerColumn)),
			y:        int(math.Round(item.Position.Y / pixelsPerRow)),
			w:        width,
			h:        height,
			controls: m.controlsFromAPI(kind, label, item.Controls),
		}
		node.loaded = true
		node.loadedAt = canvasPoint{node.x, node.y}
		node.loadedPos = item.Position
		if node.id == "" {
			node.id = fmt.Sprintf("node_%d", i+1)
		}
		editorNodes = append(editorNodes, node)
	}

	for i, item := range flow.Comments {
		editorNodes = append(editorNodes, commentFromAPI(item, i))
	}

	editorConnections := make([]editorConnection, 0, len(flow.Connections))
	for _, conn := range flow.Connections {
		source := apiRefString(conn.Source)
		target := apiRefString(conn.Target)
		if source == "" || target == "" {
			continue
		}
		editorConnections = append(editorConnections, editorConnection{
			source:       source,
			sourceOutput: conn.SourceOutput,
			target:       target,
			targetInput:  conn.TargetInput,
		})
	}

	return editorNodes, editorConnections
}

func (m *model) saveCurrentWorkflow() tea.Cmd {
	if m.liveActive() {
		// Everything is saved as it changes: this only sends it now.
		m.liveSendQueued = false
		return m.sendNow()
	}
	if m.editorSaving {
		return nil
	}
	if strings.TrimSpace(m.activeHost) == "" {
		m.editorStatusMessage = "Missing server connection"
		return nil
	}
	if m.editorWorkflowID != 0 && !m.editorDirty {
		m.editorStatusMessage = "All changes saved"
		return nil
	}

	m.editorSaving = true
	m.editorStatusMessage = "Saving changes..."
	return saveWorkflowCmd(m.activeHost, m.activeAPIKey, m.editorWorkflowID, m.workflowRequestPayload())
}

func (m model) workflowRequestPayload() workflowRequest {
	nodes := make([]apiNode, 0, len(m.editorNodes))
	comments := []apiComment{}
	for _, node := range m.editorNodes {
		if node.isComment() {
			comments = append(comments, node.apiComment())
			continue
		}
		nodes = append(nodes, apiNode{
			ID:       node.id,
			Type:     m.nodeTypeName(node.kind),
			Label:    node.label,
			Position: node.savedPosition(),
			Controls: node.controlsMap(),
		})
	}

	connections := make([]apiSaveConnection, 0, len(m.editorConnections))
	for _, conn := range m.editorConnections {
		output, input := m.savedPorts(conn)
		connections = append(connections, apiSaveConnection{
			Source:       conn.source,
			SourceOutput: output,
			Target:       conn.target,
			TargetInput:  input,
		})
	}

	return workflowRequest{
		Workflow: workflowPayload{
			Name:        m.editorWorkflowName,
			Nodes:       nodes,
			Connections: connections,
			Comments:    comments,
		},
	}
}

func (m model) updateWorkflowEditor(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.updateWorkflowEditorKey(msg)
	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		switch mouse.Button {
		case tea.MouseLeft:
			return m.handleWorkflowEditorClick(mouse)
		case tea.MouseMiddle:
			m.panning = true
			m.panLastX, m.panLastY = mouse.X, mouse.Y
		case tea.MouseRight:
			m.openWorkflowContextMenu(mouse.X, mouse.Y)
		}
	case tea.MouseMotionMsg:
		m.handleWorkflowEditorMotion(msg.Mouse())
	case tea.MouseReleaseMsg:
		mouse := msg.Mouse()
		m.draggingNode = -1
		m.panning = false
		if m.wireActive {
			m.finishWire(mouse.X, mouse.Y)
		}
		if m.marqueeActive {
			m.marqueeX, m.marqueeY = mouse.X, mouse.Y
			m.finishMarquee()
		}
	case tea.MouseWheelMsg:
		m.handleWorkflowEditorWheel(msg.Mouse())
		return m, nil
	default:
		// Paste and the cursor blink.
		if m.editorMode == editorModeModal {
			var cmd tea.Cmd
			m.modalInput, cmd = m.modalInput.Update(msg)
			return m, cmd
		}
		if m.aiPromptInput.Focused() {
			var cmd tea.Cmd
			m.aiPromptInput, cmd = m.aiPromptInput.Update(msg)
			return m, cmd
		}
		if m.publishInput.Focused() {
			var cmd tea.Cmd
			m.publishInput, cmd = m.publishInput.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m model) updateWorkflowEditorKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if m.editorMode == editorModeModal {
		return m.updateNodeModalKey(msg)
	}
	if m.editorOverlay == editorOverlayResult {
		return m.updateExecutionResultKey(msg), nil
	}
	switch m.editorOverlay {
	case editorOverlayHelp:
		return m.updateHelpKey(msg)
	case editorOverlayPublish:
		return m.updatePublishKey(msg)
	case editorOverlayVersions:
		return m.updateVersionsKey(msg)
	case editorOverlayCommand, editorOverlayCreate, editorOverlayGoTo:
		return m.updatePaletteKey(msg)
	case editorOverlayNone:
	default:
		return m.updateEditorOverlayKey(msg)
	}
	if m.agentTranscript.Permission != nil {
		switch msg.String() {
		case "y":
			return m.decideAgentRun(agent.DecideOnce)
		case "a":
			return m.decideAgentRun(agent.DecideAlways)
		case "n", "esc":
			return m.decideAgentRun(agent.DecideDeny)
		}
	}
	if m.agentBusy && msg.String() == "ctrl+x" {
		return m.stopAgent()
	}
	if m.aiPromptInput.Focused() {
		return m.updateAIBuilderInput(msg)
	}

	switch msg.String() {
	case "/":
		return m.openAIChat()
	case "?":
		m.openHelp()
		return m, nil
	case "esc":
		if m.editorMode == editorModeAdd || m.editorMode == editorModeConnect {
			m.cancelConnection()
			return m, nil
		}
		if m.hasSelectedConnection() || len(m.selectedIndices()) > 1 {
			m.clearSelection()
			return m, nil
		}
		if m.aiChatOpen {
			return m.closeAIChat(), nil
		}
		m.screen = screenApp
		return m, nil
	case "ctrl+z":
		return m, m.undoStep()
	case "ctrl+y", "ctrl+shift+z":
		return m, m.redoStep()
	case "ctrl+c":
		m.copySelection()
		m.pasteCount = 0
		return m, nil
	case "ctrl+v":
		m.pasteNearOriginal()
		return m, nil
	case "ctrl+p", "ctrl+shift+p":
		m.openWorkflowCommandPalette()
		return m, nil
	case "ctrl+s":
		return m, m.saveCurrentWorkflow()
	case "ctrl+r":
		return m, m.runCurrentWorkflow()
	case "ctrl+a":
		m.selectAllNodes()
		return m, nil
	case "+", "=":
		m.zoomCentered(m.zoomIndex + 1)
		return m, nil
	case "-":
		m.zoomCentered(m.zoomIndex - 1)
		return m, nil
	case "0":
		m.zoomCentered(defaultZoomIndex)
		return m, nil
	case "f":
		m.fitView()
		return m, nil
	case "tab":
		if m.editorMode == editorModeConnect {
			m.selectNextNode(1)
			return m, nil
		}
		return m.cycleEditorPanel(1)
	case "shift+tab":
		if m.editorMode == editorModeConnect {
			m.selectNextNode(-1)
			return m, nil
		}
		return m.cycleEditorPanel(-1)
	case "]":
		m.selectNextNode(1)
	case "[":
		m.selectNextNode(-1)
	case "ctrl+left":
		m.panBy(-wheelColumns, 0)
	case "ctrl+right":
		m.panBy(wheelColumns, 0)
	case "ctrl+up":
		m.panBy(0, -wheelRows)
	case "ctrl+down":
		m.panBy(0, wheelRows)
	case "up", "down":
		if m.editorMode == editorModeAdd {
			delta := 1
			if msg.String() == "up" {
				delta = -1
			}
			nodes := m.getPaletteNodes()
			m.paletteIndex = clamp(m.paletteIndex+delta, 0, len(nodes)-1)
			m.revealPaletteNode()
			return m, nil
		}
	case "right":
		if m.editorMode == editorModeAdd {
			m.editorMode = editorModeNormal
			return m, nil
		}
	}
	if m.editorMode != editorModeAdd {
		// In connect mode this picks the node the wire goes to.
		if dx, dy, ok := arrowDirection(msg.String()); ok {
			m.selectToward(dx, dy)
			return m, nil
		}
		if dx, dy, ok := moveDirection(msg.String()); ok {
			m.moveSelectedEditorNode(dx, dy)
			return m, nil
		}
	}
	if msg.Code == tea.KeyEnter {
		switch m.editorMode {
		case editorModeAdd:
			nodes := m.getPaletteNodes()
			if m.paletteIndex >= 0 && m.paletteIndex < len(nodes) {
				m.addEditorNode(nodes[m.paletteIndex].Type)
			}
			m.editorMode = editorModeNormal
		case editorModeConnect:
			m.finishEditorConnection()
		default:
			m.openNodeModal()
		}
	}
	// The nodes panel has the focus: these act on the canvas.
	if m.editorMode == editorModeAdd {
		return m, nil
	}
	switch msg.String() {
	case "t":
		return m.openTerminal()
	case "p":
		if m.editorWorkflowID != 0 {
			m.openPublish()
		}
	case "v":
		if m.editorWorkflowID != 0 {
			return m, m.openVersions()
		}
	case "T":
		if m.terminalOpen {
			m.terminalWide = !m.terminalWide
			m.reflow()
		}
	case "a":
		m.editorMode = editorModeAdd
	case "g":
		m.openGoTo()
	case "w":
		m.walkWires()
	case "n":
		x, y := m.newCommentSpot()
		m.addComment(commentNote, x, y)
	case "o":
		m.openExecutionResult()
	case "c":
		if m.editorMode == editorModeConnect {
			m.cycleConnectOutput()
		} else {
			m.beginConnectionFromSelected()
		}
	case "backspace", "delete":
		m.deleteSelection()
	}

	return m, nil
}

func (m model) renderWorkflowEditor() string {
	if m.editorMode == editorModeModal {
		return m.renderNodeModal()
	}
	if m.editorOverlay == editorOverlayResult {
		return m.renderExecutionResult()
	}
	if m.isPalette() {
		return m.renderWorkflowCommandPalette()
	}
	switch m.editorOverlay {
	case editorOverlayHelp:
		return m.renderHelp()
	case editorOverlayPublish:
		return m.renderPublish()
	case editorOverlayVersions:
		return m.renderVersions()
	}

	palette := m.renderPalettePane()
	canvas := m.renderCanvasPane()
	separator := lipgloss.NewStyle().Foreground(panelBorder).Render("│")
	rule := lipgloss.NewStyle().Foreground(panelBorder).Render

	var chat []string
	if m.aiChatRect.h > 0 {
		chat = m.renderAIChatBar()
	}

	var terminal []string
	if m.terminalOpen {
		terminal = m.renderTerminalPane()
	}

	rows := m.renderWorkflowTopBar()
	for i := range m.paletteRect.h {
		if m.terminalCoversEditor() {
			rows = append(rows, terminal[i])
			continue
		}
		row := padRightBlank(palette[i], m.paletteRect.w) + separator
		// The conversation can change between a layout and a frame, and a short frame is
		// better than a crash.
		if i < m.canvasRect.h {
			row += canvas[i]
		} else if line := i - m.canvasRect.h; line < len(chat) {
			row += chat[line]
		}
		if m.terminalOpen {
			row = padRightBlank(row, m.terminalRect.x-sectionGapSize) + separator + terminal[i]
		}
		rows = append(rows, row)
	}
	rows = append(rows,
		rule(m.ruleWithJunction(m.paletteRect.w, "─", "┴")),
		m.renderWorkflowFooter(),
	)
	return strings.Join(rows, "\n")
}

// renderPalettePane keeps the first node row two rows down, where
// paletteNodeKindAtRow expects it.
func (m model) renderPalettePane() []string {
	title := lipgloss.NewStyle().Foreground(mutedTextColor).Bold(true).Render("NODES")
	if m.focusedPanel() == panelNodes {
		title = panelMarker() + lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("NODES")
	}
	list := m.renderPaletteLines()
	start := m.paletteOffset()
	end := min(start+m.paletteListRows(), len(list))

	lines := append([]string{"", "  " + title}, list[start:end]...)
	for len(lines) < m.paletteRect.h {
		lines = append(lines, "")
	}
	if m.paletteOverflows() {
		lines[m.paletteRect.h-1] = m.renderPaletteScrollHint()
	}
	return lines[:m.paletteRect.h]
}

// The panels of the editor, in the order tab walks them.
const (
	panelCanvas = iota
	panelNodes
	panelAgent
	panelCount
)

func (m model) focusedPanel() int {
	switch {
	case m.aiPromptInput.Focused():
		return panelAgent
	case m.editorMode == editorModeAdd:
		return panelNodes
	default:
		return panelCanvas
	}
}

func panelMarker() string {
	return lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("▸ ")
}

// cycleEditorPanel moves the focus. Leaving the agent keeps its conversation open.
func (m model) cycleEditorPanel(delta int) (model, tea.Cmd) {
	next := (m.focusedPanel() + delta + panelCount) % panelCount

	m.aiPromptInput.Blur()
	m.editorMode = editorModeNormal
	switch next {
	case panelNodes:
		m.editorMode = editorModeAdd
		m.revealPaletteNode()
	case panelAgent:
		return m.openAIChat()
	}
	return m, nil
}

// paletteOverflows says the nodes don't fit, which costs a row for the scroll hint.
func (m model) paletteOverflows() bool {
	return len(m.paletteNodeRows()) > max(m.paletteRect.h-paletteHeader, 1)
}

// paletteListRows is how many rows of the list are on screen.
func (m model) paletteListRows() int {
	rows := max(m.paletteRect.h-paletteHeader, 1)
	if m.paletteOverflows() {
		return max(rows-1, 1)
	}
	return rows
}

// paletteOffset is the first row on screen. A smaller terminal can leave the saved
// scroll past the end, so everything reads it from here.
func (m model) paletteOffset() int {
	return clamp(m.paletteScroll, 0, max(len(m.paletteNodeRows())-m.paletteListRows(), 0))
}

func (m *model) scrollPalette(delta int) {
	m.paletteScroll = m.paletteOffset() + delta
	m.paletteScroll = m.paletteOffset()
}

// revealPaletteNode scrolls just enough to show the node picked with the arrows.
func (m *model) revealPaletteNode() {
	row, seen := 0, -1
	for i, kind := range m.paletteNodeRows() {
		if kind != "" {
			seen++
		}
		if seen == m.paletteIndex {
			row = i
			break
		}
	}

	offset := m.paletteOffset()
	switch {
	case row < offset:
		m.scrollPalette(row - offset - paletteRevealMargin)
	case row >= offset+m.paletteListRows():
		m.scrollPalette(row - offset - m.paletteListRows() + 1)
	}
}

func (m model) renderPaletteScrollHint() string {
	above, below := 0, 0
	end := m.paletteOffset() + m.paletteListRows()
	for i, kind := range m.paletteNodeRows() {
		switch {
		case kind == "":
		case i < m.paletteOffset():
			above++
		case i >= end:
			below++
		}
	}

	hints := []string{}
	if above > 0 {
		hints = append(hints, fmt.Sprintf("↑ %d", above))
	}
	if below > 0 {
		hints = append(hints, fmt.Sprintf("↓ %d more", below))
	}
	hint := truncatePlain(strings.Join(hints, "   "), max(m.paletteRect.w-3, 1))
	return "  " + lipgloss.NewStyle().Foreground(accentColor).Render(hint)
}

// renderCanvasPane keeps the canvas two columns and two rows in, the origin the
// mouse and drag handling measure from.
func (m model) renderCanvasPane() []string {
	lines := []string{"", "  " + m.renderCanvasCaption()}
	for _, line := range m.renderCanvasLines() {
		lines = append(lines, "  "+line)
	}
	for len(lines) < m.canvasRect.h {
		lines = append(lines, "")
	}
	return lines[:m.canvasRect.h]
}

func (m model) renderCanvasCaption() string {
	width := max(m.canvasRect.w-4, 8)
	if m.editorMode == editorModeConnect {
		return lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render(truncatePlain(m.connectCaption(), width))
	}

	caption := plural(m.flowNodeCount(), "node") + "  ·  " + plural(len(m.editorConnections), "connection")
	switch count := len(m.selectedIndices()); {
	case m.hasSelectedConnection():
		caption = m.selectedConnectionCaption()
	case count > 1:
		caption = fmt.Sprintf("%d nodes selected. shift+arrows move them together, del removes them.", count)
	case count == 1 && m.editorNodes[m.selectedNode].isComment():
		caption = "⏎ edits it: its text, type and color. Drag to move it."
	case count == 1:
		caption = "⏎ edits it, c connects it, a adds a node after it, w walks its wires."
	}
	caption += fmt.Sprintf("  ·  %d%%", scaled(100, m.zoom()))
	if m.focusedPanel() != panelCanvas {
		return lipgloss.NewStyle().Foreground(mutedTextColor).Render(truncatePlain(caption, width))
	}
	return panelMarker() + lipgloss.NewStyle().Foreground(mutedTextColor).Render(truncatePlain(caption, width-lipgloss.Width(panelMarker())))
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func (m model) renderWorkflowTopBar() []string {
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)
	name := lipgloss.NewStyle().Bold(true).Foreground(textColor).Render(truncatePlain(m.editorWorkflowName, max(m.width/3, 8)))
	left := "  " + renderBrandTitle() + muted.Render("  │  ") + name

	save := muted.Bold(true).Render("Save")
	switch {
	case m.liveActive() || m.liveState == liveUnsupported:
		label, tone := m.liveStatus()
		save = lipgloss.NewStyle().Foreground(tone).Render(label)
	case m.editorSaving:
		save = muted.Bold(true).Render("Saving...")
	case m.editorDirty:
		save = lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("Save")
	}
	run := lipgloss.NewStyle().Foreground(successColor).Bold(true).Render("Run")
	if m.runBusy() {
		run = muted.Bold(true).Render(runningLabel)
	}
	right := run + "   " + save + "  "
	if people := m.renderPeople(max(m.width/4, 8)); people != "" {
		right = people + "    " + right
	}
	if status := m.renderEditorStatus(max(m.width-lipgloss.Width(left)-lipgloss.Width(right)-8, 8)); status != "" {
		right = status + "    " + right
	}

	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return []string{
		left + blank(gap) + right,
		lipgloss.NewStyle().Foreground(panelBorder).Render(m.ruleWithJunction(m.paletteRect.w, "─", "┬")),
	}
}

func (m model) renderEditorStatus(width int) string {
	message := strings.TrimSpace(m.editorStatusMessage)
	if message == "" {
		return ""
	}

	statusColor := mutedTextColor
	switch {
	case strings.HasPrefix(message, "Unsaved"):
		statusColor = warmColor
	case strings.HasPrefix(message, "Save failed"), strings.HasPrefix(message, "Missing"), strings.HasPrefix(message, "Can't"),
		strings.HasPrefix(message, "Execution failed"):
		statusColor = errorColor
	case strings.HasPrefix(message, "Execution finished"):
		statusColor = successColor
	}
	return lipgloss.NewStyle().Foreground(statusColor).Render("● " + truncatePlain(message, width))
}

func (m model) workflowFooterText() string {
	if m.terminalTakesKeys() {
		return terminalLeave + " back to the canvas  shift+pgup scroll back  every other key goes to Claude Code"
	}
	if m.editorMode == editorModeConnect {
		return "←↑↓→ choose the node  c next output  ⏎ connect  esc cancel"
	}
	if m.agentTranscript.Permission != nil {
		return "y run  a always in this chat  n don't run"
	}
	if m.aiPromptInput.Focused() {
		if m.agentBusy {
			return "ctrl+x stop  tab next panel  esc close"
		}
		return "⏎ send  pgup pgdn scroll  tab next panel  esc close"
	}
	if m.editorMode == editorModeAdd {
		return "↑↓ choose  ⏎ add  tab next panel  esc canvas"
	}
	if m.hasSelectedConnection() {
		return "w next wire  del delete it  esc back"
	}
	return strings.Join(m.canvasFooterHints(), "  ")
}

// renderWorkflowFooter keeps the way to every other key on the right, always.
func (m model) renderWorkflowFooter() string {
	right := "? keys  ctrl+p commands"
	// Those keys belong to the Terminal while it has the focus.
	if m.terminalTakesKeys() {
		right = ""
	}
	return renderFooter(strings.Split(m.workflowFooterText(), "  "), right, m.footerRect.w)
}

// agentHint is also how a reply reaches someone who closed the bar.
func (m model) agentHint() string {
	switch {
	case m.agentBusy && !m.aiChatOpen:
		return "/ agent working..."
	case m.agentUnread:
		return "/ agent replied"
	}
	return "/ agent"
}

func (m model) runBusy() bool {
	return m.editorRunInFlight || m.editorRunAfterSave
}

// workflowRunButtonRect sits to the left of Save, the way the top bar draws it.
func (m model) workflowRunButtonRect() rect {
	label := "Run"
	if m.runBusy() {
		label = runningLabel
	}
	width := lipgloss.Width(label)
	return rect{x: max(m.workflowSaveButtonRect().x-3-width, 0), y: 0, w: width, h: 1}
}

func (m model) workflowSaveButtonRect() rect {
	label := "Save"
	switch {
	case m.liveActive() || m.liveState == liveUnsupported:
		label, _ = m.liveStatus()
	case m.editorSaving:
		label = "Saving..."
	}
	rightWidth := lipgloss.Width(label) + 2
	return rect{x: max(m.width-rightWidth, 0), y: 0, w: lipgloss.Width(label), h: 1}
}

func (m model) renderCanvasLines() []string {
	w := max(m.canvasRect.w-4, 1)
	h := max(m.canvasRect.h-3, 1)
	grid := newCanvasGrid(w, h)
	drawCanvasDots(grid)

	m.drawComments(grid)
	m.drawConnections(grid)
	m.drawPendingWire(grid)
	for i, node := range m.editorNodes {
		if !node.isComment() {
			m.drawNode(grid, i, m.projectNode(node))
		}
	}
	m.drawLiveCursors(grid)
	m.drawMarquee(grid)
	if m.editorOverlay == editorOverlayContext {
		m.drawContextMenu(grid)
	}

	lines := make([]string, len(grid))
	for y := range grid {
		var b strings.Builder
		for _, cell := range grid[y] {
			value := string(cell.ch)
			if cell.style != nil {
				value = cell.style.Render(value)
			}
			b.WriteString(value)
		}
		lines[y] = b.String()
	}
	return lines
}

func (m model) getPaletteNodes() []apiNodeDef {
	if len(m.availableNodes) == 0 {
		return []apiNodeDef{
			{Type: "Start", Title: "Start", Category: "FLOW CONTROL", Icon: "play", Outputs: []any{"exec"}},
			{Type: "Condition", Title: "Condition", Category: "FLOW CONTROL", Icon: "arrow", Inputs: []any{"exec"}, Outputs: []any{"true", "false"}},
			{Type: "Output", Title: "Output", Category: "FLOW CONTROL", Icon: "check", Inputs: []any{"exec"}},
			{Type: "Variable", Title: "Variable", Category: "DATA", Icon: "variable", Inputs: []any{"exec"}, Outputs: []any{"exec"}},
			{Type: "Evaluate Code", Title: "Evaluate Code", Category: "CODE", Icon: "code", Inputs: []any{"exec"}, Outputs: []any{"exec"}},
		}
	}
	return m.availableNodes
}

func (m model) nodeDefinition(kind string) (apiNodeDef, bool) {
	if kind == commentNodeKind {
		return commentDefinition, true
	}
	for _, def := range m.getPaletteNodes() {
		if slugifyNodeToken(def.Type) == slugifyNodeToken(kind) {
			return def, true
		}
	}
	return apiNodeDef{}, false
}

// nodeTypeName is the name the server knows a node by, such as "Evaluate Code".
func (m model) nodeTypeName(kind string) string {
	if def, ok := m.nodeDefinition(kind); ok {
		return def.Type
	}
	return kind
}

func paletteCategory(def apiNodeDef) string {
	if def.Category == "" {
		return "OTHER"
	}
	return strings.ToUpper(strings.ReplaceAll(def.Category, "_", " "))
}

func (m model) renderPaletteLines() []string {
	heading := lipgloss.NewStyle().Foreground(mutedTextColor)
	lines := []string{""}

	currentCategory := ""
	for i, def := range m.getPaletteNodes() {
		if category := paletteCategory(def); category != currentCategory {
			if currentCategory != "" {
				lines = append(lines, "")
			}
			currentCategory = category
			lines = append(lines, "  "+heading.Render(category))
		}

		selected := m.editorMode == editorModeAdd && i == m.paletteIndex
		lines = append(lines, renderListRow(mapIconToTerminal(def.Icon), categoryColor(def.Category), def.Title, m.paletteRect.w, selected))
	}
	return lines
}

// paletteNodeRows mirrors renderPaletteLines, with the node type on each node row.
func (m model) paletteNodeRows() []string {
	rows := []string{""}

	currentCategory := ""
	for _, def := range m.getPaletteNodes() {
		if category := paletteCategory(def); category != currentCategory {
			if currentCategory != "" {
				rows = append(rows, "")
			}
			currentCategory = category
			rows = append(rows, "")
		}
		rows = append(rows, def.Type)
	}
	return rows
}

func (m model) paletteNodeKindAtRow(row int) (string, bool) {
	rows := m.paletteNodeRows()
	if row < 0 || row >= len(rows) {
		return "", false
	}
	kind := rows[row]
	return kind, kind != ""
}

// renderAIChatBar has the look of the command bar of the workflow list: a rule, the
// conversation and the input.
func (m model) renderAIChatBar() []string {
	ruleColor := panelBorder
	ruleChar := "─"
	promptColor := mutedTextColor
	if m.aiPromptInput.Focused() {
		ruleColor = accentColor
		ruleChar = "━"
		promptColor = accentColor
	}

	title := "Agent"
	if m.agentBusy {
		title += " working..."
	}
	lead := strings.Repeat(ruleChar, 2) + " "
	fill := strings.Repeat(ruleChar, max(m.aiChatRect.w-lipgloss.Width(lead+title)-1, 0))
	rule := lipgloss.NewStyle().Foreground(ruleColor).Render
	rows := []string{rule(lead) + lipgloss.NewStyle().Foreground(mutedTextColor).Render(title) + rule(" "+fill)}

	lines := m.aiChatLines(max(m.aiChatRect.w-4, 1))
	visible := m.aiChatVisibleLines()
	offset := clampScroll(m.aiChatScroll, len(lines), visible)
	for _, line := range lines[offset:min(offset+visible, len(lines))] {
		rows = append(rows, "  "+line)
	}

	return append(rows, "  "+lipgloss.NewStyle().Foreground(promptColor).Render("› ")+m.aiPromptInput.View())
}

func (m model) renderInspectorLines() []string {
	lines := []string{}
	if node, ok := m.selectedEditorNode(); ok {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(mutedTextColor).Render("Selected"),
			lipgloss.NewStyle().Bold(true).Foreground(textColor).Render(node.label),
			fmt.Sprintf("Type: %s", node.kind),
		)
		if m.editorMode == editorModeConnect && m.connectSource >= 0 {
			lines = append(lines, "", lipgloss.NewStyle().Foreground(accentColor).Render("Connect mode: choose target, enter to connect"))
		}
	} else {
		lines = append(lines, "No node selected.")
	}
	lines = append(lines, "", "Drag with mouse to move", "Enter edit  C connect", "A add node  Esc back", "/ chat with the agent")
	return lines
}

func (m model) paletteWidth() int {
	return min(64, max(m.width-8, 28))
}

func (m model) renderWorkflowCommandPalette() string {
	items := m.editorMenuItems()
	title, placeholder := "Commands", "Type to search commands, nodes and node types"
	switch m.editorOverlay {
	case editorOverlayCreate:
		title, placeholder = "Add a node", "Type to search node types"
	case editorOverlayGoTo:
		title, placeholder = "Go to a node", "Type a name"
	}

	width := m.paletteWidth()
	inner := width - 4
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)

	search := lipgloss.NewStyle().Foreground(accentColor).Render("› ")
	if m.paletteQuery == "" {
		search += muted.Render(placeholder)
	} else {
		search += lipgloss.NewStyle().Foreground(textColor).Render(m.paletteQuery) + lipgloss.NewStyle().Foreground(accentColor).Render("▏")
	}
	start, end := m.paletteWindow(len(items))
	if count := len(items); count > end-start && count > 0 {
		search = padRightBlank(search, max(inner-12, 1)) + muted.Render(fmt.Sprintf("%d of %d", m.editorMenuSelected+1, count))
	}

	lines := []string{search, ""}
	if len(items) == 0 {
		lines = append(lines, muted.Render("  Nothing matches"))
	}
	for i := start; i < end; i++ {
		lines = append(lines, paletteRow(items[i], inner, i == m.editorMenuSelected))
	}
	lines = append(lines, "", muted.Render("↑↓ choose  ⏎ run  esc close"))

	height := m.paletteHeight(len(items))
	palette := renderSectionPanel(title, lines, width, height)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, palette)
}

// paletteRow is an item with its key, or its type, on the right.
func paletteRow(item editorMenuItem, width int, selected bool) string {
	shortcut := item.shortcut
	room := max(width-lipgloss.Width(shortcut)-3, 8)
	row := renderListRow("›", accentColor, truncatePlain(item.label, room-5), room, selected)
	if shortcut == "" {
		return row
	}
	style := lipgloss.NewStyle().Foreground(mutedTextColor)
	if selected {
		style = style.Background(neutralColor)
	}
	gap := max(width-lipgloss.Width(row)-lipgloss.Width(shortcut), 1)
	pad := lipgloss.NewStyle()
	if selected {
		pad = pad.Background(neutralColor)
	}
	return row + pad.Render(blank(gap)) + style.Render(shortcut)
}

func (m model) paletteHeight(items int) int {
	return min(max(items+paletteChrome, 10), max(m.height-4, 8))
}

// paletteWindow says which items fit in the palette, keeping the selected one in
// view. Rendering and mouse handling both read it.
func (m model) paletteWindow(items int) (int, int) {
	rows := max(m.paletteHeight(items)-paletteChrome, 1)
	if items <= rows {
		return 0, items
	}
	start := clamp(m.editorMenuSelected-rows/2, 0, items-rows)
	return start, start + rows
}

func (m model) handleWorkflowEditorClick(mouse tea.Mouse) (model, tea.Cmd) {
	x, y := mouse.X, mouse.Y
	if m.editorOverlay == editorOverlayResult {
		m.closeEditorOverlay()
		return m, nil
	}
	if m.editorOverlay == editorOverlayHelp || m.editorOverlay == editorOverlayVersions || m.editorOverlay == editorOverlayPublish {
		m.closeEditorOverlay()
		m.publishInput.Blur()
		return m, nil
	}
	if m.terminalOpen && inside(x, y, m.terminalRect) {
		m.terminalFocus = m.terminal != nil
		return m, nil
	}
	m.terminalFocus = false
	if m.editorOverlay != editorOverlayNone {
		cmd := m.handleEditorOverlayClick(x, y)
		return m, cmd
	}

	if inside(x, y, m.workflowSaveButtonRect()) {
		return m, m.saveCurrentWorkflow()
	}
	if m.editorMode != editorModeModal && inside(x, y, m.workflowRunButtonRect()) {
		return m, m.runCurrentWorkflow()
	}

	if m.editorMode == editorModeModal {
		return m.handleNodeFormClick(x, y)
	}

	if inside(x, y, m.aiPromptInputRect) {
		m.aiPromptInput.Focus()
		return m, nil
	}
	if inside(x, y, m.aiChatRect) {
		m.aiPromptInput.Blur()
		return m, nil
	}

	if m.editorMode == editorModeAdd && inside(x, y, m.canvasRect) {
		m.editorMode = editorModeNormal
	}

	if inside(x, y, m.paletteRect) {
		row := y - m.paletteRect.y - paletteHeader
		if row >= m.paletteListRows() {
			m.scrollPalette(m.paletteListRows())
			return m, nil
		}
		if kind, ok := m.paletteNodeKindAtRow(row + m.paletteOffset()); ok {
			m.addEditorNode(kind)
			m.editorMode = editorModeNormal
		}
		return m, nil
	}

	additive := mouse.Mod.Contains(tea.ModCtrl) || mouse.Mod.Contains(tea.ModShift)

	if m.editorMode != editorModeConnect && !additive {
		if node, output, ok := m.outputPortAt(x, y); ok {
			m.selectOnly(node)
			m.beginConnection(node, output)
			m.wireActive = true
			m.wireX, m.wireY = x, y
			return m, nil
		}
	}

	if i := m.nodeIndexAt(x, y); i >= 0 && !m.wireOverComment(i, x, y) {
		if m.editorMode == editorModeConnect {
			m.selectOnly(i)
			m.finishEditorConnection()
			return m, nil
		}
		if additive {
			m.toggleNodeSelection(i)
			return m, nil
		}
		if !m.isNodeSelected(i) {
			m.selectOnly(i)
		}
		m.beginNodeDrag(i, x, y)
		return m, nil
	}

	if inside(x, y, m.canvasRect) {
		connecting := m.editorMode == editorModeConnect
		m.cancelConnection()
		if i := m.connectionAt(x, y); i >= 0 && !additive && !connecting {
			m.selectConnection(i)
			return m, nil
		}
		if !additive {
			m.clearSelection()
		}
		m.beginMarquee(x, y, additive)
	}
	return m, nil
}

func (m *model) handleWorkflowEditorWheel(mouse tea.Mouse) {
	if m.terminalOpen && m.editorOverlay == editorOverlayNone && inside(mouse.X, mouse.Y, m.terminalRect) {
		switch mouse.Button {
		case tea.MouseWheelUp:
			m.scrollTerminal(terminalWheelRows)
		case tea.MouseWheelDown:
			m.scrollTerminal(-terminalWheelRows)
		}
		return
	}
	if m.editorOverlay == editorOverlayResult {
		if mouse.Button == tea.MouseWheelUp {
			m.scrollExecutionResult(-1)
		} else {
			m.scrollExecutionResult(1)
		}
		return
	}
	if inside(mouse.X, mouse.Y, m.canvasRect) {
		m.handleCanvasWheel(mouse)
		return
	}
	if inside(mouse.X, mouse.Y, m.paletteRect) {
		switch mouse.Button {
		case tea.MouseWheelUp:
			m.scrollPalette(-wheelRows)
		case tea.MouseWheelDown:
			m.scrollPalette(wheelRows)
		}
		return
	}
	if !inside(mouse.X, mouse.Y, m.aiChatRect) {
		return
	}

	switch mouse.Button {
	case tea.MouseWheelUp:
		m.scrollAIChat(-1)
	case tea.MouseWheelDown:
		m.scrollAIChat(1)
	}
}

func (m model) updateAIBuilderInput(msg tea.KeyPressMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.closeAIChat(), nil
	case "pgup", "pgdown":
		page := max(m.aiChatVisibleLines()-1, 1)
		if msg.String() == "pgup" {
			page = -page
		}
		m.aiChatScroll = clampScroll(m.aiChatScroll+page, m.aiChatLineCount(), m.aiChatVisibleLines())
		return m, nil
	case "tab":
		return m.cycleEditorPanel(1)
	case "shift+tab":
		return m.cycleEditorPanel(-1)
	}
	if msg.Code == tea.KeyEnter {
		return m.submitAIBuilderPrompt()
	}

	var cmd tea.Cmd
	m.aiPromptInput, cmd = m.aiPromptInput.Update(msg)
	return m, cmd
}

func (m *model) openWorkflowContextMenu(x, y int) {
	if m.editorOverlay == editorOverlayResult {
		return
	}
	if m.editorMode == editorModeModal {
		return
	}
	m.draggingNode = -1
	m.editorOverlay = editorOverlayContext
	m.editorMenuSelected = 0
	m.editorMenuTarget = -1
	m.editorCreateAtMenu = true

	if i := m.nodeIndexAt(x, y); i >= 0 {
		// A right click inside a selection acts on all of it, as on the web.
		if !m.isNodeSelected(i) {
			m.selectOnly(i)
		}
		m.editorMenuTarget = i
	} else if i := m.connectionAt(x, y); i >= 0 {
		m.selectConnection(i)
	}
	if m.editorMenuTarget < 0 && !inside(x, y, m.canvasRect) {
		m.closeEditorOverlay()
		return
	}
	m.editorMenuSelected = firstMenuItem(m.editorMenuItems())

	contentX := x - m.canvasRect.x - 2
	contentY := y - m.canvasRect.y - 2
	menuWidth, menuHeight := m.contextMenuSize()
	m.editorMenuX = clamp(contentX, 0, max(m.canvasRect.w-4-menuWidth, 0))
	m.editorMenuY = clamp(contentY, 0, max(m.canvasRect.h-3-menuHeight, 0))
}

func (m *model) openWorkflowCommandPalette() {
	m.draggingNode = -1
	m.editorOverlay = editorOverlayCommand
	m.paletteQuery = ""
	m.editorMenuSelected = 0
	m.editorMenuTarget = m.selectedNode
	m.editorCreateAtMenu = false
}

func (m model) updateEditorOverlayKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	items := m.editorMenuItems()
	switch msg.String() {
	case "esc":
		m.closeEditorOverlay()
	case "up":
		m.editorMenuSelected = nextMenuItem(items, m.editorMenuSelected, -1)
	case "down":
		m.editorMenuSelected = nextMenuItem(items, m.editorMenuSelected, 1)
	}
	if msg.Code == tea.KeyEnter && len(items) > 0 {
		cmd := m.runEditorAction(items[m.editorMenuSelected])
		return m, cmd
	}
	return m, nil
}

func (m *model) handleEditorOverlayClick(x, y int) tea.Cmd {
	menuRect := m.editorMenuRect()
	if !inside(x, y, menuRect) {
		m.closeEditorOverlay()
		return nil
	}

	row := y - menuRect.y - 1
	items := m.editorMenuItems()
	if m.isPalette() {
		row = y - menuRect.y - 4
		start, end := m.paletteWindow(len(items))
		if row < 0 || row >= end-start {
			return nil
		}
		row += start
	}
	if row < 0 || row >= len(items) {
		return nil
	}
	m.editorMenuSelected = row
	return m.runEditorAction(items[row])
}

// isPalette is an overlay in the middle of the screen with a search: commands, node
// types to add, or nodes to go to.
func (m model) isPalette() bool {
	switch m.editorOverlay {
	case editorOverlayCommand, editorOverlayCreate, editorOverlayGoTo:
		return true
	}
	return false
}

func (m *model) closeEditorOverlay() {
	m.editorOverlay = editorOverlayNone
	m.paletteQuery = ""
	m.editorMenuSelected = 0
	m.editorMenuTarget = -1
	m.editorCreateAtMenu = false
}

func (m model) editorMenuItems() []editorMenuItem {
	if m.editorOverlay == editorOverlayContext {
		return m.contextMenuItems()
	}
	return m.paletteItems()
}

func (m model) editorMenuRect() rect {
	if m.isPalette() {
		items := m.editorMenuItems()
		width := m.paletteWidth()
		height := m.paletteHeight(len(items))
		return rect{x: max((m.width-width)/2, 0), y: max((m.height-height)/2, 0), w: width, h: height}
	}

	width, height := m.contextMenuSize()
	return rect{
		x: m.canvasRect.x + 2 + m.editorMenuX,
		y: m.canvasRect.y + 2 + m.editorMenuY,
		w: width,
		h: height,
	}
}

func (m model) contextMenuSize() (int, int) {
	width := 0
	for _, item := range m.editorMenuItems() {
		width = max(width, len([]rune(item.label))+len([]rune(item.shortcut))+8)
	}
	return max(width, 22), len(m.editorMenuItems()) + 2
}

func (m model) editorCreateMenuItems() []editorMenuItem {
	nodes := m.getPaletteNodes()
	items := make([]editorMenuItem, 0, len(nodes))
	for _, def := range nodes {
		items = append(items, editorMenuItem{
			label:  "Add " + def.Title,
			action: editorActionCreate,
			kind:   def.Type,
		})
	}
	return items
}

func (m *model) runEditorAction(item editorMenuItem) tea.Cmd {
	if !item.selectable() {
		return nil
	}

	switch item.action {
	case editorActionUndo:
		m.closeEditorOverlay()
		return m.undoStep()
	case editorActionRedo:
		m.closeEditorOverlay()
		return m.redoStep()
	case editorActionCopy:
		m.closeEditorOverlay()
		m.copySelection()
		m.pasteCount = 0
	case editorActionPaste:
		if m.editorOverlay == editorOverlayContext {
			x, y := m.toWorld(m.editorMenuX, m.editorMenuY)
			m.closeEditorOverlay()
			m.pasteAt(x, y)
			return nil
		}
		m.closeEditorOverlay()
		m.pasteNearOriginal()
	case editorActionOpenCreate:
		m.editorOverlay = editorOverlayCreate
		m.paletteQuery = ""
		m.editorMenuSelected = 0
	case editorActionCreate:
		kind := fallbackString(item.kind, "Evaluate Code")
		if m.editorCreateAtMenu {
			x, y := m.toWorld(m.editorMenuX, m.editorMenuY)
			m.addEditorNodeAt(kind, x, y)
		} else {
			m.addEditorNode(kind)
		}
		m.closeEditorOverlay()
	case editorActionDelete:
		m.closeEditorOverlay()
		m.deleteSelection()
	case editorActionAddComment:
		kind := fallbackString(item.kind, commentNote)
		x, y := m.newCommentSpot()
		if m.editorOverlay == editorOverlayContext {
			x, y = m.toWorld(m.editorMenuX, m.editorMenuY)
		}
		m.closeEditorOverlay()
		m.addComment(kind, x, y)
	case editorActionEdit:
		m.closeEditorOverlay()
		m.openNodeModal()
	case editorActionConnect:
		m.closeEditorOverlay()
		m.beginConnectionFromSelected()
	case editorActionBack:
		m.closeEditorOverlay()
		m.screen = screenApp
	case editorActionRun:
		m.closeEditorOverlay()
		return m.runCurrentWorkflow()
	case editorActionSave:
		m.closeEditorOverlay()
		return m.saveCurrentWorkflow()
	case editorActionResult:
		m.closeEditorOverlay()
		m.openExecutionResult()
	case editorActionFit:
		m.closeEditorOverlay()
		m.fitView()
	case editorActionZoomIn:
		m.closeEditorOverlay()
		m.zoomCentered(m.zoomIndex + 1)
	case editorActionZoomOut:
		m.closeEditorOverlay()
		m.zoomCentered(m.zoomIndex - 1)
	case editorActionZoomReset:
		m.closeEditorOverlay()
		m.zoomCentered(defaultZoomIndex)
	case editorActionSelectAll:
		m.closeEditorOverlay()
		m.selectAllNodes()
	case editorActionWires:
		m.closeEditorOverlay()
		m.walkWires()
	case editorActionGoTo:
		if item.kind == "" {
			m.openGoTo()
			return nil
		}
		m.closeEditorOverlay()
		m.goToNode(item.kind)
	case editorActionHelp:
		m.openHelp()
	case editorActionAgent:
		m.closeEditorOverlay()
		next, cmd := m.openAIChat()
		*m = next
		return cmd
	case editorActionPublish:
		m.openPublish()
	case editorActionVersions:
		return m.openVersions()
	case editorActionTerminal:
		next, cmd := m.openTerminal()
		*m = next
		return cmd
	case editorActionTerminalWide:
		m.closeEditorOverlay()
		m.terminalWide = !m.terminalWide
		m.reflow()
	case editorActionTerminalClose:
		m.closeEditorOverlay()
		m.closeTerminal()
	}
	return nil
}

func (m *model) handleWorkflowEditorMotion(mouse tea.Mouse) {
	switch {
	case m.wireActive:
		m.wireX, m.wireY = mouse.X, mouse.Y
	case m.panning:
		m.panBy(scaled(m.panLastX-mouse.X, 1/m.zoom()), scaled(m.panLastY-mouse.Y, 1/m.zoom()))
		m.panLastX, m.panLastY = mouse.X, mouse.Y
	case m.marqueeActive:
		m.marqueeX, m.marqueeY = mouse.X, mouse.Y
	case m.draggingNode >= 0 && m.draggingNode < len(m.editorNodes):
		m.dragNodes(mouse.X, mouse.Y)
	}
}

// moveSelectedEditorNode moves the selection, or scrolls when nothing is selected.
func (m *model) moveSelectedEditorNode(dx, dy int) {
	indices := m.selectedIndices()
	if len(indices) == 0 {
		m.panBy(dx*wheelColumns, dy*wheelRows)
		return
	}
	m.pushUndo()
	for _, index := range indices {
		m.editorNodes[index].x += dx
		m.editorNodes[index].y += dy
	}
	m.rebuildNodeRects()
	m.markEditorDirty()
}

// addEditorNode continues from the selected node when there is one: the new node
// goes after it, already connected.
func (m *model) addEditorNode(kind string) {
	m.pushUndo()

	width, height := m.nodeSize(kind, m.nodeTitleForKind(kind))
	if !m.hasSelectedEditorNode() {
		content := m.canvasContentRect()
		x, y := m.toWorld(content.w/2-width/2, content.h/2-height/2)
		m.insertNode(kind, x, y)
		return
	}

	source := m.editorNodes[m.selectedNode]
	output := m.firstFreeOutput(source)
	x, y := m.nextNodeSpot(source, width, height)
	m.insertNode(kind, x, y)
	m.revealNode(m.selectedNode)

	inputs := m.nodeInputs(kind)
	if output < 0 || len(inputs) == 0 {
		return
	}
	m.editorConnections = append(m.editorConnections, editorConnection{
		source:       source.id,
		sourceOutput: m.nodeOutputs(source.kind)[output],
		target:       m.editorNodes[m.selectedNode].id,
		targetInput:  inputs[0],
	})
}

func (m *model) addEditorNodeAt(kind string, x, y int) {
	m.pushUndo()
	m.insertNode(kind, x, y)
}

func (m *model) insertNode(kind string, x, y int) {
	title := m.nodeTitleForKind(kind)
	width, height := m.nodeSize(kind, title)
	node := editorNode{
		id:       m.nextNodeID(),
		kind:     kind,
		label:    title,
		w:        width,
		h:        height,
		controls: m.nodeControls(kind),
	}
	node.x, node.y = x, y
	m.editorNodes = append(m.editorNodes, node)
	m.selectOnly(len(m.editorNodes) - 1)
	m.rebuildNodeRects()
	m.markEditorDirty()
}

// deleteSelectedEditorNode removes the selection, except a Start node, which every
// flow needs.
func (m *model) deleteSelectedEditorNode() {
	deleted := map[string]bool{}
	for _, index := range m.selectedIndices() {
		if node := m.editorNodes[index]; !isStartKind(node.kind) {
			deleted[node.id] = true
		}
	}
	if len(deleted) == 0 {
		return
	}
	m.pushUndo()

	nodes := make([]editorNode, 0, len(m.editorNodes))
	for _, node := range m.editorNodes {
		if !deleted[node.id] {
			nodes = append(nodes, node)
		}
	}
	m.editorNodes = nodes

	connections := make([]editorConnection, 0, len(m.editorConnections))
	for _, conn := range m.editorConnections {
		if !deleted[conn.source] && !deleted[conn.target] {
			connections = append(connections, conn)
		}
	}
	m.editorConnections = connections

	m.clearSelection()
	m.rebuildNodeRects()
	m.markEditorDirty()
}

func (m *model) selectNextNode(delta int) {
	if len(m.editorNodes) == 0 {
		m.clearSelection()
		return
	}
	m.selectOnly((m.selectedNode + delta + len(m.editorNodes)) % len(m.editorNodes))
	m.revealNode(m.selectedNode)
}

func (m model) hasSelectedEditorNode() bool {
	return m.selectedNode >= 0 && m.selectedNode < len(m.editorNodes)
}

func (m model) selectedEditorNode() (editorNode, bool) {
	if !m.hasSelectedEditorNode() {
		return editorNode{}, false
	}
	return m.editorNodes[m.selectedNode], true
}

func (m *model) rebuildNodeRects() {
	if m.nodeRects == nil {
		m.nodeRects = map[string]rect{}
	}
	for key := range m.nodeRects {
		delete(m.nodeRects, key)
	}
	content := m.canvasContentRect()
	for _, node := range m.editorNodes {
		projected := m.projectNode(node)
		m.nodeRects[node.id] = rect{x: content.x + projected.x, y: content.y + projected.y, w: projected.w, h: projected.h}
	}
}

type canvasCell struct {
	ch    rune
	style *lipgloss.Style
}

func newCanvasGrid(w, h int) [][]canvasCell {
	grid := make([][]canvasCell, h)
	for y := range grid {
		grid[y] = make([]canvasCell, w)
		for x := range grid[y] {
			grid[y][x] = canvasCell{ch: ' '}
		}
	}
	return grid
}

func drawCanvasDots(grid [][]canvasCell) {
	style := lipgloss.NewStyle().Foreground(canvasDotColor)
	for y := 0; y < len(grid); y += 2 {
		for x := 0; x < len(grid[y]); x += 4 {
			drawStyledRune(grid, x, y, '·', &style)
		}
	}
}

func (m model) drawNode(grid [][]canvasCell, index int, node editorNode) {
	if node.w < 4 || node.h < 3 {
		return
	}

	def, _ := m.nodeDefinition(node.kind)
	connecting := m.editorMode == editorModeConnect
	highlighted := m.isNodeSelected(index)
	if connecting {
		highlighted = index == m.connectSource
	}

	border := lipgloss.NewStyle().Foreground(panelBorder)
	fill := lipgloss.NewStyle().Foreground(textColor)
	other, byOther := m.selectedBy(node.id)
	switch {
	case connecting && index == m.wireTarget():
		border = border.Foreground(successColor)
		fill = fill.Bold(true)
	case highlighted:
		border = border.Foreground(focusedBorderColor)
		fill = fill.Bold(true)
	case byOther:
		border = border.Foreground(personColor(other))
	}

	drawStyledRune(grid, node.x, node.y, '╭', &border)
	drawStyledRune(grid, node.x+node.w-1, node.y, '╮', &border)
	drawStyledRune(grid, node.x, node.y+node.h-1, '╰', &border)
	drawStyledRune(grid, node.x+node.w-1, node.y+node.h-1, '╯', &border)
	for x := node.x + 1; x < node.x+node.w-1; x++ {
		drawStyledRune(grid, x, node.y, '─', &border)
		drawStyledRune(grid, x, node.y+node.h-1, '─', &border)
	}
	for y := node.y + 1; y < node.y+node.h-1; y++ {
		drawStyledRune(grid, node.x, y, '│', &border)
		drawStyledRune(grid, node.x+node.w-1, y, '│', &border)
		for x := node.x + 1; x < node.x+node.w-1; x++ {
			drawStyledRune(grid, x, y, ' ', &fill)
		}
	}

	iconStyle := fill.Foreground(categoryColor(def.Category))
	for i, r := range []rune(mapIconToTerminal(def.Icon)) {
		drawStyledRune(grid, node.x+2+i, node.y+1, r, &iconStyle)
	}
	label := []rune(node.label)
	if room := node.w - 6; len(label) > room {
		label = []rune(truncatePlain(node.label, room))
		if m.zoom() < 1 {
			label = []rune(node.label)[:max(room, 0)]
		}
	}
	for i, r := range label {
		drawStyledRune(grid, node.x+4+i, node.y+1, r, &fill)
	}

	m.drawOutputPorts(grid, index, node)

	// Who else has it selected, on its top edge.
	if byOther {
		tag := []rune(" " + truncatePlain(other.Name, max(node.w-6, 1)) + " ")
		style := lipgloss.NewStyle().Foreground(personColor(other))
		for i, r := range tag {
			if node.x+2+i < node.x+node.w-1 {
				drawStyledRune(grid, node.x+2+i, node.y, r, &style)
			}
		}
	}
}

// drawOutputPorts puts a handle on the right edge for each output: filled when
// something is attached, hollow when it's free to connect.
func (m model) drawOutputPorts(grid [][]canvasCell, index int, node editorNode) {
	outputs := m.nodeOutputs(node.kind)
	label := lipgloss.NewStyle().Foreground(mutedTextColor)

	for i, output := range outputs {
		y := outputRow(node, i, len(outputs))

		port, glyph := lipgloss.NewStyle().Foreground(mutedTextColor), '○'
		if m.outputConnected(node, output) {
			port, glyph = port.Foreground(textColor), '●'
		}
		if m.editorMode == editorModeConnect && index == m.connectSource && i == m.connectOutput {
			port, glyph = port.Foreground(accentColor), '●'
		}
		drawStyledRune(grid, node.x+node.w-1, y, glyph, &port)

		if len(outputs) > 1 && m.zoom() >= 1 {
			runes := []rune(output)
			for j, r := range runes {
				drawStyledRune(grid, node.x+node.w-2-len(runes)+j, y, r, &label)
			}
		}
	}
}

func editorNodeWidth(label string) int {
	return clamp(len([]rune(label))+6, 14, 24)
}

func (m model) drawContextMenu(grid [][]canvasCell) {
	items := m.editorMenuItems()
	if len(items) == 0 {
		return
	}
	width, height := m.contextMenuSize()
	x := m.editorMenuX
	y := m.editorMenuY

	border := lipgloss.NewStyle().Foreground(panelBorder)
	fill := lipgloss.NewStyle().Background(neutralColor).Foreground(textColor)
	active := fill.Foreground(accentColor).Bold(true)

	drawStyledRune(grid, x, y, '╭', &border)
	drawStyledRune(grid, x+width-1, y, '╮', &border)
	drawStyledRune(grid, x, y+height-1, '╰', &border)
	drawStyledRune(grid, x+width-1, y+height-1, '╯', &border)
	for col := x + 1; col < x+width-1; col++ {
		drawStyledRune(grid, col, y, '─', &border)
		drawStyledRune(grid, col, y+height-1, '─', &border)
	}
	for row := y + 1; row < y+height-1; row++ {
		drawStyledRune(grid, x, row, '│', &border)
		drawStyledRune(grid, x+width-1, row, '│', &border)
		for col := x + 1; col < x+width-1; col++ {
			drawStyledRune(grid, col, row, ' ', &fill)
		}
	}
	hint := fill.Foreground(mutedTextColor)
	disabled := fill.Foreground(panelBorder)
	danger := fill.Foreground(errorColor)
	line := fill.Foreground(panelBorder)

	for i, item := range items {
		row := y + 1 + i
		if item.separator {
			for col := x + 1; col < x+width-1; col++ {
				drawStyledRune(grid, col, row, '─', &line)
			}
			continue
		}

		prefix, style, shortcut := "  ", &fill, &hint
		switch {
		case item.disabled:
			style, shortcut = &disabled, &disabled
		case i == m.editorMenuSelected:
			prefix, style = "› ", &active
		case item.danger:
			style = &danger
		}

		for j, r := range []rune(prefix + item.label) {
			drawStyledRune(grid, x+1+j, row, r, style)
		}
		keys := []rune(item.shortcut)
		for j, r := range keys {
			drawStyledRune(grid, x+width-2-len(keys)+j, row, r, shortcut)
		}
	}
}

func drawRune(grid [][]canvasCell, x, y int, r rune) {
	drawStyledRune(grid, x, y, r, nil)
}

func drawStyledRune(grid [][]canvasCell, x, y int, r rune, style *lipgloss.Style) {
	if y < 0 || y >= len(grid) || x < 0 || len(grid) == 0 || x >= len(grid[y]) {
		return
	}
	grid[y][x] = canvasCell{ch: r, style: style}
}

func drawText(grid [][]canvasCell, x, y int, text string) {
	for i, r := range []rune(text) {
		drawRune(grid, x+i, y, r)
	}
}

func (n editorNode) savedPosition() apiPosition {
	if n.loaded && n.loadedAt == (canvasPoint{n.x, n.y}) {
		return n.loadedPos
	}
	return apiPosition{X: float64(n.x * pixelsPerColumn), Y: float64(n.y * pixelsPerRow)}
}

func (n editorNode) controlsMap() map[string]any {
	out := make(map[string]any, len(n.controls))
	for _, control := range n.controls {
		if strings.TrimSpace(control.name) == "" {
			continue
		}
		out[control.name] = control.value
	}
	return out
}

func apiRefString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return fmt.Sprintf("%.0f", v)
	case int:
		return fmt.Sprintf("%d", v)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func (m model) normalizeNodeKind(kind string) string {
	trimmed := strings.TrimSpace(kind)
	if trimmed == "" {
		return trimmed
	}
	for _, def := range m.getPaletteNodes() {
		if strings.EqualFold(def.Type, trimmed) || strings.EqualFold(def.Title, trimmed) {
			return fallbackString(def.Type, def.Title)
		}
		if slugifyNodeToken(def.Type) == slugifyNodeToken(trimmed) || slugifyNodeToken(def.Title) == slugifyNodeToken(trimmed) {
			return fallbackString(def.Type, def.Title)
		}
	}
	return trimmed
}

func (m model) nodeTitleForKind(kind string) string {
	trimmed := strings.TrimSpace(kind)
	if trimmed == "" {
		return ""
	}
	for _, def := range m.getPaletteNodes() {
		if strings.EqualFold(def.Type, trimmed) || strings.EqualFold(def.Title, trimmed) {
			return fallbackString(def.Title, trimmed)
		}
		if slugifyNodeToken(def.Type) == slugifyNodeToken(trimmed) || slugifyNodeToken(def.Title) == slugifyNodeToken(trimmed) {
			return fallbackString(def.Title, trimmed)
		}
	}
	return trimmed
}

func (m model) controlsFromAPI(kind, label string, raw map[string]any) []nodeControl {
	_, known := m.nodeDefinition(kind)
	controls := append([]nodeControl(nil), m.nodeControls(kind)...)
	if len(controls) == 0 {
		controls = []nodeControl{{name: "label", label: "Label", value: label}}
	}

	indexByName := make(map[string]int, len(controls))
	for i, control := range controls {
		indexByName[control.name] = i
	}
	if label != "" {
		if idx, ok := indexByName["label"]; ok {
			controls[idx].value = label
		}
	}

	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := controlValue(raw[key])
		if idx, ok := indexByName[key]; ok {
			controls[idx].value = value
			continue
		}
		controls = append(controls, nodeControl{
			name:   key,
			label:  titleizeControlName(key),
			value:  value,
			hidden: known,
		})
	}

	return controls
}

func slugifyNodeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", "\\", "-")
	return replacer.Replace(value)
}

func isStartKind(kind string) bool {
	slug := slugifyNodeToken(kind)
	return slug == "start"
}

func titleizeControlName(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	parts := strings.Fields(value)
	for i, part := range parts {
		runes := []rune(strings.ToLower(part))
		if len(runes) == 0 {
			continue
		}
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

func defaultControls(kind string) []nodeControl {
	switch slugifyNodeToken(kind) {
	case "evaluate-code":
		return []nodeControl{
			{name: "label", label: "Label", value: "Evaluate Code"},
			{name: "language", label: "Language", value: "python"},
			{name: "code", label: "Code", value: `set_result(variable("x"))`},
		}
	case "variable":
		return []nodeControl{
			{name: "label", label: "Label", value: "Variable"},
			{name: "var_name", label: "Variable Name", value: "x"},
			{name: "var_type", label: "Type", value: "Integer"},
			{name: "var_value", label: "Value", value: "1"},
		}
	case "output":
		return []nodeControl{
			{name: "label", label: "Label", value: "Output"},
			{name: "status", label: "Status", value: "success"},
		}
	default:
		return []nodeControl{
			{name: "label", label: "Label", value: kind},
		}
	}
}
