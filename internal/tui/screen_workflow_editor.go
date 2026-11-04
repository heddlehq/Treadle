package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *model) beginNewWorkflowEditor() {
	m.beginWorkflowEditor(defaultWorkflowName)
}

func (m *model) beginWorkflowEditor(name string) {
	m.screen = screenWorkflowEditor
	m.editorWorkflowID = 0
	m.editorWorkflowName = fallbackString(name, defaultWorkflowName)
	m.editorDirty = false
	m.editorSaving = false
	m.editorStatusMessage = ""
	m.editorMode = editorModeNormal
	m.selectedNode = 0
	m.draggingNode = -1
	m.connectSource = -1
	m.paletteIndex = 0
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
	m.editorConnections = []editorConnection{{source: "start", target: "output"}}
	m.rebuildNodeRects()
}

func (m *model) beginWorkflowEditorFromAPI(flow apiFlow) {
	m.beginWorkflowEditor(fallbackString(flow.Name, defaultWorkflowName))
	m.editorWorkflowID = flow.ID
	if len(flow.Nodes) == 0 {
		return
	}

	editorNodes, editorConnections := m.mapAPIFlowToEditor(flow)
	if len(editorNodes) == 0 {
		return
	}

	m.editorNodes = editorNodes
	m.editorConnections = editorConnections
	m.selectedNode = 0
	m.clampEditorNodes()
	m.rebuildNodeRects()
}

func (m *model) markEditorDirty() {
	m.editorDirty = true
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
	type rawNode struct {
		node editorNode
		x    float64
		y    float64
	}

	rawNodes := make([]rawNode, 0, len(flow.Nodes))
	minX, maxX := 0.0, 0.0
	minY, maxY := 0.0, 0.0
	for i, item := range flow.Nodes {
		kind := m.normalizeNodeKind(item.Type)
		label := fallbackString(item.Label, m.nodeTitleForKind(kind))
		controls := m.controlsFromAPI(kind, label, item.Controls)
		node := editorNode{
			id:       apiRefString(item.ID),
			kind:     kind,
			label:    label,
			w:        editorNodeWidth(label),
			h:        3,
			controls: controls,
		}
		if node.id == "" {
			node.id = fmt.Sprintf("node_%d", i+1)
		}
		rawNodes = append(rawNodes, rawNode{node: node, x: item.Position.X, y: item.Position.Y})
		if i == 0 || item.Position.X < minX {
			minX = item.Position.X
		}
		if i == 0 || item.Position.X > maxX {
			maxX = item.Position.X
		}
		if i == 0 || item.Position.Y < minY {
			minY = item.Position.Y
		}
		if i == 0 || item.Position.Y > maxY {
			maxY = item.Position.Y
		}
	}

	maxNodeWidth := 0
	for _, item := range rawNodes {
		if item.node.w > maxNodeWidth {
			maxNodeWidth = item.node.w
		}
	}
	availableX := max(m.canvasRect.w-4-maxNodeWidth, 0)
	availableY := max(m.canvasRect.h-3-3, 0)
	spanX := maxX - minX
	spanY := maxY - minY
	scaleX := 1.0
	scaleY := 1.0
	if spanX > 0 && float64(availableX) < spanX {
		scaleX = float64(availableX) / spanX
	}
	if spanY > 0 && float64(availableY) < spanY {
		scaleY = float64(availableY) / spanY
	}

	editorNodes := make([]editorNode, 0, len(rawNodes))
	for _, item := range rawNodes {
		node := item.node
		if spanX > 0 {
			node.x = int(math.Round((item.x - minX) * scaleX))
		}
		if spanY > 0 {
			node.y = int(math.Round((item.y - minY) * scaleY))
		}
		editorNodes = append(editorNodes, node)
	}

	editorConnections := make([]editorConnection, 0, len(flow.Connections))
	for _, conn := range flow.Connections {
		source := apiRefString(conn.Source)
		target := apiRefString(conn.Target)
		if source == "" || target == "" {
			continue
		}
		editorConnections = append(editorConnections, editorConnection{source: source, target: target})
	}

	return editorNodes, editorConnections
}

func (m *model) saveCurrentWorkflow() tea.Cmd {
	if m.editorSaving {
		return nil
	}
	if m.editorWorkflowID == 0 {
		m.editorStatusMessage = "Save unavailable for unsynced workflow"
		return nil
	}
	if strings.TrimSpace(m.activeHost) == "" {
		m.editorStatusMessage = "Missing server connection"
		return nil
	}
	if !m.editorDirty {
		m.editorStatusMessage = "All changes saved"
		return nil
	}

	m.editorSaving = true
	m.editorStatusMessage = "Saving changes..."
	return saveWorkflowCmd(m.activeHost, m.activeAPIKey, m.editorWorkflowID, m.workflowRequestPayload())
}

func (m model) workflowRequestPayload() workflowRequest {
	nodes := make([]apiNode, 0, len(m.editorNodes))
	for _, node := range m.editorNodes {
		nodes = append(nodes, apiNode{
			ID:       node.id,
			Type:     slugifyNodeToken(node.kind),
			Label:    node.label,
			Position: apiPosition{X: float64(node.x), Y: float64(node.y)},
			Controls: node.controlsMap(),
		})
	}

	connections := make([]apiSaveConnection, 0, len(m.editorConnections))
	for _, conn := range m.editorConnections {
		connections = append(connections, apiSaveConnection{
			Source:       conn.source,
			SourceOutput: "output",
			Target:       conn.target,
			TargetInput:  "input",
		})
	}

	return workflowRequest{
		Workflow: workflowPayload{
			Name:        m.editorWorkflowName,
			Nodes:       nodes,
			Connections: connections,
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
			return m.handleWorkflowEditorClick(mouse.X, mouse.Y)
		case tea.MouseRight:
			m.openWorkflowContextMenu(mouse.X, mouse.Y)
		}
	case tea.MouseMotionMsg:
		m.handleWorkflowEditorMotion(msg.Mouse())
	case tea.MouseReleaseMsg:
		m.draggingNode = -1
	case tea.MouseWheelMsg:
		m.handleWorkflowEditorWheel(msg.Mouse())
		return m, nil
	}
	return m, nil
}

func (m model) updateWorkflowEditorKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if m.editorMode == editorModeModal {
		return m.updateNodeModalKey(msg), nil
	}
	if m.editorOverlay != editorOverlayNone {
		return m.updateEditorOverlayKey(msg), nil
	}
	if m.aiPromptInput.Focused() {
		return m.updateAIBuilderInput(msg), nil
	}

	switch msg.String() {
	case "esc":
		if m.editorMode == editorModeAdd || m.editorMode == editorModeConnect {
			m.editorMode = editorModeNormal
			m.connectSource = -1
			return m, nil
		}
		m.screen = screenApp
		return m, nil
	case "ctrl+p", "ctrl+shift+p":
		m.openWorkflowCommandPalette()
		return m, nil
	case "ctrl+s":
		return m, m.saveCurrentWorkflow()
	case "tab":
		m.selectNextNode(1)
	case "shift+tab":
		m.selectNextNode(-1)
	case "left":
		m.moveSelectedEditorNode(-1, 0)
	case "right":
		m.moveSelectedEditorNode(1, 0)
	case "up":
		if m.editorMode == editorModeAdd {
			nodes := m.getPaletteNodes()
			m.paletteIndex = clamp(m.paletteIndex-1, 0, len(nodes)-1)
			return m, nil
		}
		m.moveSelectedEditorNode(0, -1)
	case "down":
		if m.editorMode == editorModeAdd {
			nodes := m.getPaletteNodes()
			m.paletteIndex = clamp(m.paletteIndex+1, 0, len(nodes)-1)
			return m, nil
		}
		m.moveSelectedEditorNode(0, 1)
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
	switch msg.String() {
	case "a":
		m.editorMode = editorModeAdd
	case "c":
		if m.hasSelectedEditorNode() {
			m.editorMode = editorModeConnect
			m.connectSource = m.selectedNode
		}
	case "backspace", "delete":
		m.deleteSelectedEditorNode()
	}

	return m, nil
}

func (m model) renderWorkflowEditor() string {
	if m.editorMode == editorModeModal {
		return m.renderNodeModal()
	}
	if m.editorOverlay == editorOverlayCommand || m.editorOverlay == editorOverlayCreate {
		return m.renderWorkflowCommandPalette()
	}

	topbar := m.renderWorkflowTopBar()
	palette := renderSectionPanel("NODES", m.renderPaletteLines(), m.paletteRect.w, m.paletteRect.h)
	canvas := renderSectionPanel(
		m.editorWorkflowName,
		m.renderCanvasLines(),
		m.canvasRect.w,
		m.canvasRect.h,
	)
	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		palette,
		blank(sectionGapSize),
		canvas,
	)
	footer := renderFooter(m.workflowFooterText(), m.footerRect.w)
	ui := lipgloss.JoinVertical(lipgloss.Left, topbar, body, footer)
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, ui)
}

func (m model) renderWorkflowTopBar() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(textColor).Render("Heddle")
	name := lipgloss.NewStyle().Bold(true).Foreground(textColor).Render(m.editorWorkflowName)
	saveLabel := "Save"
	saveStyle := lipgloss.NewStyle().Foreground(successColor).Bold(true)
	if m.editorSaving {
		saveLabel = "Saving..."
		saveStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	} else if m.editorDirty {
		saveStyle = lipgloss.NewStyle().Foreground(successColor).Bold(true)
	} else {
		saveStyle = lipgloss.NewStyle().Foreground(mutedTextColor).Bold(true)
	}
	save := saveStyle.Render("Save")
	if m.editorSaving {
		save = saveStyle.Render(saveLabel)
	}

	left := "  " + title + "  │  " + name
	right := save + "  "
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	line := left + blank(gap) + right
	divider := lipgloss.NewStyle().Foreground(panelBorder).Render(strings.Repeat("─", max(m.width, 1)))
	return line + "\n" + divider + "\n" + blank(m.width)
}
func (m model) workflowFooterText() string {
	base := "drag nodes  right-click menu  ctrl+shift+p palette  ctrl+s save  esc back"
	if strings.TrimSpace(m.editorStatusMessage) == "" {
		return base
	}
	return base + "  |  " + m.editorStatusMessage
}

func (m model) workflowSaveButtonRect() rect {
	label := "Save"
	if m.editorSaving {
		label = "Saving..."
	}
	rightWidth := lipgloss.Width(label) + 2
	return rect{x: max(m.width-rightWidth, 0), y: 0, w: lipgloss.Width(label), h: 1}
}

func (m model) renderCanvasLines() []string {
	w := max(m.canvasRect.w-4, 1)
	h := max(m.canvasRect.h-3, 1)
	grid := newCanvasGrid(w, h)

	for _, conn := range m.editorConnections {
		m.drawConnection(grid, conn)
	}
	for i, node := range m.editorNodes {
		drawEditorNode(grid, node, i == m.selectedNode)
	}
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
			{Type: "Start", Title: "Start", Category: "FLOW CONTROL", Color: "#A5B4FC", Icon: "▷"},
			{Type: "Condition", Title: "Condition", Category: "FLOW CONTROL", Color: "#F59E0B", Icon: "◇"},
			{Type: "Output", Title: "Output", Category: "FLOW CONTROL", Color: "#A5B4FC", Icon: "◎"},
			{Type: "Variable", Title: "Variable", Category: "DATA", Color: "#8B5CF6", Icon: "[x]"},
			{Type: "Evaluate Code", Title: "Evaluate Code", Category: "CODE", Color: "#A5B4FC", Icon: "</>"},
		}
	}
	return m.availableNodes
}

func (m model) renderPaletteLines() []string {
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)
	searchWidth := m.paletteSearchInputWidth()
	searchContent := muted.Render(truncatePlain("Search nodes", max(searchWidth-3, 1)))
	search := renderInputBlock(searchContent, searchWidth, false)
	searchLines := strings.Split(search, "\n")

	leftPadding := (max(m.paletteRect.w-4, 1) - searchWidth) / 2
	if leftPadding < 0 {
		leftPadding = 0
	}
	padStr := strings.Repeat(" ", leftPadding)
	for i, l := range searchLines {
		searchLines[i] = padStr + l
	}

	lines := searchLines
	lines = append(lines, "")

	nodes := m.getPaletteNodes()
	currentCategory := ""
	for _, def := range nodes {
		cat := strings.ToUpper(def.Category)
		if cat == "" {
			cat = "OTHER"
		}
		if cat != currentCategory {
			if currentCategory != "" {
				lines = append(lines, "")
			}
			currentCategory = cat
			lines = append(lines, muted.Bold(true).Render(currentCategory))
		}

		color := def.Color
		if color == "" {
			color = "#A5B4FC"
		}
		icon := mapIconToTerminal(def.Icon)

		item := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(icon + "  " + def.Title)
		lines = append(lines, item)
	}
	return lines
}

func (m model) paletteSearchInputWidth() int {
	return max(max(m.paletteRect.w-4, 1)-2, 1)
}

func (m model) paletteNodeRows() []string {
	rows := make([]string, 0, len(m.getPaletteNodes())+8)
	searchRows := strings.Split(renderInputBlock("Search nodes", m.paletteSearchInputWidth(), false), "\n")
	for range searchRows {
		rows = append(rows, "")
	}
	rows = append(rows, "")

	nodes := m.getPaletteNodes()
	currentCategory := ""
	for _, def := range nodes {
		cat := strings.ToUpper(def.Category)
		if cat == "" {
			cat = "OTHER"
		}
		if cat != currentCategory {
			if currentCategory != "" {
				rows = append(rows, "")
			}
			currentCategory = cat
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

func (m model) renderAIChatPanel() string {
	w := m.aiChatRect.w
	h := m.aiChatRect.h
	if w <= 0 || h <= 0 {
		return ""
	}

	innerWidth := max(w-4, 1)
	msgAreaHeight := max(h-5, 1)

	border := lipgloss.NewStyle().Foreground(panelBorder).Render
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(textColor)
	mutedStyle := lipgloss.NewStyle().Foreground(mutedTextColor)
	agentStyle := lipgloss.NewStyle().Foreground(accentColor)
	userStyle := lipgloss.NewStyle().Foreground(successColor)

	var allDisplayLines []string
	for _, msg := range m.aiChatMessages {
		if len(msg) > 5 && msg[:5] == "You: " {
			header := userStyle.Render("You") + mutedStyle.Render(": ")
			contentWidth := max(innerWidth-5, 4) // "You: " = 5 chars
			wrapped := wrapPlainPreserveStyle(msg[5:], contentWidth)
			for i, line := range wrapped {
				if i == 0 {
					allDisplayLines = append(allDisplayLines, header+line)
				} else {
					allDisplayLines = append(allDisplayLines, "     "+line)
				}
			}
		} else if len(msg) > 7 && msg[:7] == "Agent: " {
			header := agentStyle.Render("Agent") + mutedStyle.Render(": ")
			contentWidth := max(innerWidth-7, 4) // "Agent: " = 7 chars
			wrapped := wrapPlainPreserveStyle(msg[7:], contentWidth)
			for i, line := range wrapped {
				if i == 0 {
					allDisplayLines = append(allDisplayLines, header+line)
				} else {
					allDisplayLines = append(allDisplayLines, "       "+line)
				}
			}
		} else {
			wrapped := wrapPlainPreserveStyle(msg, innerWidth)
			allDisplayLines = append(allDisplayLines, wrapped...)
		}
	}

	offset := clampScroll(m.aiChatScroll, len(allDisplayLines), msgAreaHeight)
	msgLines := make([]string, msgAreaHeight)
	for row := 0; row < msgAreaHeight; row++ {
		index := offset + row
		if index < len(allDisplayLines) {
			msgLines[row] = allDisplayLines[index]
		}
	}

	inputWidth := max(w-4, 1)
	inputLine := renderInputBlock(m.aiPromptInput.View(), inputWidth, m.aiPromptInput.Focused())

	var b strings.Builder
	b.WriteString(border("╭" + strings.Repeat("─", max(w-2, 0)) + "╮"))
	b.WriteByte('\n')
	b.WriteString(border("│ ") + padRightBlank(titleStyle.Render("AI Chat"), innerWidth) + border(" │"))
	for _, line := range msgLines {
		b.WriteByte('\n')
		b.WriteString(border("│ ") + padRightBlank(line, innerWidth) + border(" │"))
	}
	b.WriteByte('\n')
	b.WriteString(border("├" + strings.Repeat("─", max(w-2, 0)) + "┤"))

	leftPadding := (innerWidth - inputWidth) / 2
	if leftPadding < 0 {
		leftPadding = 0
	}
	padStr := strings.Repeat(" ", leftPadding)

	for _, line := range strings.Split(inputLine, "\n") {
		b.WriteByte('\n')
		rightPadding := innerWidth - lipgloss.Width(padStr+line)
		if rightPadding < 0 {
			rightPadding = 0
		}
		rightPadStr := strings.Repeat(" ", rightPadding)
		b.WriteString(border("│ ") + padStr + line + rightPadStr + border(" │"))
	}
	b.WriteByte('\n')
	b.WriteString(border("╰" + strings.Repeat("─", max(w-2, 0)) + "╯"))
	return b.String()
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
	lines = append(lines, "", "Drag with mouse to move", "Enter edit  C connect", "A add node  Esc back")
	return lines
}

func (m model) renderNodeModal() string {
	node, ok := m.selectedEditorNode()
	if !ok {
		return m.renderWorkflowEditor()
	}

	width := max(m.modalRect.w, 32)
	lines := []string{
		lipgloss.NewStyle().Foreground(mutedTextColor).Render("Type: " + node.kind),
		"",
	}
	for i, control := range m.modalDraft {
		style := lipgloss.NewStyle().Foreground(textColor)
		prefix := "  "
		if i == m.modalFocusedControl {
			style = style.Foreground(accentColor).Bold(true)
			prefix = "› "
		}
		lines = append(lines, style.Render(prefix+control.label))
		lines = append(lines, "  "+control.value)
		lines = append(lines, "")
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(mutedTextColor).Render("tab field  type edit  enter save  esc cancel"))
	lines = append(lines, "", "          Save        Cancel")

	modal := renderSectionPanel("Edit node", lines, width, m.modalRect.h)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

func (m model) renderWorkflowCommandPalette() string {
	items := m.editorMenuItems()
	title := "Command Palette"
	subtitle := "Workflow actions"
	if m.editorOverlay == editorOverlayCreate {
		title = "Create Node"
		subtitle = "Choose a node type"
	}
	lines := []string{
		lipgloss.NewStyle().Foreground(mutedTextColor).Render(subtitle),
		"",
	}
	for i, item := range items {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(textColor)
		if i == m.editorMenuSelected {
			prefix = "› "
			style = style.Foreground(accentColor).Bold(true)
		}
		lines = append(lines, style.Render(prefix+item.label))
	}
	lines = append(lines, "", lipgloss.NewStyle().Foreground(mutedTextColor).Render("↑↓ select  enter run  esc close"))

	width := min(46, max(m.width-8, 28))
	height := min(max(len(lines)+3, 10), max(m.height-4, 8))
	palette := renderSectionPanel(title, lines, width, height)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, palette)
}

func (m model) handleWorkflowEditorClick(x, y int) (model, tea.Cmd) {
	if m.editorOverlay != editorOverlayNone {
		m.handleEditorOverlayClick(x, y)
		return m, nil
	}

	if inside(x, y, m.workflowSaveButtonRect()) {
		return m, m.saveCurrentWorkflow()
	}

	if m.editorMode == editorModeModal {
		if !inside(x, y, m.modalRect) {
			return m, nil
		}
		if y >= m.modalRect.y+m.modalRect.h-3 {
			if x < m.modalRect.x+m.modalRect.w/2 {
				m.saveNodeModal()
			} else {
				m.editorMode = editorModeNormal
				m.modalDraft = nil
			}
			return m, nil
		}
		row := y - m.modalRect.y - 4
		if row >= 0 {
			index := row / 3
			if index >= 0 && index < len(m.modalDraft) {
				m.modalFocusedControl = index
			}
		}
		return m, nil
	}

	if inside(x, y, m.aiPromptInputRect) {
		m.aiPromptInput.Focus()
		return m, nil
	}
	if inside(x, y, m.aiChatRect) {
		m.aiPromptInput.Blur()
		return m, nil
	}

	if inside(x, y, m.paletteRect) {
		row := y - m.paletteRect.y - 2
		if kind, ok := m.paletteNodeKindAtRow(row); ok {
			m.addEditorNode(kind)
			m.editorMode = editorModeNormal
		}
		return m, nil
	}

	for i, node := range m.editorNodes {
		if inside(x, y, m.nodeRects[node.id]) {
			wasSelected := m.selectedNode == i
			m.selectedNode = i
			if m.editorMode == editorModeConnect {
				m.finishEditorConnection()
				return m, nil
			}
			if wasSelected {
				m.draggingNode = i
			} else {
				m.draggingNode = i
			}
			m.dragOffsetX = x - m.nodeRects[node.id].x
			m.dragOffsetY = y - m.nodeRects[node.id].y
			return m, nil
		}
	}
	return m, nil
}

func (m *model) handleWorkflowEditorWheel(mouse tea.Mouse) {
	if !inside(mouse.X, mouse.Y, m.aiChatRect) {
		return
	}

	switch mouse.Button {
	case tea.MouseWheelUp:
		m.aiChatScroll--
	case tea.MouseWheelDown:
		m.aiChatScroll++
	}
	m.aiChatScroll = clampScroll(m.aiChatScroll, len(m.aiChatMessages), max(m.aiChatMessagesRect.h, 1))
}

func (m model) updateAIBuilderInput(msg tea.KeyPressMsg) model {
	switch msg.String() {
	case "esc":
		m.aiPromptInput.Blur()
		return m
	}
	if msg.Code == tea.KeyEnter {
		m.submitAIBuilderPrompt()
		return m
	}

	var cmd tea.Cmd
	m.aiPromptInput, cmd = m.aiPromptInput.Update(msg)
	_ = cmd
	return m
}

func (m *model) submitAIBuilderPrompt() {
	prompt := fallbackString(m.aiPromptInput.Value(), "")
	if prompt == "" {
		return
	}
	m.aiChatMessages = append(m.aiChatMessages,
		"You: "+prompt,
		"Agent: API not connected yet. I would draft nodes for this request.",
	)
	m.aiChatScroll = clampScroll(len(m.aiChatMessages), len(m.aiChatMessages), max(m.aiChatMessagesRect.h, 1))
	m.aiPromptInput.SetValue("")
}

func (m *model) openWorkflowContextMenu(x, y int) {
	if m.editorMode == editorModeModal {
		return
	}
	m.draggingNode = -1
	m.editorOverlay = editorOverlayContext
	m.editorMenuSelected = 0
	m.editorMenuTarget = -1
	m.editorCreateAtMenu = true

	for i, node := range m.editorNodes {
		if inside(x, y, m.nodeRects[node.id]) {
			m.selectedNode = i
			m.editorMenuTarget = i
			break
		}
	}
	if m.editorMenuTarget < 0 && !inside(x, y, m.canvasRect) {
		m.closeEditorOverlay()
		return
	}

	contentX := x - m.canvasRect.x - 2
	contentY := y - m.canvasRect.y - 2
	menuWidth, menuHeight := m.contextMenuSize()
	m.editorMenuX = clamp(contentX, 0, max(m.canvasRect.w-4-menuWidth, 0))
	m.editorMenuY = clamp(contentY, 0, max(m.canvasRect.h-3-menuHeight, 0))
}

func (m *model) openWorkflowCommandPalette() {
	m.draggingNode = -1
	m.editorOverlay = editorOverlayCommand
	m.editorMenuSelected = 0
	m.editorMenuTarget = m.selectedNode
	m.editorCreateAtMenu = false
}

func (m model) updateEditorOverlayKey(msg tea.KeyPressMsg) model {
	items := m.editorMenuItems()
	switch msg.String() {
	case "esc":
		m.closeEditorOverlay()
	case "up":
		m.editorMenuSelected = clamp(m.editorMenuSelected-1, 0, len(items)-1)
	case "down":
		m.editorMenuSelected = clamp(m.editorMenuSelected+1, 0, len(items)-1)
	}
	if msg.Code == tea.KeyEnter {
		if len(items) > 0 {
			m.runEditorAction(items[m.editorMenuSelected])
		}
	}
	return m
}

func (m *model) handleEditorOverlayClick(x, y int) {
	menuRect := m.editorMenuRect()
	if !inside(x, y, menuRect) {
		m.closeEditorOverlay()
		return
	}

	row := y - menuRect.y - 1
	if m.editorOverlay == editorOverlayCommand || m.editorOverlay == editorOverlayCreate {
		row = y - menuRect.y - 5
	}
	items := m.editorMenuItems()
	if row < 0 || row >= len(items) {
		return
	}
	m.editorMenuSelected = row
	m.runEditorAction(items[row])
}

func (m *model) closeEditorOverlay() {
	m.editorOverlay = editorOverlayNone
	m.editorMenuSelected = 0
	m.editorMenuTarget = -1
	m.editorCreateAtMenu = false
}

func (m model) editorMenuItems() []editorMenuItem {
	if m.editorOverlay == editorOverlayCreate {
		return m.editorCreateMenuItems()
	}

	if m.editorOverlay == editorOverlayContext {
		if m.editorMenuTarget >= 0 && m.editorMenuTarget < len(m.editorNodes) {
			items := []editorMenuItem{
				{label: "Edit node", action: editorActionEdit},
			}
			if !isStartKind(m.editorNodes[m.editorMenuTarget].kind) {
				items = append(items, editorMenuItem{label: "Delete node", action: editorActionDelete})
			}
			return items
		}
		return []editorMenuItem{
			{label: "Create node", action: editorActionOpenCreate},
		}
	}

	items := []editorMenuItem{
		{label: "Create node", action: editorActionOpenCreate},
	}
	if m.hasSelectedEditorNode() {
		items = append(items,
			editorMenuItem{label: "Edit selected node", action: editorActionEdit},
			editorMenuItem{label: "Connect from selected node", action: editorActionConnect},
		)
		if !isStartKind(m.editorNodes[m.selectedNode].kind) {
			items = append(items, editorMenuItem{label: "Delete selected node", action: editorActionDelete})
		}
	}
	return append(items, editorMenuItem{label: "Back to workflows", action: editorActionBack})
}

func (m model) editorMenuRect() rect {
	if m.editorOverlay == editorOverlayCommand || m.editorOverlay == editorOverlayCreate {
		items := m.editorMenuItems()
		width := min(46, max(m.width-8, 28))
		height := min(max(len(items)+7, 10), max(m.height-4, 8))
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
		width = max(width, len(item.label)+6)
	}
	return max(width, 16), len(m.editorMenuItems()) + 2
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

func (m *model) runEditorAction(item editorMenuItem) {
	target := m.editorMenuTarget
	if target >= 0 && target < len(m.editorNodes) {
		m.selectedNode = target
	}

	switch item.action {
	case editorActionOpenCreate:
		m.editorOverlay = editorOverlayCreate
		m.editorMenuSelected = 0
	case editorActionCreate:
		kind := fallbackString(item.kind, "Evaluate Code")
		if m.editorCreateAtMenu {
			m.addEditorNodeAt(kind, m.editorMenuX, m.editorMenuY)
		} else {
			m.addEditorNode(kind)
		}
		m.closeEditorOverlay()
	case editorActionDelete:
		m.closeEditorOverlay()
		m.deleteSelectedEditorNode()
	case editorActionEdit:
		m.closeEditorOverlay()
		m.openNodeModal()
	case editorActionConnect:
		m.closeEditorOverlay()
		if m.hasSelectedEditorNode() {
			m.editorMode = editorModeConnect
			m.connectSource = m.selectedNode
		}
	case editorActionBack:
		m.closeEditorOverlay()
		m.screen = screenApp
	}
}

func (m *model) handleWorkflowEditorMotion(mouse tea.Mouse) {
	if m.draggingNode < 0 || m.draggingNode >= len(m.editorNodes) {
		return
	}
	node := &m.editorNodes[m.draggingNode]
	prevX, prevY := node.x, node.y
	contentOriginX := m.canvasRect.x + 2
	contentOriginY := m.canvasRect.y + 2
	maxX := max(m.canvasRect.w-4-node.w, 0)
	maxY := max(m.canvasRect.h-3-node.h, 0)
	node.x = clamp(mouse.X-contentOriginX-m.dragOffsetX, 0, maxX)
	node.y = clamp(mouse.Y-contentOriginY-m.dragOffsetY, 0, maxY)
	m.rebuildNodeRects()
	if node.x != prevX || node.y != prevY {
		m.markEditorDirty()
	}
}

func (m *model) moveSelectedEditorNode(dx, dy int) {
	if !m.hasSelectedEditorNode() {
		return
	}
	node := &m.editorNodes[m.selectedNode]
	prevX, prevY := node.x, node.y
	node.x = clamp(node.x+dx, 0, max(m.canvasRect.w-4-node.w, 0))
	node.y = clamp(node.y+dy, 0, max(m.canvasRect.h-3-node.h, 0))
	m.rebuildNodeRects()
	if node.x != prevX || node.y != prevY {
		m.markEditorDirty()
	}
}

func (m *model) addEditorNode(kind string) {
	m.addEditorNodeAt(kind, max((m.canvasRect.w-4)/2-8, 0), max((m.canvasRect.h-3)/2-1, 0))
}

func (m *model) addEditorNodeAt(kind string, x, y int) {
	id := fmt.Sprintf("node_%d", len(m.editorNodes)+1)
	title := kind
	nodes := m.getPaletteNodes()
	for _, def := range nodes {
		if def.Type == kind {
			title = def.Title
			break
		}
	}
	node := editorNode{
		id:       id,
		kind:     kind,
		label:    title,
		w:        editorNodeWidth(title),
		h:        3,
		controls: defaultControls(kind),
	}
	node.x = clamp(x, 0, max(m.canvasRect.w-4-node.w, 0))
	node.y = clamp(y, 0, max(m.canvasRect.h-3-node.h, 0))
	m.editorNodes = append(m.editorNodes, node)
	m.selectedNode = len(m.editorNodes) - 1
	m.rebuildNodeRects()
	m.markEditorDirty()
}

func (m *model) finishEditorConnection() {
	if m.connectSource < 0 || m.connectSource >= len(m.editorNodes) || !m.hasSelectedEditorNode() || m.connectSource == m.selectedNode {
		m.editorMode = editorModeNormal
		m.connectSource = -1
		return
	}
	source := m.editorNodes[m.connectSource].id
	target := m.editorNodes[m.selectedNode].id
	for _, conn := range m.editorConnections {
		if conn.source == source && conn.target == target {
			m.editorMode = editorModeNormal
			m.connectSource = -1
			return
		}
	}
	m.editorConnections = append(m.editorConnections, editorConnection{source: source, target: target})
	m.editorMode = editorModeNormal
	m.connectSource = -1
	m.markEditorDirty()
}

func (m *model) openNodeModal() {
	if !m.hasSelectedEditorNode() {
		return
	}
	m.editorMode = editorModeModal
	m.modalFocusedControl = 0
	m.modalDraft = append([]nodeControl(nil), m.editorNodes[m.selectedNode].controls...)
}

func (m model) updateNodeModalKey(msg tea.KeyPressMsg) model {
	switch msg.String() {
	case "esc":
		m.editorMode = editorModeNormal
		m.modalDraft = nil
	case "tab":
		if len(m.modalDraft) > 0 {
			m.modalFocusedControl = (m.modalFocusedControl + 1) % len(m.modalDraft)
		}
	case "backspace":
		if len(m.modalDraft) > 0 {
			value := []rune(m.modalDraft[m.modalFocusedControl].value)
			if len(value) > 0 {
				m.modalDraft[m.modalFocusedControl].value = string(value[:len(value)-1])
			}
		}
	}
	if msg.Code == tea.KeyEnter {
		m.saveNodeModal()
	}
	text := msg.Key().Text
	if text != "" && len(m.modalDraft) > 0 {
		m.modalDraft[m.modalFocusedControl].value += text
	}
	return m
}

func (m *model) saveNodeModal() {
	if m.hasSelectedEditorNode() {
		m.editorNodes[m.selectedNode].controls = append([]nodeControl(nil), m.modalDraft...)
		for _, control := range m.modalDraft {
			if control.name == "label" {
				m.editorNodes[m.selectedNode].label = fallbackString(control.value, m.editorNodes[m.selectedNode].kind)
				m.editorNodes[m.selectedNode].w = editorNodeWidth(m.editorNodes[m.selectedNode].label)
			}
		}
		m.markEditorDirty()
	}
	m.editorMode = editorModeNormal
	m.modalDraft = nil
}

func (m *model) deleteSelectedEditorNode() {
	if !m.hasSelectedEditorNode() || isStartKind(m.editorNodes[m.selectedNode].kind) {
		return
	}
	deletedID := m.editorNodes[m.selectedNode].id
	m.editorNodes = append(m.editorNodes[:m.selectedNode], m.editorNodes[m.selectedNode+1:]...)
	nextConnections := m.editorConnections[:0]
	for _, conn := range m.editorConnections {
		if conn.source != deletedID && conn.target != deletedID {
			nextConnections = append(nextConnections, conn)
		}
	}
	m.editorConnections = nextConnections
	m.selectedNode = clamp(m.selectedNode, 0, len(m.editorNodes)-1)
	m.rebuildNodeRects()
	m.markEditorDirty()
}

func (m *model) selectNextNode(delta int) {
	if len(m.editorNodes) == 0 {
		m.selectedNode = -1
		return
	}
	m.selectedNode = (m.selectedNode + delta + len(m.editorNodes)) % len(m.editorNodes)
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

func (m *model) clampEditorNodes() {
	for i := range m.editorNodes {
		node := &m.editorNodes[i]
		node.x = clamp(node.x, 0, max(m.canvasRect.w-4-node.w, 0))
		node.y = clamp(node.y, 0, max(m.canvasRect.h-3-node.h, 0))
	}
}

func (m *model) rebuildNodeRects() {
	if m.nodeRects == nil {
		m.nodeRects = map[string]rect{}
	}
	for key := range m.nodeRects {
		delete(m.nodeRects, key)
	}
	for _, node := range m.editorNodes {
		m.nodeRects[node.id] = rect{
			x: m.canvasRect.x + 2 + node.x,
			y: m.canvasRect.y + 2 + node.y,
			w: node.w,
			h: node.h,
		}
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

func drawEditorNode(grid [][]canvasCell, node editorNode, selected bool) {
	if node.w < 4 || node.h < 3 {
		return
	}
	var borderStyle *lipgloss.Style
	if selected {
		style := lipgloss.NewStyle().Foreground(focusedBorderColor)
		borderStyle = &style
	}

	leftTop, rightTop, leftBottom, rightBottom := '╭', '╮', '╰', '╯'
	drawStyledRune(grid, node.x, node.y, leftTop, borderStyle)
	drawStyledRune(grid, node.x+node.w-1, node.y, rightTop, borderStyle)
	drawStyledRune(grid, node.x, node.y+node.h-1, leftBottom, borderStyle)
	drawStyledRune(grid, node.x+node.w-1, node.y+node.h-1, rightBottom, borderStyle)
	for x := node.x + 1; x < node.x+node.w-1; x++ {
		drawStyledRune(grid, x, node.y, '─', borderStyle)
		drawStyledRune(grid, x, node.y+node.h-1, '─', borderStyle)
	}
	for y := node.y + 1; y < node.y+node.h-1; y++ {
		drawStyledRune(grid, node.x, y, '│', borderStyle)
		drawStyledRune(grid, node.x+node.w-1, y, '│', borderStyle)
	}
	label := node.label
	if selected {
		label = "> " + label
	}
	drawText(grid, node.x+2, node.y+1, truncatePlain(label, node.w-4))
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

	drawRune(grid, x, y, '╭')
	drawRune(grid, x+width-1, y, '╮')
	drawRune(grid, x, y+height-1, '╰')
	drawRune(grid, x+width-1, y+height-1, '╯')
	for col := x + 1; col < x+width-1; col++ {
		drawRune(grid, col, y, '─')
		drawRune(grid, col, y+height-1, '─')
	}
	for row := y + 1; row < y+height-1; row++ {
		drawRune(grid, x, row, '│')
		drawRune(grid, x+width-1, row, '│')
		for col := x + 1; col < x+width-1; col++ {
			drawRune(grid, col, row, ' ')
		}
	}
	for i, item := range items {
		prefix := "  "
		if i == m.editorMenuSelected {
			prefix = "› "
		}
		drawText(grid, x+2, y+1+i, truncatePlain(prefix+item.label, max(width-4, 1)))
	}
}

func (m model) drawConnection(grid [][]canvasCell, conn editorConnection) {
	source, okSource := m.editorNodeByID(conn.source)
	target, okTarget := m.editorNodeByID(conn.target)
	if !okSource || !okTarget {
		return
	}
	x1 := source.x + source.w
	y1 := source.y + source.h/2
	x2 := target.x - 1
	y2 := target.y + target.h/2
	arrow := '▶'
	if y1 == y2 && x1 <= x2 {
		drawHorizontalLine(grid, x1, x2, y1)
		drawRune(grid, x2, y2, arrow)
		return
	}
	if x1 > x2 {
		x1 = source.x + source.w/2
		y1 = source.y + source.h
		x2 = target.x + target.w/2
		y2 = target.y - 1
		arrow = '▼'
		if target.y < source.y {
			y1 = source.y - 1
			y2 = target.y + target.h
			arrow = '▲'
		}
	}
	midX := (x1 + x2) / 2
	drawHorizontalLine(grid, x1, midX, y1)
	drawVerticalLine(grid, midX, y1, y2)
	drawHorizontalLine(grid, midX, x2, y2)
	drawRune(grid, midX, y1, connectionCorner(horizontalDirection(x1, midX), verticalDirection(y1, y2)))
	drawRune(grid, midX, y2, connectionCorner(-horizontalDirection(midX, x2), -verticalDirection(y1, y2)))
	drawRune(grid, x2, y2, arrow)
}

func drawHorizontalLine(grid [][]canvasCell, x1, x2, y int) {
	for x := min(x1, x2); x <= max(x1, x2); x++ {
		drawRune(grid, x, y, '─')
	}
}

func drawVerticalLine(grid [][]canvasCell, x, y1, y2 int) {
	for y := min(y1, y2); y <= max(y1, y2); y++ {
		drawRune(grid, x, y, '│')
	}
}

func horizontalDirection(from, to int) int {
	if to < from {
		return -1
	}
	return 1
}

func verticalDirection(from, to int) int {
	if to < from {
		return -1
	}
	return 1
}

func connectionCorner(dx, dy int) rune {
	switch {
	case dx > 0 && dy > 0:
		return '╮'
	case dx > 0 && dy < 0:
		return '╯'
	case dx < 0 && dy > 0:
		return '╭'
	default:
		return '╰'
	}
}

func (m model) editorNodeByID(id string) (editorNode, bool) {
	for _, node := range m.editorNodes {
		if node.id == id {
			return node, true
		}
	}
	return editorNode{}, false
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
	controls := append([]nodeControl(nil), defaultControls(kind)...)
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
		value := strings.TrimSpace(fmt.Sprintf("%v", raw[key]))
		if idx, ok := indexByName[key]; ok {
			controls[idx].value = value
			continue
		}
		controls = append(controls, nodeControl{
			name:  key,
			label: titleizeControlName(key),
			value: value,
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
			{name: "name", label: "Name", value: "x"},
			{name: "type", label: "Type", value: "Integer"},
			{name: "value", label: "Value", value: "1"},
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
