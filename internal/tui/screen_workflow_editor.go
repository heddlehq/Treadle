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
	m.selectOnly(0)
	m.undoStack, m.redoStack, m.pasteCount = nil, nil, 0
	m.viewX, m.viewY, m.zoomIndex = 0, 0, defaultZoomIndex
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
	m.editorConnections = []editorConnection{{source: "start", sourceOutput: "exec", target: "output", targetInput: "exec"}}
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
	m.selectOnly(0)
	m.fitView()
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
	for _, node := range m.editorNodes {
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
	}
	return m, nil
}

func (m model) updateWorkflowEditorKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	if m.editorMode == editorModeModal {
		return m.updateNodeModalKey(msg)
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
			m.cancelConnection()
			return m, nil
		}
		if m.hasSelectedConnection() || len(m.selectedIndices()) > 1 {
			m.clearSelection()
			return m, nil
		}
		m.screen = screenApp
		return m, nil
	case "ctrl+z":
		m.undo()
		return m, nil
	case "ctrl+y", "ctrl+shift+z":
		m.redo()
		return m, nil
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
	if m.editorOverlay == editorOverlayCommand || m.editorOverlay == editorOverlayCreate {
		return m.renderWorkflowCommandPalette()
	}

	palette := m.renderPalettePane()
	canvas := m.renderCanvasPane()
	separator := lipgloss.NewStyle().Foreground(panelBorder).Render("│")
	rule := lipgloss.NewStyle().Foreground(panelBorder).Render

	rows := m.renderWorkflowTopBar()
	for i := range m.paletteRect.h {
		rows = append(rows, padRightBlank(palette[i], m.paletteRect.w)+separator+canvas[i])
	}
	rows = append(rows,
		rule(m.ruleWithJunction(m.paletteRect.w, "─", "┴")),
		renderHints(m.workflowFooterText(), m.footerRect.w),
	)
	return strings.Join(rows, "\n")
}

// renderPalettePane keeps the first node row two rows down, where
// paletteNodeKindAtRow expects it.
func (m model) renderPalettePane() []string {
	title := lipgloss.NewStyle().Foreground(mutedTextColor).Bold(true).Render("NODES")
	lines := append([]string{"", "  " + title}, m.renderPaletteLines()...)
	for len(lines) < m.paletteRect.h {
		lines = append(lines, "")
	}
	return lines[:m.paletteRect.h]
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

	caption := plural(len(m.editorNodes), "node") + "  ·  " + plural(len(m.editorConnections), "connection")
	switch count := len(m.selectedIndices()); {
	case m.hasSelectedConnection():
		caption = m.selectedConnectionCaption()
	case count > 1:
		caption = fmt.Sprintf("%d nodes selected. Drag to move them together, del removes them.", count)
	case count == 1:
		caption = "Drag ○ to a node to connect it, or click a node type to add it after this one."
	}
	caption += fmt.Sprintf("  ·  %d%%", scaled(100, m.zoom()))
	return lipgloss.NewStyle().Foreground(mutedTextColor).Render(truncatePlain(caption, width))
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
	case m.editorSaving:
		save = muted.Bold(true).Render("Saving...")
	case m.editorDirty:
		save = lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("Save")
	}
	right := save + "  "
	if status := m.renderEditorStatus(max(m.width-lipgloss.Width(left)-lipgloss.Width(right)-6, 8)); status != "" {
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
	case strings.HasPrefix(message, "Save failed"), strings.HasPrefix(message, "Missing"), strings.HasPrefix(message, "Can't"):
		statusColor = errorColor
	}
	return lipgloss.NewStyle().Foreground(statusColor).Render("● " + truncatePlain(message, width))
}

func (m model) workflowFooterText() string {
	if m.editorMode == editorModeConnect {
		return "tab next node  c next output  ⏎ connect  esc cancel"
	}
	return "c connect  del delete  ctrl+z undo  +/- zoom  f fit  ctrl+s save  esc back"
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
	drawCanvasDots(grid)

	m.drawConnections(grid)
	m.drawPendingWire(grid)
	for i, node := range m.editorNodes {
		m.drawNode(grid, i, m.projectNode(node))
	}
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
	width := min(46, max(m.width-8, 28))
	for i, item := range items {
		lines = append(lines, renderListRow("›", accentColor, item.label, width-4, i == m.editorMenuSelected))
	}
	lines = append(lines, "", lipgloss.NewStyle().Foreground(mutedTextColor).Render("↑↓ select  enter run  esc close"))

	height := min(max(len(lines)+3, 10), max(m.height-4, 8))
	palette := renderSectionPanel(title, lines, width, height)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, palette)
}

func (m model) handleWorkflowEditorClick(mouse tea.Mouse) (model, tea.Cmd) {
	x, y := mouse.X, mouse.Y
	if m.editorOverlay != editorOverlayNone {
		m.handleEditorOverlayClick(x, y)
		return m, nil
	}

	if inside(x, y, m.workflowSaveButtonRect()) {
		return m, m.saveCurrentWorkflow()
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

	if inside(x, y, m.paletteRect) {
		row := y - m.paletteRect.y - 2
		if kind, ok := m.paletteNodeKindAtRow(row); ok {
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

	if i := m.nodeIndexAt(x, y); i >= 0 {
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
	if inside(mouse.X, mouse.Y, m.canvasRect) {
		m.handleCanvasWheel(mouse)
		return
	}
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
		m.editorMenuSelected = nextMenuItem(items, m.editorMenuSelected, -1)
	case "down":
		m.editorMenuSelected = nextMenuItem(items, m.editorMenuSelected, 1)
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
		return m.contextMenuItems()
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

func (m *model) runEditorAction(item editorMenuItem) {
	if !item.selectable() {
		return
	}

	switch item.action {
	case editorActionUndo:
		m.closeEditorOverlay()
		m.undo()
	case editorActionRedo:
		m.closeEditorOverlay()
		m.redo()
	case editorActionCopy:
		m.closeEditorOverlay()
		m.copySelection()
		m.pasteCount = 0
	case editorActionPaste:
		x, y := m.toWorld(m.editorMenuX, m.editorMenuY)
		m.closeEditorOverlay()
		m.pasteAt(x, y)
	case editorActionOpenCreate:
		m.editorOverlay = editorOverlayCreate
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
	case editorActionEdit:
		m.closeEditorOverlay()
		m.openNodeModal()
	case editorActionConnect:
		m.closeEditorOverlay()
		m.beginConnectionFromSelected()
	case editorActionBack:
		m.closeEditorOverlay()
		m.screen = screenApp
	}
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
	switch {
	case connecting && index == m.wireTarget():
		border = border.Foreground(successColor)
		fill = fill.Bold(true)
	case highlighted:
		border = border.Foreground(focusedBorderColor)
		fill = fill.Bold(true)
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
