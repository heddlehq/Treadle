package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

type canvasPoint struct {
	x int
	y int
}

type canvasBox struct {
	top    int
	bottom int
}

// portID reads a port from the API, which sends either a name or an object.
func portID(raw any) string {
	switch port := raw.(type) {
	case string:
		return port
	case map[string]any:
		if id, ok := port["id"].(string); ok {
			return id
		}
	}
	return ""
}

func portIDs(raw []any) []string {
	ports := make([]string, 0, len(raw))
	for _, item := range raw {
		if id := portID(item); id != "" {
			ports = append(ports, id)
		}
	}
	return ports
}

func (m model) nodeOutputs(kind string) []string {
	if def, ok := m.nodeDefinition(kind); ok {
		return portIDs(def.Outputs)
	}
	return []string{"exec"}
}

func (m model) nodeInputs(kind string) []string {
	if def, ok := m.nodeDefinition(kind); ok {
		return portIDs(def.Inputs)
	}
	return []string{"exec"}
}

// nodeSize keeps single-output nodes on one row. Nodes that branch get a row per
// output, so each one can be labelled and grabbed.
func (m model) nodeSize(kind, label string) (int, int) {
	outputs := m.nodeOutputs(kind)
	width := editorNodeWidth(label)
	if len(outputs) < 2 {
		return width, 3
	}
	for _, output := range outputs {
		width = max(width, len([]rune(output))+6)
	}
	return width, len(outputs) + 3
}

func outputRow(node editorNode, index, outputCount int) int {
	if outputCount < 2 {
		return node.y + 1
	}
	return node.y + 2 + index
}

func (m model) outputIndex(node editorNode, output string) int {
	for i, candidate := range m.nodeOutputs(node.kind) {
		if candidate == output {
			return i
		}
	}
	return 0
}

// savedPorts names the ports of a connection, falling back to the node's first
// ports for connections that were never given any.
func (m model) savedPorts(conn editorConnection) (string, string) {
	output, input := conn.sourceOutput, conn.targetInput
	if source, ok := m.editorNodeByID(conn.source); ok {
		outputs := m.nodeOutputs(source.kind)
		if len(outputs) > 0 && !containsString(outputs, output) {
			output = outputs[0]
		}
	}
	if target, ok := m.editorNodeByID(conn.target); ok {
		inputs := m.nodeInputs(target.kind)
		if len(inputs) > 0 && !containsString(inputs, input) {
			input = inputs[0]
		}
	}
	return output, input
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (m model) editorNodeByID(id string) (editorNode, bool) {
	for _, node := range m.editorNodes {
		if node.id == id {
			return node, true
		}
	}
	return editorNode{}, false
}

func (m model) outputPoint(node editorNode, index int) canvasPoint {
	return canvasPoint{x: node.x + node.w, y: outputRow(node, index, len(m.nodeOutputs(node.kind)))}
}

func inputPoint(node editorNode) canvasPoint {
	return canvasPoint{x: node.x - 1, y: node.y + 1}
}

func nodeBox(node editorNode) canvasBox {
	return canvasBox{top: node.y, bottom: node.y + node.h - 1}
}

// routeWire joins an output to an input with right angles, always leaving rightward
// and arriving rightward. A target behind the source is reached by looping around.
func routeWire(start, end canvasPoint, source, target canvasBox) []canvasPoint {
	if end.x >= start.x+2 {
		if start.y == end.y {
			return []canvasPoint{start, end}
		}
		midX := (start.x + end.x) / 2
		return []canvasPoint{start, {midX, start.y}, {midX, end.y}, end}
	}

	var lane int
	switch {
	case target.top > source.bottom+1:
		lane = (source.bottom + target.top) / 2
	case source.top > target.bottom+1:
		lane = (target.bottom + source.top) / 2
	default:
		lane = max(source.bottom, target.bottom) + 1
	}

	outX := start.x + 1
	inX := max(end.x-1, 0)
	return []canvasPoint{start, {outX, start.y}, {outX, lane}, {inX, lane}, {inX, end.y}, end}
}

func wireCorner(fromX, fromY, toX, toY int) rune {
	switch {
	case fromX > 0 && toY > 0, fromY < 0 && toX < 0:
		return '╮'
	case fromX > 0 && toY < 0, fromY > 0 && toX < 0:
		return '╯'
	case fromX < 0 && toY > 0, fromY < 0 && toX > 0:
		return '╭'
	default:
		return '╰'
	}
}

func sign(value int) int {
	switch {
	case value > 0:
		return 1
	case value < 0:
		return -1
	default:
		return 0
	}
}

func drawWireCell(grid [][]canvasCell, x, y int, r rune, style *lipgloss.Style) {
	if y >= 0 && y < len(grid) && x >= 0 && x < len(grid[y]) {
		existing := grid[y][x].ch
		if (existing == '─' && r == '│') || (existing == '│' && r == '─') {
			r = '┼'
		}
	}
	drawStyledRune(grid, x, y, r, style)
}

// heavyWire redraws a route in thicker lines, to mark the selected connection.
func heavyWire(grid [][]canvasCell, points []canvasPoint, style *lipgloss.Style) {
	heavy := map[rune]rune{'─': '━', '│': '┃', '╭': '┏', '╮': '┓', '╰': '┗', '╯': '┛', '┼': '╋'}
	for i := 0; i+1 < len(points); i++ {
		from, to := points[i], points[i+1]
		dx, dy := sign(to.x-from.x), sign(to.y-from.y)
		for x, y := from.x, from.y; x != to.x || y != to.y; x, y = x+dx, y+dy {
			if y >= 0 && y < len(grid) && x >= 0 && x < len(grid[y]) {
				if thick, ok := heavy[grid[y][x].ch]; ok {
					drawStyledRune(grid, x, y, thick, style)
				}
			}
		}
	}
}

// drawWire draws a route that ends in an arrow on its last cell.
func drawWire(grid [][]canvasCell, points []canvasPoint, line, head *lipgloss.Style) {
	for i := 0; i+1 < len(points); i++ {
		from, to := points[i], points[i+1]
		dx, dy := sign(to.x-from.x), sign(to.y-from.y)
		for x, y := from.x, from.y; x != to.x || y != to.y; x, y = x+dx, y+dy {
			if dy == 0 {
				drawWireCell(grid, x, y, '─', line)
			} else {
				drawWireCell(grid, x, y, '│', line)
			}
		}
	}

	for i := 1; i+1 < len(points); i++ {
		before, here, after := points[i-1], points[i], points[i+1]
		if before == here || here == after {
			continue
		}
		corner := wireCorner(sign(here.x-before.x), sign(here.y-before.y), sign(after.x-here.x), sign(after.y-here.y))
		drawStyledRune(grid, here.x, here.y, corner, line)
	}

	last := points[len(points)-1]
	drawStyledRune(grid, last.x, last.y, '▶', head)
}

func (m model) connectionPath(conn editorConnection) ([]canvasPoint, bool) {
	source, okSource := m.editorNodeByID(conn.source)
	target, okTarget := m.editorNodeByID(conn.target)
	if !okSource || !okTarget {
		return nil, false
	}
	output, _ := m.savedPorts(conn)
	index := m.outputIndex(source, output)
	source, target = m.projectNode(source), m.projectNode(target)
	return routeWire(m.outputPoint(source, index), inputPoint(target), nodeBox(source), nodeBox(target)), true
}

func (m model) drawConnections(grid [][]canvasCell) {
	quiet := lipgloss.NewStyle().Foreground(mutedTextColor)
	active := lipgloss.NewStyle().Foreground(accentColor)

	selected := map[string]bool{}
	for _, index := range m.selectedIndices() {
		selected[m.editorNodes[index].id] = true
	}

	// Wires of the selected node go last, so they stay on top.
	for _, highlighted := range []bool{false, true} {
		for _, conn := range m.editorConnections {
			if (selected[conn.source] || selected[conn.target]) != highlighted {
				continue
			}
			path, ok := m.connectionPath(conn)
			if !ok {
				continue
			}
			if highlighted {
				drawWire(grid, path, &active, &active)
			} else {
				drawWire(grid, path, &quiet, &active)
			}
		}
	}

	if m.hasSelectedConnection() {
		if path, ok := m.connectionPath(m.editorConnections[m.selectedConnection]); ok {
			bold := active.Bold(true)
			drawWire(grid, path, &bold, &bold)
			heavyWire(grid, path, &bold)
		}
	}
}

// wireTarget is the node a pending connection would reach, or -1.
func (m model) wireTarget() int {
	if m.editorMode != editorModeConnect {
		return -1
	}
	target := m.selectedNode
	if m.wireActive {
		target = m.nodeIndexAt(m.wireX, m.wireY)
	}
	if target == m.connectSource {
		return -1
	}
	return target
}

func (m model) drawPendingWire(grid [][]canvasCell) {
	if m.editorMode != editorModeConnect || m.connectSource < 0 || m.connectSource >= len(m.editorNodes) {
		return
	}

	style := lipgloss.NewStyle().Foreground(accentColor)
	source := m.projectNode(m.editorNodes[m.connectSource])
	start := m.outputPoint(source, m.connectOutput)

	if target := m.wireTarget(); target >= 0 && target < len(m.editorNodes) {
		node := m.projectNode(m.editorNodes[target])
		drawWire(grid, routeWire(start, inputPoint(node), nodeBox(source), nodeBox(node)), &style, &style)
		return
	}
	if m.wireActive {
		content := m.canvasContentRect()
		cursor := canvasPoint{x: m.wireX - content.x, y: m.wireY - content.y}
		drawWire(grid, routeWire(start, cursor, nodeBox(source), canvasBox{top: cursor.y, bottom: cursor.y}), &style, &style)
	}
}

func (m model) nodeIndexAt(x, y int) int {
	if !inside(x, y, m.canvasContentRect()) {
		return -1
	}
	for i := len(m.editorNodes) - 1; i >= 0; i-- {
		if inside(x, y, m.nodeRects[m.editorNodes[i].id]) {
			return i
		}
	}
	return -1
}

// outputPortAt is generous on purpose: a port is one cell, so the cells either side
// of it count too.
func (m model) outputPortAt(x, y int) (int, int, bool) {
	if !inside(x, y, m.canvasContentRect()) {
		return 0, 0, false
	}
	for i := len(m.editorNodes) - 1; i >= 0; i-- {
		node := m.editorNodes[i]
		outputs := m.nodeOutputs(node.kind)
		r := m.nodeRects[node.id]
		if len(outputs) == 0 || x < r.x+r.w-2 || x > r.x+r.w {
			continue
		}
		if len(outputs) == 1 && y >= r.y && y < r.y+r.h {
			return i, 0, true
		}
		for output := range outputs {
			if y == r.y+outputRow(node, output, len(outputs))-node.y {
				return i, output, true
			}
		}
	}
	return 0, 0, false
}

func (m model) outputConnected(node editorNode, output string) bool {
	for _, conn := range m.editorConnections {
		if conn.source != node.id {
			continue
		}
		if saved, _ := m.savedPorts(conn); saved == output {
			return true
		}
	}
	return false
}

// firstFreeOutput returns the first output with nothing attached, or -1.
func (m model) firstFreeOutput(node editorNode) int {
	for i, output := range m.nodeOutputs(node.kind) {
		if !m.outputConnected(node, output) {
			return i
		}
	}
	return -1
}

func (m *model) beginConnection(source, output int) {
	m.connectSource = source
	m.connectOutput = output
	m.selectedNode = source
	m.editorMode = editorModeConnect
}

func (m *model) beginConnectionFromSelected() {
	if !m.hasSelectedEditorNode() {
		return
	}
	node := m.editorNodes[m.selectedNode]
	if len(m.nodeOutputs(node.kind)) == 0 {
		m.editorStatusMessage = "Can't connect from " + node.label + ": it has no outputs"
		return
	}
	m.beginConnection(m.selectedNode, max(m.firstFreeOutput(node), 0))
}

func (m *model) cycleConnectOutput() {
	if m.connectSource < 0 || m.connectSource >= len(m.editorNodes) {
		return
	}
	count := len(m.nodeOutputs(m.editorNodes[m.connectSource].kind))
	if count > 0 {
		m.connectOutput = (m.connectOutput + 1) % count
	}
}

func (m *model) cancelConnection() {
	m.editorMode = editorModeNormal
	m.connectSource = -1
	m.wireActive = false
}

// finishWire ends a drag. Letting go on the source keeps the connection open, so a
// click on the port followed by a click on the target works too.
func (m *model) finishWire(x, y int) {
	m.wireActive = false

	target := m.nodeIndexAt(x, y)
	switch target {
	case m.connectSource:
	case -1:
		m.cancelConnection()
	default:
		m.selectedNode = target
		m.finishEditorConnection()
	}
}

// finishEditorConnection links the source to the selected node. Linking the same
// pair again removes the connection.
func (m *model) finishEditorConnection() {
	defer m.cancelConnection()

	if m.connectSource < 0 || m.connectSource >= len(m.editorNodes) || !m.hasSelectedEditorNode() || m.connectSource == m.selectedNode {
		return
	}

	source := m.editorNodes[m.connectSource]
	target := m.editorNodes[m.selectedNode]
	inputs := m.nodeInputs(target.kind)
	if len(inputs) == 0 {
		m.editorStatusMessage = "Can't connect to " + target.label + ": it has no inputs"
		return
	}
	outputs := m.nodeOutputs(source.kind)
	if m.connectOutput >= len(outputs) {
		return
	}
	output := outputs[m.connectOutput]

	for i, conn := range m.editorConnections {
		saved, _ := m.savedPorts(conn)
		if conn.source == source.id && conn.target == target.id && saved == output {
			m.pushUndo()
			m.editorConnections = append(m.editorConnections[:i:i], m.editorConnections[i+1:]...)
			m.markEditorDirty()
			return
		}
	}

	m.pushUndo()
	m.editorConnections = append(m.editorConnections, editorConnection{
		source:       source.id,
		sourceOutput: output,
		target:       target.id,
		targetInput:  inputs[0],
	})
	m.markEditorDirty()
}

// nextNodeSpot places a new node after source, moving down past anything in the way.
func (m model) nextNodeSpot(source editorNode, width, height int) (int, int) {
	x := source.x + source.w + 6
	y := source.y
	for m.nodeOverlaps(x, y, width, height) {
		y += height + 1
	}
	return x, y
}

func (m model) nodeOverlaps(x, y, width, height int) bool {
	for _, node := range m.editorNodes {
		if x < node.x+node.w+1 && node.x < x+width+1 && y < node.y+node.h && node.y < y+height {
			return true
		}
	}
	return false
}

func (m model) nextNodeID() string {
	for n := len(m.editorNodes) + 1; ; n++ {
		id := fmt.Sprintf("node_%d", n)
		if _, taken := m.editorNodeByID(id); !taken {
			return id
		}
	}
}

func (m model) connectCaption() string {
	if m.connectSource < 0 || m.connectSource >= len(m.editorNodes) {
		return ""
	}
	source := m.editorNodes[m.connectSource]
	label := source.label
	if outputs := m.nodeOutputs(source.kind); len(outputs) > 1 && m.connectOutput < len(outputs) {
		label += " (" + outputs[m.connectOutput] + ")"
	}
	return "From " + label + ": pick a node. Picking a linked one removes the link."
}
