package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func longSidebar(t *testing.T) model {
	t.Helper()
	m := manyNodesEditor(110, 24)
	m.editorOverlay = editorOverlayNone
	return m
}

func sidebarText(m model) string {
	return strings.Join(screenLines(m), "\n")
}

func wheelOnSidebar(m model, button tea.MouseButton, times int) model {
	for range times {
		next, _ := m.Update(tea.MouseWheelMsg{X: m.paletteRect.x + 2, Y: m.paletteRect.y + 4, Button: button})
		m = next.(model)
	}
	return m
}

func TestTheWheelScrollsTheSidebarToItsLastNode(t *testing.T) {
	m := longSidebar(t)
	if strings.Contains(sidebarText(m), "Node 56") {
		t.Fatal("the last node is on screen before scrolling")
	}

	m = wheelOnSidebar(m, tea.MouseWheelDown, 40)

	if screen := sidebarText(m); !strings.Contains(screen, "Node 56") {
		t.Fatalf("the last node is not on screen:\n%s", screen)
	}

	m = wheelOnSidebar(m, tea.MouseWheelUp, 40)

	if screen := sidebarText(m); !strings.Contains(screen, "Node 00") {
		t.Fatalf("the first node is not back:\n%s", screen)
	}
}

func TestTheSidebarSaysHowManyNodesAreOutOfView(t *testing.T) {
	m := longSidebar(t)
	hidden := len(m.availableNodes) - (m.paletteListRows() - 2)

	if screen := sidebarText(m); !strings.Contains(screen, "more") {
		t.Fatalf("no hint of the %d hidden nodes:\n%s", hidden, screen)
	}

	m = wheelOnSidebar(m, tea.MouseWheelDown, 40)

	if screen := sidebarText(m); strings.Contains(screen, "more") || !strings.Contains(screen, "↑") {
		t.Fatalf("hint at the end:\n%s", screen)
	}
}

func TestASidebarThatFitsHasNoHintAndDoesNotScroll(t *testing.T) {
	m := editorModel(110, 60)
	if m.paletteOverflows() {
		t.Skip("the sample nodes don't fit")
	}

	m = wheelOnSidebar(m, tea.MouseWheelDown, 5)

	if m.paletteOffset() != 0 {
		t.Fatalf("offset = %d", m.paletteOffset())
	}
}

func TestClickingAScrolledSidebarAddsTheNodeDrawnThere(t *testing.T) {
	m := wheelOnSidebar(longSidebar(t), tea.MouseWheelDown, 8)

	row := -1
	for i, line := range screenLines(m) {
		if strings.Contains(line, "Node 30") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("Node 30 is not on screen:\n%s", sidebarText(m))
	}

	before := len(m.editorNodes)
	m, _ = m.handleWorkflowEditorClick(tea.Mouse{X: m.paletteRect.x + 3, Y: row})

	if len(m.editorNodes) != before+1 || m.editorNodes[before].kind != "Node 30" {
		t.Fatalf("nodes = %+v", m.editorNodes[before:])
	}
}

func TestClickingTheHintScrollsInsteadOfAddingANode(t *testing.T) {
	m := longSidebar(t)
	before := len(m.editorNodes)

	m, _ = m.handleWorkflowEditorClick(tea.Mouse{X: m.paletteRect.x + 3, Y: m.paletteRect.y + m.paletteRect.h - 1})

	if len(m.editorNodes) != before || m.paletteOffset() == 0 {
		t.Fatalf("nodes = %d, offset = %d", len(m.editorNodes), m.paletteOffset())
	}
}

func TestTheArrowsInAddModeKeepTheNodeOnScreen(t *testing.T) {
	m := longSidebar(t)
	m, _ = m.updateWorkflowEditorKey(keyPress("a"))

	for range 56 {
		m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if screen := sidebarText(m); !strings.Contains(screen, "Node 56") {
		t.Fatalf("going down:\n%s", screen)
	}

	for range 56 {
		m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if screen := sidebarText(m); !strings.Contains(screen, "Node 00") {
		t.Fatalf("going up:\n%s", screen)
	}
}

func TestShrinkingTheTerminalDoesNotLeaveTheSidebarPastItsEnd(t *testing.T) {
	m := wheelOnSidebar(longSidebar(t), tea.MouseWheelDown, 40)

	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 60})
	m = next.(model)

	if screen := sidebarText(m); !strings.Contains(screen, "Node 56") {
		t.Fatalf("the end of the list is gone:\n%s", screen)
	}
}

func TestTheFooterShowsHowToAddANodeAndOpenTheCommands(t *testing.T) {
	m := editorModel(80, 24)

	footer := strings.Join(screenLines(m), "\n")
	for _, want := range []string{"a add node", "tab panels", "ctrl+p commands"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("%q is not in the footer:\n%s", want, footer)
		}
	}
}

func TestAddModeSaysHowToPickANode(t *testing.T) {
	m := editorModel(110, 24)

	m, _ = m.updateWorkflowEditorKey(keyPress("a"))

	if got := m.workflowFooterText(); !strings.Contains(got, "⏎ add") {
		t.Fatalf("footer = %q", got)
	}
}

func TestBothCommandPaletteShortcutsOpenIt(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'p', Mod: tea.ModCtrl},
		{Code: 'p', Mod: tea.ModCtrl | tea.ModShift},
	} {
		m := editorModel(110, 24)

		m, _ = m.updateWorkflowEditorKey(key)

		if m.editorOverlay != editorOverlayCommand {
			t.Fatalf("%s: overlay = %q", key.String(), m.editorOverlay)
		}
	}
}

func press(m model, code rune, mod tea.KeyMod) model {
	next, _ := m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: code, Mod: mod})
	return next
}

func TestTabWalksThePanelsAndComesBackToTheCanvas(t *testing.T) {
	m := editorModel(110, 30)
	if m.focusedPanel() != panelCanvas {
		t.Fatalf("starts on %d", m.focusedPanel())
	}

	for _, want := range []int{panelNodes, panelAgent, panelCanvas} {
		m = press(m, tea.KeyTab, 0)
		if got := m.focusedPanel(); got != want {
			t.Fatalf("panel = %d, want %d", got, want)
		}
	}
}

func TestShiftTabWalksThePanelsBackwards(t *testing.T) {
	m := editorModel(110, 30)

	for _, want := range []int{panelAgent, panelNodes, panelCanvas} {
		m = press(m, tea.KeyTab, tea.ModShift)
		if got := m.focusedPanel(); got != want {
			t.Fatalf("panel = %d, want %d", got, want)
		}
	}
}

func TestTheFocusedPanelIsMarked(t *testing.T) {
	m := editorModel(110, 30)
	if got := sidebarText(m); !strings.Contains(got, "▸ ") || strings.Contains(got, "▸ NODES") {
		t.Fatalf("on the canvas:\n%s", got)
	}

	m = press(m, tea.KeyTab, 0)

	if got := sidebarText(m); !strings.Contains(got, "▸ NODES") {
		t.Fatalf("on the nodes:\n%s", got)
	}
}

func TestEscAndTheRightArrowGoBackToTheCanvas(t *testing.T) {
	for _, key := range []rune{tea.KeyEscape, tea.KeyRight} {
		m := press(editorModel(110, 30), tea.KeyTab, 0)

		m = press(m, key, 0)

		if m.focusedPanel() != panelCanvas {
			t.Fatalf("panel = %d", m.focusedPanel())
		}
		if m.screen != screenWorkflowEditor {
			t.Fatalf("left the editor: %v", m.screen)
		}
	}
}

func TestClickingTheCanvasGivesItTheFocus(t *testing.T) {
	m := press(editorModel(110, 30), tea.KeyTab, 0)
	content := m.canvasContentRect()

	m, _ = m.handleWorkflowEditorClick(tea.Mouse{X: content.x + content.w - 2, Y: content.y + content.h - 1})

	if m.focusedPanel() != panelCanvas {
		t.Fatalf("panel = %d", m.focusedPanel())
	}
}

func TestTheNodesPanelDoesNotTouchTheCanvas(t *testing.T) {
	m := editorModel(110, 30)
	m.selectOnly(1)
	before := len(m.editorNodes)
	at := m.editorNodes[1].x
	m = press(m, tea.KeyTab, 0)

	m = press(m, tea.KeyLeft, 0)
	m = press(m, tea.KeyDelete, 0)

	if len(m.editorNodes) != before || m.editorNodes[1].x != at {
		t.Fatalf("nodes = %+v", m.editorNodes)
	}
}

func TestEnterOnTheNodesPanelAddsAndGoesBackToTheCanvas(t *testing.T) {
	m := press(editorModel(110, 30), tea.KeyTab, 0)
	before := len(m.editorNodes)

	m = press(m, tea.KeyEnter, 0)

	if len(m.editorNodes) != before+1 || m.focusedPanel() != panelCanvas {
		t.Fatalf("nodes = %d, panel = %d", len(m.editorNodes), m.focusedPanel())
	}
}

func TestBracketsSelectTheNodesOfTheCanvas(t *testing.T) {
	m := editorModel(110, 30)
	m.selectOnly(0)

	m, _ = m.updateWorkflowEditorKey(keyPress("]"))
	if m.selectedNode != 1 {
		t.Fatalf("selected = %d", m.selectedNode)
	}

	m, _ = m.updateWorkflowEditorKey(keyPress("["))
	if m.selectedNode != 0 {
		t.Fatalf("selected = %d", m.selectedNode)
	}
}
