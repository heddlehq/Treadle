package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Every key of the editor is said once, here. The footer shows the few that matter
// where the person is, the help (?) shows them all by group, and the command palette
// (ctrl+p) runs any of them by name, so nothing in the editor needs the mouse.
type keyBinding struct {
	keys   string
	label  string
	group  string
	action editorAction
	// footer is where it goes in the footer of the canvas, from 1; 0 keeps it out.
	footer int
	// when says whether it can be used now. Nil is always.
	when func(model) bool
}

const (
	groupMove   = "Move around"
	groupEdit   = "Edit"
	groupFlow   = "Flow"
	groupView   = "View"
	groupPanels = "Panels"
)

var keyGroups = []string{groupMove, groupEdit, groupFlow, groupView, groupPanels}

const (
	editorActionRun        editorAction = "run"
	editorActionSave       editorAction = "save"
	editorActionResult     editorAction = "result"
	editorActionFit        editorAction = "fit"
	editorActionZoomIn     editorAction = "zoom_in"
	editorActionZoomOut    editorAction = "zoom_out"
	editorActionZoomReset  editorAction = "zoom_reset"
	editorActionNextNode   editorAction = "next_node"
	editorActionPrevNode   editorAction = "prev_node"
	editorActionGoTo       editorAction = "go_to"
	editorActionSelectAll  editorAction = "select_all"
	editorActionWires      editorAction = "wires"
	editorActionAgent      editorAction = "agent"
	editorActionNodesPanel editorAction = "nodes_panel"
	editorActionHelp       editorAction = "help"

	editorActionTerminal      editorAction = "terminal"
	editorActionTerminalWide  editorAction = "terminal_wide"
	editorActionTerminalClose editorAction = "terminal_close"
)

func hasNode(m model) bool      { return m.hasSelectedEditorNode() }
func hasSelection(m model) bool { return len(m.selectedIndices()) > 0 || m.hasSelectedConnection() }
func hasConnections(m model) bool {
	return m.hasSelectedEditorNode() && len(m.wiresOf(m.selectedNode)) > 0
}

func (m model) keyBindings() []keyBinding {
	return []keyBinding{
		{keys: "←↑↓→  hjkl", label: "Go to the nearest node that way", group: groupMove, footer: 1},
		{keys: "[  ]", label: "Previous and next node", group: groupMove},
		{keys: "g", label: "Go to a node by name", group: groupMove, action: editorActionGoTo},
		{keys: "w", label: "Walk the wires of the node", group: groupMove, action: editorActionWires, when: hasConnections},
		{keys: "ctrl+←↑↓→", label: "Scroll the canvas", group: groupMove},
		{keys: "ctrl+a", label: "Select every node", group: groupMove, action: editorActionSelectAll},
		{keys: "esc", label: "Clear the selection, then back to workflows", group: groupMove},

		{keys: "⏎", label: "Edit the node", group: groupEdit, action: editorActionEdit, footer: 2, when: hasNode},
		{keys: "a", label: "Add a node, after the selected one", group: groupEdit, action: editorActionOpenCreate, footer: 3},
		{keys: "c", label: "Connect from the node", group: groupEdit, action: editorActionConnect, footer: 4, when: hasNode},
		{keys: "shift+←↑↓→  HJKL", label: "Move the selection", group: groupEdit},
		{keys: "n", label: "Add a note", group: groupEdit, action: editorActionAddComment},
		{keys: "del", label: "Delete the selection", group: groupEdit, action: editorActionDelete, when: func(m model) bool { return m.canDeleteSelection() }},
		{keys: "ctrl+c", label: "Copy", group: groupEdit, action: editorActionCopy, when: func(m model) bool { return m.canCopySelection() }},
		{keys: "ctrl+v", label: "Paste", group: groupEdit, action: editorActionPaste, when: func(m model) bool { return len(m.clipboardNodes) > 0 }},
		{keys: "ctrl+z", label: "Undo", group: groupEdit, action: editorActionUndo, when: func(m model) bool { return len(m.undoStack) > 0 }},
		{keys: "ctrl+y", label: "Redo", group: groupEdit, action: editorActionRedo, when: func(m model) bool { return len(m.redoStack) > 0 }},

		{keys: "ctrl+r", label: "Run the flow", group: groupFlow, action: editorActionRun, footer: 5},
		{keys: "o", label: "Open the result of the last run", group: groupFlow, action: editorActionResult, when: func(m model) bool { return m.editorExecution != nil && m.editorExecution.settled() }},
		{keys: "ctrl+s", label: "Save", group: groupFlow, action: editorActionSave},

		{keys: "f", label: "Fit the flow on screen", group: groupView, action: editorActionFit},
		{keys: "+", label: "Zoom in", group: groupView, action: editorActionZoomIn},
		{keys: "-", label: "Zoom out", group: groupView, action: editorActionZoomOut},
		{keys: "0", label: "Actual size", group: groupView, action: editorActionZoomReset},

		{keys: "/", label: "Talk to the agent", group: groupPanels, action: editorActionAgent, footer: 6},
		{keys: "t", label: "Terminal: Claude Code on this flow", group: groupPanels, action: editorActionTerminal, footer: 7},
		{keys: "T", label: "Terminal across the whole editor, or beside the canvas", group: groupPanels, action: editorActionTerminalWide, when: func(m model) bool { return m.terminalOpen }},
		{keys: terminalLeave, label: "Leave the Terminal, back to the canvas", group: groupPanels},
		{keys: "", label: "Close the Terminal", group: groupPanels, action: editorActionTerminalClose, when: func(m model) bool { return m.terminalOpen }},
		{keys: "tab", label: "Next panel: canvas, nodes, agent", group: groupPanels},
		{keys: "ctrl+p", label: "Commands", group: groupPanels},
		{keys: "?", label: "Every key", group: groupPanels, action: editorActionHelp},
		{keys: "q", label: "Quit Treadle", group: groupPanels},
	}
}

// footerHint is how a key reads in the footer: short, since the help says the rest.
func (b keyBinding) footerHint() string {
	keys := b.keys
	if head, _, found := strings.Cut(keys, "  "); found {
		keys = head
	}
	switch b.action {
	case "":
		if b.footer == 1 {
			return keys + " nodes"
		}
	case editorActionEdit:
		return keys + " edit"
	case editorActionOpenCreate:
		return keys + " add node"
	case editorActionConnect:
		return keys + " connect"
	case editorActionRun:
		return keys + " run"
	case editorActionTerminal:
		return keys + " terminal"
	}
	return keys + " " + strings.ToLower(b.label)
}

func (b keyBinding) available(m model) bool {
	return b.when == nil || b.when(m)
}

// canvasFooterHints are the keys of the canvas that can be used now, in their order.
func (m model) canvasFooterHints() []string {
	var picked []keyBinding
	for _, binding := range m.keyBindings() {
		if binding.footer > 0 && binding.available(m) {
			picked = append(picked, binding)
		}
	}
	for i := 1; i < len(picked); i++ {
		for j := i; j > 0 && picked[j].footer < picked[j-1].footer; j-- {
			picked[j], picked[j-1] = picked[j-1], picked[j]
		}
	}

	hints := make([]string, 0, len(picked))
	for _, binding := range picked {
		if binding.action == editorActionAgent {
			hints = append(hints, m.agentHint())
			continue
		}
		hints = append(hints, binding.footerHint())
	}
	return hints
}

// renderFooter puts what can be done here on the left and the way to everything else
// on the right. What doesn't fit on the left is left out: the help has it.
func renderFooter(left []string, right string, width int) string {
	const margin, gap = 2, 3
	join := func(hints []string) (string, int) {
		parts := make([]string, 0, len(hints))
		for _, hint := range hints {
			if hint = strings.TrimSpace(hint); hint != "" {
				parts = append(parts, styledHint(hint))
			}
		}
		row := strings.Join(parts, blank(gap))
		return row, lipgloss.Width(row)
	}

	rightHints := strings.Split(right, "  ")
	rightRow, rightWidth := join(rightHints)
	for len(rightHints) > 0 && margin*2+rightWidth > width {
		rightHints = rightHints[1:]
		rightRow, rightWidth = join(rightHints)
	}

	kept := left
	leftRow, leftWidth := join(kept)
	for len(kept) > 0 && margin*2+leftWidth+gap+rightWidth > width {
		kept = kept[:len(kept)-1]
		leftRow, leftWidth = join(kept)
	}

	fill := max(width-margin*2-leftWidth-rightWidth, 0)
	return blank(margin) + leftRow + blank(fill) + rightRow + blank(max(width-margin-leftWidth-fill-rightWidth, 0))
}

// styledHint is a key, bright, and what it does, muted.
func styledHint(hint string) string {
	name, rest, found := strings.Cut(hint, " ")
	row := lipgloss.NewStyle().Foreground(textColor).Render(name)
	if found {
		row += lipgloss.NewStyle().Foreground(mutedTextColor).Render(" " + rest)
	}
	return row
}

// paletteCommands are the actions the command palette offers right now.
func (m model) paletteCommands() []editorMenuItem {
	var items []editorMenuItem
	for _, binding := range m.keyBindings() {
		if binding.action == "" || !binding.available(m) {
			continue
		}
		items = append(items, editorMenuItem{label: binding.label, shortcut: binding.keys, action: binding.action})
	}
	items = append(items, editorMenuItem{label: "Back to workflows", shortcut: "esc", action: editorActionBack})
	return items
}

// paletteItems are the commands, and once something is typed, the nodes of the flow to
// go to and the node types to add, all narrowed down to what matches.
func (m model) paletteItems() []editorMenuItem {
	if m.editorOverlay == editorOverlayCreate {
		return filterItems(m.editorCreateMenuItems(), m.paletteQuery)
	}
	if m.editorOverlay == editorOverlayGoTo {
		return filterItems(m.goToItems(), m.paletteQuery)
	}

	items := m.paletteCommands()
	if strings.TrimSpace(m.paletteQuery) != "" {
		items = append(items, m.goToItems()...)
		items = append(items, m.editorCreateMenuItems()...)
	}
	return filterItems(items, m.paletteQuery)
}

func (m model) goToItems() []editorMenuItem {
	items := make([]editorMenuItem, 0, len(m.editorNodes))
	for _, node := range m.editorNodes {
		label := node.label
		if node.isComment() {
			label = node.commentLabel()
		}
		items = append(items, editorMenuItem{label: "Go to " + label, shortcut: m.nodeTypeName(node.kind), action: editorActionGoTo, kind: node.id})
	}
	return items
}

// filterItems keeps the items that have every word typed, in any order. Those with a
// word that starts with what was typed come first: "lo" finds Logger before Flow.
func filterItems(items []editorMenuItem, query string) []editorMenuItem {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return items
	}

	type match struct {
		item  editorMenuItem
		score int
	}
	var matches []match
	for _, item := range items {
		text := strings.ToLower(item.label + " " + item.shortcut)
		labelWords := strings.FieldsFunc(strings.ToLower(item.label), func(r rune) bool {
			return r == ' ' || r == ':' || r == ',' || r == '.'
		})
		score, found := 0, true
		for _, word := range words {
			if !strings.Contains(text, word) {
				found = false
				break
			}
			if !anyHasPrefix(labelWords, word) {
				score++
			}
		}
		if found {
			matches = append(matches, match{item, score})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score < matches[j].score })
	kept := make([]editorMenuItem, len(matches))
	for i, found := range matches {
		kept[i] = found.item
	}
	return kept
}

func anyHasPrefix(words []string, prefix string) bool {
	for _, word := range words {
		if strings.HasPrefix(word, prefix) {
			return true
		}
	}
	return false
}

// updatePaletteKey types into the search of a palette, moves through what it found,
// and runs the chosen item.
func (m model) updatePaletteKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	items := m.paletteItems()
	switch msg.String() {
	case "esc":
		m.closeEditorOverlay()
		return m, nil
	case "up", "ctrl+k":
		m.editorMenuSelected = nextMenuItem(items, m.editorMenuSelected, -1)
		return m, nil
	case "down", "ctrl+j":
		m.editorMenuSelected = nextMenuItem(items, m.editorMenuSelected, 1)
		return m, nil
	case "backspace":
		if query := []rune(m.paletteQuery); len(query) > 0 {
			m.paletteQuery = string(query[:len(query)-1])
			m.editorMenuSelected = firstMenuItem(m.paletteItems())
		}
		return m, nil
	case "ctrl+u":
		m.paletteQuery = ""
		m.editorMenuSelected = firstMenuItem(m.paletteItems())
		return m, nil
	}
	if msg.Code == tea.KeyEnter {
		if m.editorMenuSelected >= 0 && m.editorMenuSelected < len(items) {
			cmd := m.runEditorAction(items[m.editorMenuSelected])
			return m, cmd
		}
		return m, nil
	}
	if msg.Text != "" && msg.Mod&^tea.ModShift == 0 {
		m.paletteQuery += msg.Text
		m.editorMenuSelected = firstMenuItem(m.paletteItems())
	}
	return m, nil
}

// helpLines are every key of the editor, by group.
func (m model) helpLines(width int) []string {
	keyStyle := lipgloss.NewStyle().Foreground(textColor).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(mutedTextColor)
	groupStyle := lipgloss.NewStyle().Foreground(accentColor).Bold(true)

	keyWidth := 0
	for _, binding := range m.keyBindings() {
		keyWidth = max(keyWidth, lipgloss.Width(binding.keys))
	}

	var lines []string
	for _, group := range keyGroups {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, groupStyle.Render(group))
		for _, binding := range m.keyBindings() {
			if binding.group != group {
				continue
			}
			keys := keyStyle.Render(binding.keys) + blank(keyWidth-lipgloss.Width(binding.keys)+3)
			lines = append(lines, keys+labelStyle.Render(truncatePlain(binding.label, max(width-keyWidth-3, 8))))
		}
	}
	return lines
}

func (m model) helpRect() (int, int) {
	return min(78, max(m.width-6, 30)), max(m.height-4, 10)
}

// helpRows are the keys that fit in a help this tall: the border, its title and the
// hint at the bottom take five rows.
func helpRows(height int) int {
	return max(height-5, 1)
}

func (m model) renderHelp() string {
	width, height := m.helpRect()
	lines := m.helpLines(width - 4)
	rows := helpRows(height)
	offset := clampScroll(m.helpScroll, len(lines), rows)
	visible := lines[offset:min(offset+rows, len(lines))]

	hint := "ctrl+p run a command  esc close"
	if len(lines) > rows {
		hint = "↑↓ scroll  " + hint
	}
	body := append(append([]string{}, visible...), "", lipgloss.NewStyle().Foreground(mutedTextColor).Render(hint))
	panel := renderSectionPanel("Keys", body, width, min(height, len(body)+3))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

func (m model) updateHelpKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	width, height := m.helpRect()
	total := len(m.helpLines(width - 4))
	rows := helpRows(height)
	switch msg.String() {
	case "esc", "?", "q":
		m.editorOverlay = editorOverlayNone
	case "up", "k":
		m.helpScroll = clampScroll(m.helpScroll-1, total, rows)
	case "down", "j":
		m.helpScroll = clampScroll(m.helpScroll+1, total, rows)
	case "pgup":
		m.helpScroll = clampScroll(m.helpScroll-rows, total, rows)
	case "pgdown", "space":
		m.helpScroll = clampScroll(m.helpScroll+rows, total, rows)
	case "ctrl+p":
		m.editorOverlay = editorOverlayNone
		m.openWorkflowCommandPalette()
	}
	return m, nil
}
