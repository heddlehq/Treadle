package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	nodeFormWidth     = 56
	nodeFormRoomy     = 24
	controlText       = "text"
	controlSelect     = "select"
	multilineHint     = "Has several lines. Edit it in the web editor."
	nodeFormHints     = "tab next  ⏎ save  esc cancel"
	nodeFormPickHints = "tab next  ←→ choose  ⏎ save  esc cancel"
)

type controlOption struct {
	label string
	value string
}

// apiUIField describes one field of a node's form, as the server defines it.
type apiUIField struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Label   string `json:"label"`
	Options []any  `json:"options"`
	Default any    `json:"default"`
}

func controlOptions(raw []any) []controlOption {
	options := make([]controlOption, 0, len(raw))
	for _, item := range raw {
		switch option := item.(type) {
		case string:
			options = append(options, controlOption{label: option, value: option})
		case map[string]any:
			value := controlValue(option["value"])
			options = append(options, controlOption{label: fallbackString(controlValue(option["label"]), value), value: value})
		}
	}
	return options
}

// controlValue keeps structured values readable as JSON instead of Go's map syntax.
func controlValue(raw any) string {
	switch value := raw.(type) {
	case nil:
		return ""
	case string:
		return value
	case map[string]any, []any:
		if encoded, err := json.Marshal(value); err == nil {
			return string(encoded)
		}
	}
	return fmt.Sprintf("%v", raw)
}

func controlFromField(field apiUIField) nodeControl {
	control := nodeControl{
		name:  field.Name,
		label: fallbackString(field.Label, titleizeControlName(field.Name)),
		value: controlValue(field.Default),
		kind:  controlText,
	}
	if field.Type == controlSelect {
		control.kind = controlSelect
		control.options = controlOptions(field.Options)
	}
	return control
}

// nodeControls builds a node's form from what the server says it has.
func (m model) nodeControls(kind string) []nodeControl {
	def, ok := m.nodeDefinition(kind)
	if !ok || len(def.UIFields) == 0 {
		return defaultControls(kind)
	}

	controls := []nodeControl{{name: "label", label: "Label", value: def.Title, kind: controlText}}
	for _, field := range def.UIFields {
		if field.Name != "" {
			controls = append(controls, controlFromField(field))
		}
	}
	return controls
}

func (c nodeControl) editable() bool {
	return c.kind != controlSelect && !strings.Contains(c.value, "\n")
}

func (c nodeControl) optionIndex() int {
	for i, option := range c.options {
		if option.value == c.value {
			return i
		}
	}
	return -1
}

func (c nodeControl) displayValue() string {
	if index := c.optionIndex(); index >= 0 {
		return c.options[index].label
	}
	return c.value
}

// nodeFormLayout places the form. Rendering and mouse handling both read it, so
// what is drawn and what is clickable can't drift apart.
type nodeFormLayout struct {
	compact  bool
	x        int
	width    int
	titleY   int
	first    int
	fields   []rect
	save     rect
	cancel   rect
	scrolled bool
}

func newNodeFormLayout(width, height, fieldCount, focused int) nodeFormLayout {
	body := max(height-footerHeight, 1)
	compact := height < nodeFormRoomy

	fieldRows, buttonHeight := connectFieldHeight+1, 3
	if compact {
		fieldRows, buttonHeight = connectFieldHeight, 1
	}

	// Title, description and a blank row above; the save and cancel rows below.
	overhead := 3 + buttonHeight + 1
	visible := clamp((body-overhead)/fieldRows, 1, max(fieldCount, 1))
	first := clamp(focused-visible/2, 0, max(fieldCount-visible, 0))

	formWidth := min(nodeFormWidth, max(width-8, 1))
	l := nodeFormLayout{
		compact:  compact,
		x:        max((width-formWidth)/2, 0),
		width:    formWidth,
		first:    first,
		scrolled: visible < fieldCount,
	}

	total := overhead + visible*fieldRows
	l.titleY = max((body-total)/2, 0)

	y := l.titleY + 3
	for range min(visible, fieldCount) {
		l.fields = append(l.fields, rect{x: l.x, y: y, w: formWidth, h: connectFieldHeight})
		y += fieldRows
	}
	l.save = rect{x: l.x, y: y, w: formWidth, h: buttonHeight}
	l.cancel = rect{x: l.x, y: y + buttonHeight, w: formWidth, h: 1}
	return l
}

func (m model) nodeForm() nodeFormLayout {
	return newNodeFormLayout(m.width, m.height, len(m.modalDraft), m.modalFocusedControl)
}

func (m *model) openNodeModal() {
	if !m.hasSelectedEditorNode() {
		return
	}
	m.editorMode = editorModeModal
	m.modalFocusedControl = 0
	m.modalDraft = nil
	for _, control := range m.editorNodes[m.selectedNode].controls {
		if !control.hidden {
			m.modalDraft = append(m.modalDraft, control)
		}
	}
	m.modalInput.SetWidth(max(m.nodeForm().width-1, 1))
	m.loadFormField()
}

func (m model) formControl() (nodeControl, bool) {
	if m.modalFocusedControl < 0 || m.modalFocusedControl >= len(m.modalDraft) {
		return nodeControl{}, false
	}
	return m.modalDraft[m.modalFocusedControl], true
}

func (m *model) loadFormField() tea.Cmd {
	control, ok := m.formControl()
	if !ok || !control.editable() {
		m.modalInput.Blur()
		return nil
	}
	m.modalInput.SetValue(control.value)
	m.modalInput.CursorEnd()
	return m.modalInput.Focus()
}

func (m *model) commitFormField() {
	if control, ok := m.formControl(); ok && control.editable() {
		m.modalDraft[m.modalFocusedControl].value = m.modalInput.Value()
	}
}

func (m *model) focusFormField(index int) tea.Cmd {
	m.commitFormField()
	m.modalFocusedControl = index
	return m.loadFormField()
}

func (m *model) cycleFormOption(delta int) {
	control, ok := m.formControl()
	if !ok || control.kind != controlSelect || len(control.options) == 0 {
		return
	}
	next := (max(control.optionIndex(), 0) + delta + len(control.options)) % len(control.options)
	m.modalDraft[m.modalFocusedControl].value = control.options[next].value
}

func (m *model) closeNodeForm() {
	m.editorMode = editorModeNormal
	m.modalDraft = nil
	m.modalInput.Blur()
}

func (m model) updateNodeModalKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	count := len(m.modalDraft)
	switch msg.String() {
	case "esc":
		m.closeNodeForm()
		return m, nil
	case "tab", "down":
		if count > 0 {
			return m, m.focusFormField((m.modalFocusedControl + 1) % count)
		}
		return m, nil
	case "shift+tab", "up":
		if count > 0 {
			return m, m.focusFormField((m.modalFocusedControl + count - 1) % count)
		}
		return m, nil
	}
	if msg.Code == tea.KeyEnter {
		m.saveNodeModal()
		return m, nil
	}

	control, ok := m.formControl()
	switch {
	case !ok:
		return m, nil
	case control.kind == controlSelect:
		switch msg.String() {
		case "left":
			m.cycleFormOption(-1)
		case "right", "space":
			m.cycleFormOption(1)
		}
		return m, nil
	case !control.editable():
		return m, nil
	}

	var cmd tea.Cmd
	m.modalInput, cmd = m.modalInput.Update(msg)
	return m, cmd
}

func (m *model) saveNodeModal() {
	m.commitFormField()
	if m.hasSelectedEditorNode() {
		m.pushUndo()
		node := &m.editorNodes[m.selectedNode]
		controls := append([]nodeControl(nil), m.modalDraft...)
		for _, control := range node.controls {
			if control.hidden {
				controls = append(controls, control)
			}
		}
		node.controls = controls
		for _, control := range m.modalDraft {
			if control.name == "label" {
				node.label = fallbackString(control.value, node.kind)
				node.w, node.h = m.nodeSize(node.kind, node.label)
			}
		}
		m.rebuildNodeRects()
		m.markEditorDirty()
	}
	m.closeNodeForm()
}

func (m model) handleNodeFormClick(x, y int) (model, tea.Cmd) {
	l := m.nodeForm()
	switch {
	case inside(x, y, l.save):
		m.saveNodeModal()
		return m, nil
	case inside(x, y, l.cancel):
		m.closeNodeForm()
		return m, nil
	}

	for i, field := range l.fields {
		if !inside(x, y, field) {
			continue
		}
		index := l.first + i
		if index == m.modalFocusedControl {
			m.cycleFormOption(1)
			return m, nil
		}
		return m, m.focusFormField(index)
	}
	return m, nil
}

func (m model) renderFormValue(control nodeControl, focused bool, width int) string {
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)
	text := lipgloss.NewStyle().Foreground(textColor)

	switch {
	case control.kind == controlSelect && focused:
		arrow := lipgloss.NewStyle().Foreground(accentColor).Render
		return arrow("‹ ") + text.Bold(true).Render(truncatePlain(control.displayValue(), max(width-4, 1))) + arrow(" ›")
	case !control.editable() && control.kind != controlSelect:
		return muted.Render(truncatePlain(multilineHint, width))
	case focused:
		return m.modalInput.View()
	case control.value == "":
		return muted.Render("empty")
	default:
		return text.Render(truncatePlain(control.displayValue(), width))
	}
}

func (m model) renderNodeModal() string {
	node, ok := m.selectedEditorNode()
	if !ok {
		return m.renderWorkflowEditor()
	}

	l := m.nodeForm()
	rows := make([]string, max(m.height-footerHeight, 1))
	put := func(y int, lines ...string) {
		for i, line := range lines {
			if y+i >= 0 && y+i < len(rows) {
				rows[y+i] = strings.Repeat(" ", l.x) + line
			}
		}
	}

	def, _ := m.nodeDefinition(node.kind)
	title := lipgloss.NewStyle().Foreground(categoryColor(def.Category)).Render(mapIconToTerminal(def.Icon)) + " " +
		lipgloss.NewStyle().Bold(true).Foreground(textColor).Render(truncatePlain(node.label, max(l.width-2, 1)))
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)
	if l.scrolled {
		title += muted.Render(fmt.Sprintf("   field %d of %d", m.modalFocusedControl+1, len(m.modalDraft)))
	}
	description := fallbackString(def.Description, fallbackString(def.Title, node.kind))
	put(l.titleY, title, muted.Render(truncatePlain(description, l.width)))

	for i, field := range l.fields {
		index := l.first + i
		if index >= len(m.modalDraft) {
			break
		}
		control := m.modalDraft[index]
		focused := index == m.modalFocusedControl
		value := m.renderFormValue(control, focused, l.width)
		put(field.y, renderField(strings.ToUpper(control.label), value, l.width, focused)...)
	}

	put(l.save.y, renderButton("Save  ⏎", l.save.w, false, false, l.compact)...)
	cancel := muted.Render("Cancel")
	put(l.cancel.y, strings.Repeat(" ", max((l.width-lipgloss.Width(cancel))/2, 0))+cancel)

	hints := nodeFormHints
	if control, ok := m.formControl(); ok && control.kind == controlSelect {
		hints = nodeFormPickHints
	}
	return strings.Join(rows, "\n") + "\n" + renderHints(hints, m.footerRect.w)
}
