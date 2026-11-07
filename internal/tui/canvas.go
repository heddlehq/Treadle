package tui

import (
	"math"
	"sort"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Nodes live in canvas coordinates, one unit per cell at 100%. What's on screen
// comes from projectNode, so everything drawn or hit-tested agrees at any zoom.
//
// A terminal can't scale text, so zoom scales positions and widths in steps.
var zoomLevels = []float64{0.5, 0.75, 1, 1.25, 1.5}

const (
	defaultZoomIndex = 2
	minProjectedNode = 9
	wheelRows        = 3
	wheelColumns     = 6
	fitMarginX       = 5
	fitMarginY       = 1
	revealMargin     = 3

	// The web editor positions nodes in pixels, and its nodes are 88px across. These
	// keep two nodes that don't touch here from touching there.
	pixelsPerColumn = 10
	pixelsPerRow    = 25
)

func (m model) zoom() float64 {
	return zoomLevels[clamp(m.zoomIndex, 0, len(zoomLevels)-1)]
}

func scaled(value int, factor float64) int {
	return int(math.Round(float64(value) * factor))
}

// canvasContentRect is the screen area the canvas draws into.
func (m model) canvasContentRect() rect {
	return rect{x: m.canvasRect.x + 2, y: m.canvasRect.y + 2, w: max(m.canvasRect.w-4, 1), h: max(m.canvasRect.h-3, 1)}
}

func (m model) toScreen(x, y int) (int, int) {
	return scaled(x-m.viewX, m.zoom()), scaled(y-m.viewY, m.zoom())
}

func (m model) toWorld(x, y int) (int, int) {
	return m.viewX + scaled(x, 1/m.zoom()), m.viewY + scaled(y, 1/m.zoom())
}

// projectNode returns a node in the canvas grid. Height stays put, because each
// output needs its own row. Comments have no outputs, so theirs scales too.
func (m model) projectNode(node editorNode) editorNode {
	node.x, node.y = m.toScreen(node.x, node.y)
	if m.zoom() != 1 {
		node.w = max(scaled(node.w, m.zoom()), minProjectedNode)
		if node.isComment() {
			node.h = max(scaled(node.h, m.zoom()), 1)
		}
	}
	return node
}

func (m *model) setZoomIndex(index, anchorX, anchorY int) {
	worldX, worldY := m.toWorld(anchorX, anchorY)
	m.zoomIndex = clamp(index, 0, len(zoomLevels)-1)
	m.viewX = worldX - scaled(anchorX, 1/m.zoom())
	m.viewY = worldY - scaled(anchorY, 1/m.zoom())
	m.rebuildNodeRects()
}

func (m *model) zoomCentered(index int) {
	content := m.canvasContentRect()
	m.setZoomIndex(index, content.w/2, content.h/2)
}

func (m *model) panBy(dx, dy int) {
	m.viewX += dx
	m.viewY += dy
	m.rebuildNodeRects()
}

// fitView picks the closest zoom that shows every node, and centres them.
func (m *model) fitView() {
	if len(m.editorNodes) == 0 {
		m.viewX, m.viewY, m.zoomIndex = 0, 0, defaultZoomIndex
		m.rebuildNodeRects()
		return
	}

	minX, minY := m.editorNodes[0].x, m.editorNodes[0].y
	maxX, maxY := minX, minY
	tallest := 0
	for _, node := range m.editorNodes {
		minX = min(minX, node.x)
		minY = min(minY, node.y)
		maxX = max(maxX, node.x+node.w)
		maxY = max(maxY, node.y)
		tallest = max(tallest, node.h)
	}

	content := m.canvasContentRect()
	index := 0
	for i := defaultZoomIndex; i >= 0; i-- {
		z := zoomLevels[i]
		if scaled(maxX-minX, z)+fitMarginX*2 <= content.w && scaled(maxY-minY, z)+tallest+fitMarginY*2 <= content.h {
			index = i
			break
		}
	}

	m.zoomIndex = index
	z := m.zoom()
	spareX := max(content.w-scaled(maxX-minX, z), 0) / 2
	spareY := max(content.h-scaled(maxY-minY, z)-tallest, 0) / 2
	m.viewX = minX - scaled(spareX, 1/z)
	m.viewY = minY - scaled(spareY, 1/z)
	m.rebuildNodeRects()
}

// revealNode scrolls just enough to bring a node into view.
func (m *model) revealNode(index int) {
	if index < 0 || index >= len(m.editorNodes) {
		return
	}

	node := m.projectNode(m.editorNodes[index])
	content := m.canvasContentRect()
	shiftX, shiftY := 0, 0
	switch {
	case node.x+node.w+revealMargin > content.w:
		shiftX = node.x + node.w + revealMargin - content.w
	case node.x < revealMargin:
		shiftX = node.x - revealMargin
	}
	switch {
	case node.y+node.h+1 > content.h:
		shiftY = node.y + node.h + 1 - content.h
	case node.y < 1:
		shiftY = node.y - 1
	}
	m.panBy(scaled(shiftX, 1/m.zoom()), scaled(shiftY, 1/m.zoom()))
}

func (m *model) handleCanvasWheel(mouse tea.Mouse) {
	content := m.canvasContentRect()
	if mouse.Mod.Contains(tea.ModCtrl) {
		step := 0
		switch mouse.Button {
		case tea.MouseWheelUp:
			step = 1
		case tea.MouseWheelDown:
			step = -1
		}
		m.setZoomIndex(m.zoomIndex+step, mouse.X-content.x, mouse.Y-content.y)
		return
	}

	rows := max(scaled(wheelRows, 1/m.zoom()), 1)
	columns := max(scaled(wheelColumns, 1/m.zoom()), 1)
	horizontal := mouse.Mod.Contains(tea.ModShift)
	switch {
	case mouse.Button == tea.MouseWheelLeft, mouse.Button == tea.MouseWheelUp && horizontal:
		m.panBy(-columns, 0)
	case mouse.Button == tea.MouseWheelRight, mouse.Button == tea.MouseWheelDown && horizontal:
		m.panBy(columns, 0)
	case mouse.Button == tea.MouseWheelUp:
		m.panBy(0, -rows)
	case mouse.Button == tea.MouseWheelDown:
		m.panBy(0, rows)
	}
}

func (m model) isNodeSelected(index int) bool {
	if index == m.selectedNode {
		return true
	}
	return index >= 0 && index < len(m.editorNodes) && m.extraSelection[m.editorNodes[index].id]
}

func (m model) selectedIndices() []int {
	indices := make([]int, 0, len(m.extraSelection)+1)
	for i := range m.editorNodes {
		if m.isNodeSelected(i) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m *model) selectOnly(index int) {
	m.selectedNode = index
	m.extraSelection = nil
	m.selectedConnection = -1
}

func (m *model) clearSelection() {
	m.selectOnly(-1)
}

// setSelection makes the first index the node the single-node actions act on.
func (m *model) setSelection(indices []int) {
	m.clearSelection()
	if len(indices) == 0 {
		return
	}

	sort.Ints(indices)
	m.selectedNode = indices[0]
	m.extraSelection = map[string]bool{}
	for _, index := range indices[1:] {
		m.extraSelection[m.editorNodes[index].id] = true
	}
}

func (m *model) toggleNodeSelection(index int) {
	selected := m.selectedIndices()
	next := make([]int, 0, len(selected)+1)
	for _, candidate := range selected {
		if candidate != index {
			next = append(next, candidate)
		}
	}
	if len(next) == len(selected) {
		next = append(next, index)
	}
	m.setSelection(next)
}

func (m *model) selectAllNodes() {
	indices := make([]int, len(m.editorNodes))
	for i := range indices {
		indices[i] = i
	}
	m.setSelection(indices)
}

func (m *model) beginNodeDrag(index, x, y int) {
	m.draggingNode = index
	m.dragMoved = false
	m.dragAnchorX, m.dragAnchorY = x, y
	m.dragOrigins = map[string]canvasPoint{}
	for _, selected := range m.selectedIndices() {
		node := m.editorNodes[selected]
		m.dragOrigins[node.id] = canvasPoint{node.x, node.y}
	}
}

func (m *model) dragNodes(x, y int) {
	dx := scaled(x-m.dragAnchorX, 1/m.zoom())
	dy := scaled(y-m.dragAnchorY, 1/m.zoom())

	moved := false
	for i := range m.editorNodes {
		node := m.editorNodes[i]
		origin, ok := m.dragOrigins[node.id]
		if !ok || (node.x == origin.x+dx && node.y == origin.y+dy) {
			continue
		}
		// A whole drag is one step of undo.
		if !m.dragMoved {
			m.pushUndo()
			m.dragMoved = true
		}
		m.editorNodes[i].x, m.editorNodes[i].y = origin.x+dx, origin.y+dy
		moved = true
	}
	if moved {
		m.rebuildNodeRects()
		m.markEditorDirty()
	}
}

func (m *model) beginMarquee(x, y int, additive bool) {
	m.marqueeActive = true
	m.marqueeAdditive = additive
	m.marqueeStartX, m.marqueeStartY = x, y
	m.marqueeX, m.marqueeY = x, y
}

func (m model) marqueeRect() rect {
	x, y := min(m.marqueeStartX, m.marqueeX), min(m.marqueeStartY, m.marqueeY)
	return rect{x: x, y: y, w: max(m.marqueeStartX, m.marqueeX) - x + 1, h: max(m.marqueeStartY, m.marqueeY) - y + 1}
}

func rectsOverlap(a, b rect) bool {
	return a.x < b.x+b.w && b.x < a.x+a.w && a.y < b.y+b.h && b.y < a.y+a.h
}

// finishMarquee selects every node the box touches. A click with no drag selects
// nothing, which is how clicking the canvas clears the selection.
func (m *model) finishMarquee() {
	m.marqueeActive = false
	if m.marqueeStartX == m.marqueeX && m.marqueeStartY == m.marqueeY {
		return
	}

	box := m.marqueeRect()
	indices := []int{}
	if m.marqueeAdditive {
		indices = m.selectedIndices()
	}
	for i, node := range m.editorNodes {
		if rectsOverlap(box, m.nodeRects[node.id]) && !containsInt(indices, i) {
			indices = append(indices, i)
		}
	}
	m.setSelection(indices)
}

func containsInt(values []int, value int) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (m model) drawMarquee(grid [][]canvasCell) {
	if !m.marqueeActive {
		return
	}

	content := m.canvasContentRect()
	box := m.marqueeRect()
	left, top := box.x-content.x, box.y-content.y
	right, bottom := left+box.w-1, top+box.h-1
	style := lipgloss.NewStyle().Foreground(accentColor)

	for x := left; x <= right; x++ {
		drawStyledRune(grid, x, top, '╌', &style)
		drawStyledRune(grid, x, bottom, '╌', &style)
	}
	for y := top; y <= bottom; y++ {
		drawStyledRune(grid, left, y, '╎', &style)
		drawStyledRune(grid, right, y, '╎', &style)
	}
	drawStyledRune(grid, left, top, '┌', &style)
	drawStyledRune(grid, right, top, '┐', &style)
	drawStyledRune(grid, left, bottom, '└', &style)
	drawStyledRune(grid, right, bottom, '┘', &style)
}
