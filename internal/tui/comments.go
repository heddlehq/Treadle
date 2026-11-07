package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A comment is what the web editor lets you write on the canvas: a title, a text, a
// note or a section. It lives among the nodes so selecting, moving, undoing and
// copying treat it like one, but the server keeps it apart and it never runs.
const (
	commentNodeKind = "__comment"

	commentTitle   = "title"
	commentText    = "text"
	commentNote    = "note"
	commentSection = "section"

	colorDefault = "default"

	minCommentColumns = 12

	// A form field holds one line, so a line break is typed as these two characters.
	commentLineBreak = `\n`
)

const (
	layerNode = iota
	layerComment
	layerSection
)

var commentDefinition = apiNodeDef{
	Type:        commentNodeKind,
	Title:       "Comment",
	Description: `Written on the canvas, it never runs. \n breaks a line.`,
	Icon:        "hero-document-text",
}

// The sizes of a new comment match the web editor's, in its pixels.
var commentKinds = []struct {
	kind        string
	label       string
	placeholder string
	width       float64
	height      float64
}{
	{commentTitle, "Title", "Add a title", 520, 64},
	{commentText, "Text", "Write something", 280, 96},
	{commentNote, "Note", "Write something", 240, 140},
	{commentSection, "Section", "Name this section", 520, 320},
}

// The dark theme of the web editor, from rete_custom.css.
var commentColors = []struct {
	name       string
	label      string
	ink        color.Color
	background color.Color
}{
	{colorDefault, "Default", textColor, neutralColor},
	{"yellow", "Yellow", lipgloss.Color("#e6c976"), lipgloss.Color("#3d3620")},
	{"green", "Green", lipgloss.Color("#9fd0b3"), lipgloss.Color("#24382c")},
	{"blue", "Blue", lipgloss.Color("#9fcbd9"), lipgloss.Color("#213640")},
	{"red", "Red", lipgloss.Color("#e8a597"), lipgloss.Color("#402722")},
}

func commentControls(kind, colorName, text string) []nodeControl {
	kinds := make([]controlOption, 0, len(commentKinds))
	for _, item := range commentKinds {
		kinds = append(kinds, controlOption{label: item.label, value: item.kind})
	}
	colors := make([]controlOption, 0, len(commentColors))
	for _, item := range commentColors {
		colors = append(colors, controlOption{label: item.label, value: item.name})
	}

	return []nodeControl{
		{name: "text", label: "Text", value: strings.ReplaceAll(text, "\n", commentLineBreak), kind: controlText},
		{name: "kind", label: "Type", value: kind, kind: controlSelect, options: kinds},
		{name: "color", label: "Color", value: colorName, kind: controlSelect, options: colors},
	}
}

func defaultCommentColor(kind string) string {
	if kind == commentNote {
		return "yellow"
	}
	return colorDefault
}

func commentCells(width, height float64) (int, int) {
	return max(int(math.Round(width/pixelsPerColumn)), minCommentColumns), max(int(math.Round(height/pixelsPerRow)), 1)
}

func commentFromAPI(item apiComment, index int) editorNode {
	node := editorNode{
		id:       apiRefString(item.ID),
		kind:     commentNodeKind,
		x:        int(math.Round(item.Position.X / pixelsPerColumn)),
		y:        int(math.Round(item.Position.Y / pixelsPerRow)),
		pxWidth:  item.Width,
		pxHeight: item.Height,
		controls: commentControls(item.Kind, item.Color, item.Text),
	}
	node.w, node.h = commentCells(item.Width, item.Height)
	node.label = node.commentLabel()
	node.loaded = true
	node.loadedAt = canvasPoint{node.x, node.y}
	node.loadedPos = item.Position
	if node.id == "" {
		node.id = fmt.Sprintf("comment_%d", index+1)
	}
	return node
}

func (n editorNode) isComment() bool {
	return n.kind == commentNodeKind
}

func (n editorNode) layer() int {
	switch {
	case !n.isComment():
		return layerNode
	case n.commentKind() == commentSection:
		return layerSection
	default:
		return layerComment
	}
}

func (n editorNode) commentField(name string) string {
	for _, control := range n.controls {
		if control.name == name {
			return control.value
		}
	}
	return ""
}

func (n editorNode) commentKind() string {
	return n.commentField("kind")
}

func (n editorNode) commentBody() string {
	return strings.ReplaceAll(n.commentField("text"), commentLineBreak, "\n")
}

func (n editorNode) commentLabel() string {
	for _, item := range commentKinds {
		if item.kind == n.commentKind() {
			return item.label
		}
	}
	return commentDefinition.Title
}

func (n editorNode) commentPlaceholder() string {
	for _, item := range commentKinds {
		if item.kind == n.commentKind() {
			return item.placeholder
		}
	}
	return ""
}

func (n editorNode) commentPalette() (color.Color, color.Color) {
	for _, item := range commentColors {
		if item.name == n.commentField("color") {
			return item.ink, item.background
		}
	}
	return textColor, neutralColor
}

func (n editorNode) apiComment() apiComment {
	return apiComment{
		ID:       n.id,
		Kind:     n.commentKind(),
		Color:    n.commentField("color"),
		Text:     n.commentBody(),
		Position: n.savedPosition(),
		Width:    n.pxWidth,
		Height:   n.pxHeight,
	}
}

// commentLines wraps the text to fit inside a comment of this width.
func commentLines(text string, width int) []string {
	return strings.Split(ansi.Wrap(text, max(width, 1), ""), "\n")
}

// commentPadding is how many columns and rows a kind of comment spends on its frame.
func commentPadding(kind string) (int, int) {
	switch kind {
	case commentNote, commentSection:
		return 4, 2
	case commentTitle:
		return 0, 1
	default:
		return 0, 0
	}
}

// applyCommentForm grows the comment to show what was typed, as the web editor does.
func (n *editorNode) applyCommentForm() {
	n.label = n.commentLabel()

	columns, rows := commentPadding(n.commentKind())
	if needed := len(commentLines(n.commentBody(), n.w-columns)) + rows; needed > n.h {
		n.h = needed
		n.pxHeight = float64(needed * pixelsPerRow)
	}
}

func (m model) flowNodeCount() int {
	count := 0
	for _, node := range m.editorNodes {
		if !node.isComment() {
			count++
		}
	}
	return count
}

// wireOverComment lets a click reach a wire drawn on top of a comment.
func (m model) wireOverComment(index, x, y int) bool {
	return m.editorNodes[index].isComment() && m.connectionAt(x, y) >= 0
}

func commentMenuItems() []editorMenuItem {
	items := make([]editorMenuItem, 0, len(commentKinds))
	for _, item := range commentKinds {
		menuItem := editorMenuItem{label: "Add " + strings.ToLower(item.label), action: editorActionAddComment, kind: item.kind}
		if item.kind == commentNote {
			menuItem.shortcut = "n"
		}
		items = append(items, menuItem)
	}
	return items
}

// newCommentSpot is above the middle of the canvas, clear of a flow that was just fit.
func (m model) newCommentSpot() (int, int) {
	content := m.canvasContentRect()
	return m.toWorld(content.w/4, content.h/4)
}

// addComment opens the form right away, since a comment is there to be written.
func (m *model) addComment(kind string, x, y int) {
	m.pushUndo()

	node := editorNode{id: m.nextNodeID(), kind: commentNodeKind, x: x, y: y}
	node.controls = commentControls(kind, defaultCommentColor(kind), "")
	node.label = node.commentLabel()
	for _, item := range commentKinds {
		if item.kind == kind {
			node.pxWidth, node.pxHeight = item.width, item.height
		}
	}
	node.w, node.h = commentCells(node.pxWidth, node.pxHeight)

	m.editorNodes = append(m.editorNodes, node)
	m.selectOnly(len(m.editorNodes) - 1)
	m.rebuildNodeRects()
	m.markEditorDirty()
	m.openNodeModal()
}

// drawComments draws under the wires and nodes, and sections under other comments.
func (m model) drawComments(grid [][]canvasCell) {
	for _, layer := range []int{layerSection, layerComment} {
		for i, node := range m.editorNodes {
			if node.layer() == layer {
				m.drawComment(grid, i, m.projectNode(node))
			}
		}
	}
}

func (m model) drawComment(grid [][]canvasCell, index int, node editorNode) {
	ink, background := node.commentPalette()
	selected := m.isNodeSelected(index)

	text := lipgloss.NewStyle().Foreground(ink)
	frame := text
	if selected {
		frame = frame.Foreground(focusedBorderColor)
	}

	kind := node.commentKind()
	switch {
	case kind == commentNote:
		text = text.Foreground(textColor).Background(background)
		frame = frame.Background(background)
		fillComment(grid, node, &text)
		drawCommentFrame(grid, node, []rune("╭╮╰╯─│"), &frame)
	case kind == commentSection:
		drawCommentFrame(grid, node, []rune("┌┐└┘╌╎"), &frame)
	case selected:
		text = text.Background(neutralColor)
		fillComment(grid, node, &text)
	}
	if kind == commentTitle || kind == commentSection {
		text = text.Bold(true)
	}

	m.drawCommentText(grid, node, text)
}

func fillComment(grid [][]canvasCell, node editorNode, style *lipgloss.Style) {
	for y := node.y; y < node.y+node.h; y++ {
		for x := node.x; x < node.x+node.w; x++ {
			drawStyledRune(grid, x, y, ' ', style)
		}
	}
}

// drawCommentFrame takes the four corners, then the horizontal and vertical sides.
func drawCommentFrame(grid [][]canvasCell, node editorNode, runes []rune, style *lipgloss.Style) {
	right, bottom := node.x+node.w-1, node.y+node.h-1
	for x := node.x + 1; x < right; x++ {
		drawStyledRune(grid, x, node.y, runes[4], style)
		drawStyledRune(grid, x, bottom, runes[4], style)
	}
	for y := node.y + 1; y < bottom; y++ {
		drawStyledRune(grid, node.x, y, runes[5], style)
		drawStyledRune(grid, right, y, runes[5], style)
	}
	drawStyledRune(grid, node.x, node.y, runes[0], style)
	drawStyledRune(grid, right, node.y, runes[1], style)
	drawStyledRune(grid, node.x, bottom, runes[2], style)
	drawStyledRune(grid, right, bottom, runes[3], style)
}

func (m model) drawCommentText(grid [][]canvasCell, node editorNode, style lipgloss.Style) {
	kind := node.commentKind()
	columns, rows := commentPadding(kind)
	body := node.commentBody()
	if body == "" {
		body = node.commentPlaceholder()
		style = style.Faint(true)
	}

	left, top := node.x, node.y
	if columns > 0 {
		left, top = node.x+columns/2, node.y+rows/2
	}

	lines := commentLines(body, node.w-columns)
	lines = lines[:min(len(lines), max(node.h-rows, 1))]
	widest := 0
	for row, line := range lines {
		runes := []rune(line)
		widest = max(widest, len(runes))
		for i, r := range runes {
			drawStyledRune(grid, left+i, top+row, r, &style)
		}
	}

	if kind == commentTitle {
		rule := style.UnsetBold().UnsetFaint()
		for i := range widest {
			drawStyledRune(grid, left+i, top+len(lines), '━', &rule)
		}
	}
}
