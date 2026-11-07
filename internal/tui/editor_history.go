package tui

import "fmt"

const (
	maxUndoSteps = 50
	pasteOffsetX = 4
	pasteOffsetY = 2
)

// editorSnapshot is the part of the workflow that undo restores.
type editorSnapshot struct {
	nodes       []editorNode
	connections []editorConnection
}

func copyNodes(nodes []editorNode) []editorNode {
	copied := make([]editorNode, len(nodes))
	for i, node := range nodes {
		node.controls = append([]nodeControl(nil), node.controls...)
		copied[i] = node
	}
	return copied
}

func (m model) snapshot() editorSnapshot {
	return editorSnapshot{
		nodes:       copyNodes(m.editorNodes),
		connections: append([]editorConnection(nil), m.editorConnections...),
	}
}

// pushUndo runs just before a change, and a new change ends any redo.
func (m *model) pushUndo() {
	m.undoStack = append(m.undoStack, m.snapshot())
	if len(m.undoStack) > maxUndoSteps {
		m.undoStack = m.undoStack[len(m.undoStack)-maxUndoSteps:]
	}
	m.redoStack = nil
}

func (m *model) restore(snapshot editorSnapshot) {
	m.editorNodes = snapshot.nodes
	m.editorConnections = snapshot.connections
	m.clearSelection()
	m.rebuildNodeRects()
	m.markEditorDirty()
}

func (m *model) undo() {
	if len(m.undoStack) == 0 {
		return
	}
	last := len(m.undoStack) - 1
	m.redoStack = append(m.redoStack, m.snapshot())
	snapshot := m.undoStack[last]
	m.undoStack = m.undoStack[:last]
	m.restore(snapshot)
}

func (m *model) redo() {
	if len(m.redoStack) == 0 {
		return
	}
	last := len(m.redoStack) - 1
	m.undoStack = append(m.undoStack, m.snapshot())
	snapshot := m.redoStack[last]
	m.redoStack = m.redoStack[:last]
	m.restore(snapshot)
}

// copySelection keeps the selected nodes and the connections between them. Start is
// left out: a workflow only has one.
func (m *model) copySelection() {
	selected := map[string]bool{}
	var nodes []editorNode
	for _, index := range m.selectedIndices() {
		if node := m.editorNodes[index]; !isStartKind(node.kind) {
			selected[node.id] = true
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		return
	}

	m.clipboardNodes = copyNodes(nodes)
	m.clipboardConnections = nil
	for _, conn := range m.editorConnections {
		if selected[conn.source] && selected[conn.target] {
			m.clipboardConnections = append(m.clipboardConnections, conn)
		}
	}
	m.editorStatusMessage = plural(len(nodes), "node") + " copied"
}

func (m model) clipboardOrigin() canvasPoint {
	origin := canvasPoint{m.clipboardNodes[0].x, m.clipboardNodes[0].y}
	for _, node := range m.clipboardNodes {
		origin.x = min(origin.x, node.x)
		origin.y = min(origin.y, node.y)
	}
	return origin
}

// pasteAt puts the copied nodes down with their top left corner at a point.
func (m *model) pasteAt(x, y int) {
	if len(m.clipboardNodes) == 0 {
		return
	}
	m.pushUndo()

	origin := m.clipboardOrigin()
	newIDs := map[string]string{}
	first := len(m.editorNodes)
	for _, node := range copyNodes(m.clipboardNodes) {
		newID := m.nextNodeID()
		newIDs[node.id] = newID
		node.id = newID
		node.x += x - origin.x
		node.y += y - origin.y
		node.loaded = false
		m.editorNodes = append(m.editorNodes, node)
	}

	for _, conn := range m.clipboardConnections {
		conn.source, conn.target = newIDs[conn.source], newIDs[conn.target]
		m.editorConnections = append(m.editorConnections, conn)
	}

	indices := make([]int, 0, len(m.clipboardNodes))
	for i := first; i < len(m.editorNodes); i++ {
		indices = append(indices, i)
	}
	m.rebuildNodeRects()
	m.setSelection(indices)
	m.revealNode(first)
	m.markEditorDirty()
}

// pasteNearOriginal offsets each paste, so repeating it doesn't stack the copies.
func (m *model) pasteNearOriginal() {
	if len(m.clipboardNodes) == 0 {
		return
	}
	origin := m.clipboardOrigin()
	m.pasteCount++
	m.pasteAt(origin.x+pasteOffsetX*m.pasteCount, origin.y+pasteOffsetY*m.pasteCount)
}

func (m model) hasSelectedConnection() bool {
	return m.selectedConnection >= 0 && m.selectedConnection < len(m.editorConnections)
}

// connectionAt finds the wire at a screen position. A vertical wire is one narrow
// cell wide, so the cells beside it count too.
func (m model) connectionAt(x, y int) int {
	content := m.canvasContentRect()
	if !inside(x, y, content) {
		return -1
	}
	cx, cy := x-content.x, y-content.y

	for i := len(m.editorConnections) - 1; i >= 0; i-- {
		path, ok := m.connectionPath(m.editorConnections[i])
		if !ok {
			continue
		}
		for j := 0; j+1 < len(path); j++ {
			from, to := path[j], path[j+1]
			switch {
			case from.y == to.y && cy == from.y && cx >= min(from.x, to.x) && cx <= max(from.x, to.x):
				return i
			case from.x == to.x && abs(cx-from.x) <= 1 && cy >= min(from.y, to.y) && cy <= max(from.y, to.y):
				return i
			}
		}
	}
	return -1
}

func (m *model) selectConnection(index int) {
	m.clearSelection()
	m.selectedConnection = index
}

func (m *model) deleteSelectedConnection() {
	if !m.hasSelectedConnection() {
		return
	}
	m.pushUndo()
	i := m.selectedConnection
	m.editorConnections = append(m.editorConnections[:i:i], m.editorConnections[i+1:]...)
	m.selectedConnection = -1
	m.markEditorDirty()
}

func (m *model) deleteSelection() {
	if m.hasSelectedConnection() {
		m.deleteSelectedConnection()
		return
	}
	m.deleteSelectedEditorNode()
}

func (m model) canDeleteSelection() bool {
	if m.hasSelectedConnection() {
		return true
	}
	for _, index := range m.selectedIndices() {
		if !isStartKind(m.editorNodes[index].kind) {
			return true
		}
	}
	return false
}

func (m model) canCopySelection() bool {
	for _, index := range m.selectedIndices() {
		if !isStartKind(m.editorNodes[index].kind) {
			return true
		}
	}
	return false
}

func (m model) selectedConnectionCaption() string {
	conn := m.editorConnections[m.selectedConnection]
	source, _ := m.editorNodeByID(conn.source)
	target, _ := m.editorNodeByID(conn.target)
	label := source.label
	if output, _ := m.savedPorts(conn); len(m.nodeOutputs(source.kind)) > 1 {
		label += " (" + output + ")"
	}
	return fmt.Sprintf("%s → %s. del removes this connection.", label, target.label)
}

// contextMenuItems follows the web editor's menu, plus the two actions the web
// editor does with a double click and a drag.
func (m model) contextMenuItems() []editorMenuItem {
	items := []editorMenuItem{
		{label: "Undo", shortcut: "ctrl+z", action: editorActionUndo, disabled: len(m.undoStack) == 0},
		{label: "Redo", shortcut: "ctrl+y", action: editorActionRedo, disabled: len(m.redoStack) == 0},
		{separator: true},
	}

	if m.editorMenuTarget >= 0 && m.editorMenuTarget < len(m.editorNodes) && len(m.selectedIndices()) == 1 {
		items = append(items, editorMenuItem{label: "Edit", shortcut: "⏎", action: editorActionEdit})
		if len(m.nodeOutputs(m.editorNodes[m.editorMenuTarget].kind)) > 0 {
			items = append(items, editorMenuItem{label: "Connect", shortcut: "c", action: editorActionConnect})
		}
	}
	if m.canCopySelection() {
		items = append(items, editorMenuItem{label: "Copy", shortcut: "ctrl+c", action: editorActionCopy})
	}
	if len(m.clipboardNodes) > 0 {
		items = append(items, editorMenuItem{label: "Paste", shortcut: "ctrl+v", action: editorActionPaste})
	}
	if m.canDeleteSelection() {
		items = append(items, editorMenuItem{label: "Delete", shortcut: "del", action: editorActionDelete, danger: true})
	}

	if !items[len(items)-1].separator {
		items = append(items, editorMenuItem{separator: true})
	}
	items = append(items, editorMenuItem{label: "Create node", shortcut: "a", action: editorActionOpenCreate})
	return append(items, commentMenuItems()...)
}

func (item editorMenuItem) selectable() bool {
	return !item.separator && !item.disabled
}

// nextMenuItem moves past separators and disabled rows, and stays put at the ends.
func nextMenuItem(items []editorMenuItem, from, delta int) int {
	for i := from + delta; i >= 0 && i < len(items); i += delta {
		if items[i].selectable() {
			return i
		}
	}
	return from
}

func firstMenuItem(items []editorMenuItem) int {
	for i, item := range items {
		if item.selectable() {
			return i
		}
	}
	return 0
}
