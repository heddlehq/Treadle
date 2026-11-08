package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func footerLine(m model) string {
	lines := screenLines(m)
	return lines[len(lines)-1]
}

func TestTheFooterAlwaysLeadsToEveryKey(t *testing.T) {
	for _, width := range []int{72, 90, 120, 200} {
		m := editorModel(width, 24)
		m.selectOnly(0)

		footer := footerLine(m)
		if got := lipgloss.Width(footer); got != width {
			t.Fatalf("%d: footer is %d wide", width, got)
		}
		for _, want := range []string{"? keys", "ctrl+p commands"} {
			if !strings.Contains(footer, want) {
				t.Fatalf("%d: %q is not in the footer: %q", width, want, footer)
			}
		}
	}
}

func TestTheFooterShowsAKeyOnce(t *testing.T) {
	m := editorModel(200, 24)
	m.selectOnly(0)

	footer := footerLine(m)
	if count := strings.Count(footer, "/"); count != 1 {
		t.Fatalf("/ shows %d times: %q", count, footer)
	}
}

func TestTheFooterFollowsTheSelection(t *testing.T) {
	m := editorModel(200, 24)

	m.clearSelection()
	if footer := footerLine(m); strings.Contains(footer, "⏎ edit") {
		t.Fatalf("nothing to edit: %q", footer)
	}

	m.selectOnly(0)
	if footer := footerLine(m); !strings.Contains(footer, "⏎ edit") || !strings.Contains(footer, "c connect") {
		t.Fatalf("selected: %q", footer)
	}
}

func TestHelpShowsEveryKey(t *testing.T) {
	m := editorModel(110, 80)

	m, _ = m.updateWorkflowEditorKey(keyPress("?"))
	if m.editorOverlay != editorOverlayHelp {
		t.Fatalf("overlay = %q", m.editorOverlay)
	}

	screen := strings.Join(screenLines(m), "\n")
	for _, binding := range m.keyBindings() {
		if !strings.Contains(screen, binding.label) {
			t.Errorf("help does not show %q", binding.label)
		}
	}

	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.editorOverlay != editorOverlayNone {
		t.Fatalf("esc left %q open", m.editorOverlay)
	}
}

func TestHelpScrollsWhenItDoesNotFit(t *testing.T) {
	m := editorModel(80, 20)
	m, _ = m.updateWorkflowEditorKey(keyPress("?"))
	first := strings.Join(screenLines(m), "\n")

	for range 40 {
		m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}

	last := strings.Join(screenLines(m), "\n")
	if first == last || !strings.Contains(last, "Quit Treadle") {
		t.Fatalf("help did not scroll to its end:\n%s", last)
	}
}

func typeInto(m model, text string) model {
	for _, r := range text {
		m, _ = m.updateWorkflowEditorKey(keyPress(string(r)))
	}
	return m
}

func TestThePaletteFindsACommandByNameAndRunsIt(t *testing.T) {
	m := editorModel(110, 30)
	before := m.zoomIndex

	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeInto(m, "zoom in")

	items := m.editorMenuItems()
	if len(items) != 1 || items[0].label != "Zoom in" {
		t.Fatalf("items = %+v", items)
	}

	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.editorOverlay != editorOverlayNone || m.zoomIndex != before+1 {
		t.Fatalf("overlay = %q, zoom = %d", m.editorOverlay, m.zoomIndex)
	}
}

func TestThePaletteGoesToANodeByItsName(t *testing.T) {
	m := canvasModel()
	m.place("Logger", 4, 4)
	m.place("Output", 140, 60)
	m.editorNodes[1].label = "Send the report"

	m, _ = m.updateWorkflowEditorKey(keyPress("g"))
	m = typeInto(m, "report")
	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.selectedNode != 1 {
		t.Fatalf("selected = %d", m.selectedNode)
	}
	if r := m.nodeRects[m.editorNodes[1].id]; !inside(r.x, r.y, m.canvasContentRect()) {
		t.Fatalf("the node is not in view: %+v", r)
	}
}

func TestThePaletteAddsANodeTypeFoundByName(t *testing.T) {
	m := canvasModel()

	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeInto(m, "add logger")
	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(m.editorNodes) != 1 || m.editorNodes[0].kind != "Logger" {
		t.Fatalf("nodes = %+v", m.editorNodes)
	}
}

func TestThePaletteSaysWhenNothingMatches(t *testing.T) {
	m := editorModel(110, 30)

	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeInto(m, "zzzz")
	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !strings.Contains(strings.Join(screenLines(m), "\n"), "Nothing matches") {
		t.Fatal("no word about nothing matching")
	}
}

// grid places nodes like this, in world cells:
//
//	a        b
//	c        d
func grid(t *testing.T) model {
	t.Helper()
	m := canvasModel()
	m.place("Logger", 4, 4)
	m.place("Logger", 44, 4)
	m.place("Logger", 4, 20)
	m.place("Logger", 44, 21)
	return m
}

func TestArrowsGoToTheNearestNodeThatWay(t *testing.T) {
	m := grid(t)
	m.selectOnly(0)

	for _, step := range []struct {
		key  rune
		want int
	}{
		{tea.KeyRight, 1},
		{tea.KeyDown, 3},
		{tea.KeyLeft, 2},
		{tea.KeyUp, 0},
		{tea.KeyUp, 0},
	} {
		m = press(m, step.key, 0)
		if m.selectedNode != step.want {
			t.Fatalf("after %q selected = %d, want %d", step.key, m.selectedNode, step.want)
		}
	}
}

func TestVimKeysGoBetweenNodesToo(t *testing.T) {
	m := grid(t)
	m.selectOnly(0)

	m, _ = m.updateWorkflowEditorKey(keyPress("l"))
	m, _ = m.updateWorkflowEditorKey(keyPress("j"))

	if m.selectedNode != 3 {
		t.Fatalf("selected = %d", m.selectedNode)
	}
}

func TestArrowsStartFromTheMiddleOfTheView(t *testing.T) {
	m := grid(t)
	m.clearSelection()

	m = press(m, tea.KeyRight, 0)

	if !m.hasSelectedEditorNode() {
		t.Fatal("nothing selected")
	}
}

func TestShiftArrowsMoveTheSelection(t *testing.T) {
	m := grid(t)
	m.selectOnly(1)
	x := m.editorNodes[1].x

	m = press(m, tea.KeyRight, tea.ModShift)
	m, _ = m.updateWorkflowEditorKey(keyPress("L"))

	if m.editorNodes[1].x != x+2 || m.selectedNode != 1 {
		t.Fatalf("x = %d, want %d, selected = %d", m.editorNodes[1].x, x+2, m.selectedNode)
	}
}

func TestWWalksTheWiresOfANodeAndComesBack(t *testing.T) {
	m := grid(t)
	m.editorConnections = []editorConnection{
		{source: m.editorNodes[0].id, sourceOutput: "exec", target: m.editorNodes[1].id, targetInput: "exec"},
		{source: m.editorNodes[2].id, sourceOutput: "exec", target: m.editorNodes[0].id, targetInput: "exec"},
	}
	m.selectOnly(0)

	m, _ = m.updateWorkflowEditorKey(keyPress("w"))
	if m.selectedConnection != 0 {
		t.Fatalf("first wire = %d", m.selectedConnection)
	}
	m, _ = m.updateWorkflowEditorKey(keyPress("w"))
	if m.selectedConnection != 1 {
		t.Fatalf("second wire = %d", m.selectedConnection)
	}
	m, _ = m.updateWorkflowEditorKey(keyPress("w"))
	if m.hasSelectedConnection() || m.selectedNode != 0 {
		t.Fatalf("connection = %d, node = %d", m.selectedConnection, m.selectedNode)
	}
}

func TestAWireReachedByKeyCanBeDeleted(t *testing.T) {
	m := grid(t)
	m.editorConnections = []editorConnection{
		{source: m.editorNodes[0].id, sourceOutput: "exec", target: m.editorNodes[1].id, targetInput: "exec"},
	}
	m.selectOnly(0)

	m, _ = m.updateWorkflowEditorKey(keyPress("w"))
	m = press(m, tea.KeyDelete, 0)

	if len(m.editorConnections) != 0 || len(m.editorNodes) != 4 {
		t.Fatalf("connections = %d, nodes = %d", len(m.editorConnections), len(m.editorNodes))
	}
}

func TestEveryPaletteCommandDoesSomething(t *testing.T) {
	m := grid(t)
	m.editorConnections = []editorConnection{
		{source: m.editorNodes[0].id, sourceOutput: "exec", target: m.editorNodes[1].id, targetInput: "exec"},
	}
	m.selectOnly(0)

	for _, item := range m.paletteCommands() {
		if item.action == "" {
			t.Errorf("%q has no action", item.label)
		}
	}
}

func TestThePaletteListsWhatStartsWithTheSearchFirst(t *testing.T) {
	m := canvasModel()

	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = typeInto(m, "lo")

	if items := m.editorMenuItems(); len(items) == 0 || items[0].label != "Add Logger" {
		t.Fatalf("items = %+v", items)
	}
}
