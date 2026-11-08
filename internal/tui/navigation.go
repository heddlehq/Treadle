package tui

import "math"

// A cell is about twice as tall as it is wide, so a row counts as two columns when
// telling which node is nearest.
const rowWeight = 2

// selectToward selects the nearest node in a direction from the selected one, the
// way arrows move between the cells of a sheet. A node straight that way wins over a
// closer one off to the side. With nothing selected, it starts from the node nearest
// the middle of the view.
func (m *model) selectToward(dx, dy int) {
	if len(m.editorNodes) == 0 {
		return
	}
	if !m.hasSelectedEditorNode() {
		m.selectOnly(m.nodeNearestView())
		m.revealNode(m.selectedNode)
		return
	}

	fromX, fromY := nodeCenter(m.editorNodes[m.selectedNode])
	best, bestScore := -1, math.MaxFloat64
	for i, node := range m.editorNodes {
		if i == m.selectedNode {
			continue
		}
		x, y := nodeCenter(node)
		ahead := (x-fromX)*float64(dx) + (y-fromY)*float64(dy)*rowWeight
		aside := math.Abs((x-fromX)*float64(dy)) + math.Abs((y-fromY)*float64(dx))*rowWeight
		if ahead <= 0 {
			continue
		}
		// Off to the side costs more than ahead, so a row of nodes reads in order.
		if score := ahead + aside*2; score < bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return
	}
	m.selectOnly(best)
	m.revealNode(best)
}

func nodeCenter(node editorNode) (float64, float64) {
	return float64(node.x) + float64(node.w)/2, float64(node.y) + float64(node.h)/2
}

func (m model) nodeNearestView() int {
	content := m.canvasContentRect()
	cx, cy := m.toWorld(content.w/2, content.h/2)
	best, bestDistance := 0, math.MaxFloat64
	for i, node := range m.editorNodes {
		x, y := nodeCenter(node)
		if d := math.Hypot(x-float64(cx), (y-float64(cy))*rowWeight); d < bestDistance {
			best, bestDistance = i, d
		}
	}
	return best
}

// wiresOf are the connections that leave or reach a node: what leaves it first, in
// the order they run, then what reaches it.
func (m model) wiresOf(index int) []int {
	if index < 0 || index >= len(m.editorNodes) {
		return nil
	}
	id := m.editorNodes[index].id
	var leaving, reaching []int
	for i, conn := range m.editorConnections {
		switch {
		case conn.source == id:
			leaving = append(leaving, i)
		case conn.target == id:
			reaching = append(reaching, i)
		}
	}
	return append(leaving, reaching...)
}

// walkWires selects the wires of the selected node one after another, so a wire can be
// read or deleted without the mouse. Past the last one it goes back to the node.
func (m *model) walkWires() {
	node := m.selectedNode
	if m.hasSelectedConnection() {
		node = m.wireNode
		// A wire picked with the mouse: go on from it, along the node it leaves.
		if wires := m.wiresOf(node); m.wireCursor >= len(wires) || wires[m.wireCursor] != m.selectedConnection {
			node, _ = m.editorNodeIndex(m.editorConnections[m.selectedConnection].source)
			m.wireCursor = indexOf(m.wiresOf(node), m.selectedConnection)
		}
	}
	wires := m.wiresOf(node)
	if len(wires) == 0 {
		return
	}

	next := 0
	if m.hasSelectedConnection() {
		next = m.wireCursor + 1
	}
	if next >= len(wires) {
		m.selectOnly(node)
		return
	}

	m.selectConnection(wires[next])
	m.wireNode, m.wireCursor = node, next
}

// goToNode selects a node by its id and brings it into view.
func (m *model) goToNode(id string) {
	for i, node := range m.editorNodes {
		if node.id == id {
			m.selectOnly(i)
			m.revealNode(i)
			return
		}
	}
}

// arrowDirection reads the keys that go from node to node.
func arrowDirection(key string) (int, int, bool) {
	switch key {
	case "left", "h":
		return -1, 0, true
	case "right", "l":
		return 1, 0, true
	case "up", "k":
		return 0, -1, true
	case "down", "j":
		return 0, 1, true
	}
	return 0, 0, false
}

// moveDirection reads the keys that move what is selected.
func moveDirection(key string) (int, int, bool) {
	switch key {
	case "shift+left", "shift+h", "H":
		return -1, 0, true
	case "shift+right", "shift+l", "L":
		return 1, 0, true
	case "shift+up", "shift+k", "K":
		return 0, -1, true
	case "shift+down", "shift+j", "J":
		return 0, 1, true
	}
	return 0, 0, false
}

func (m *model) openHelp() {
	m.closeEditorOverlay()
	m.editorOverlay = editorOverlayHelp
	m.helpScroll = 0
}

func (m *model) openGoTo() {
	m.openWorkflowCommandPalette()
	m.editorOverlay = editorOverlayGoTo
}

func (m model) editorNodeIndex(id string) (int, bool) {
	for i, node := range m.editorNodes {
		if node.id == id {
			return i, true
		}
	}
	return -1, false
}

func indexOf(values []int, value int) int {
	for i, candidate := range values {
		if candidate == value {
			return i
		}
	}
	return -1
}
