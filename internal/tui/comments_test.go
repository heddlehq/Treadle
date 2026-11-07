package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func commentFlow() apiFlow {
	return apiFlow{
		ID: 1,
		Nodes: []apiNode{
			{ID: "a", Type: "Start", Label: "Start", Position: apiPosition{X: 100, Y: 100}},
			{ID: "b", Type: "Output", Label: "Output", Position: apiPosition{X: 400, Y: 100}},
		},
		Connections: []apiConnection{{Source: "a", SourceOutput: "exec", Target: "b", TargetInput: "exec"}},
		Comments: []apiComment{
			{ID: "group", Kind: commentSection, Color: "blue", Text: "Billing", Position: apiPosition{X: 60, Y: 25}, Width: 520, Height: 320},
			{ID: "title", Kind: commentTitle, Color: colorDefault, Text: "Daily orders", Position: apiPosition{X: 60.5, Y: -100.5}, Width: 333, Height: 64},
			{ID: "note", Kind: commentNote, Color: "yellow", Text: "Ask finance\nbefore going live", Position: apiPosition{X: 700, Y: 300}, Width: 240, Height: 140},
		},
	}
}

func commentEditor() model {
	m := canvasModel()
	m.beginWorkflowEditorFromAPI(commentFlow())
	return m
}

func nodeIndex(t *testing.T, m model, id string) int {
	t.Helper()
	for i, node := range m.editorNodes {
		if node.id == id {
			return i
		}
	}
	t.Fatalf("no node %q", id)
	return -1
}

func canvasText(m model) string {
	return ansi.Strip(strings.Join(m.renderCanvasLines(), "\n"))
}

func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		m, _ = m.updateWorkflowEditorKey(keyPress(string(r)))
	}
	return m
}

func pressEnter(m model) model {
	m, _ = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}

func TestCommentsSurviveAnOpenAndSaveUntouched(t *testing.T) {
	flow := commentFlow()
	m := commentEditor()

	payload := m.workflowRequestPayload().Workflow

	if len(payload.Nodes) != 2 {
		t.Fatalf("nodes = %+v", payload.Nodes)
	}
	if len(payload.Comments) != len(flow.Comments) {
		t.Fatalf("comments = %+v", payload.Comments)
	}
	for i, want := range flow.Comments {
		if got := payload.Comments[i]; got != want {
			t.Fatalf("comment %d = %+v, want %+v", i, got, want)
		}
	}
}

func TestAFlowWithOnlyCommentsKeepsThem(t *testing.T) {
	flow := commentFlow()
	flow.Nodes, flow.Connections = nil, nil
	m := canvasModel()

	m.beginWorkflowEditorFromAPI(flow)

	if got := m.workflowRequestPayload().Workflow.Comments; len(got) != len(flow.Comments) {
		t.Fatalf("comments = %+v", got)
	}
}

func TestDeletingTheLastCommentSendsAnEmptyList(t *testing.T) {
	m := canvasModel()
	flow := commentFlow()
	flow.Comments = flow.Comments[:1]
	m.beginWorkflowEditorFromAPI(flow)

	m.selectOnly(nodeIndex(t, m, "group"))
	m.deleteSelection()

	encoded, err := json.Marshal(m.workflowRequestPayload().Workflow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"comments":[]`) {
		t.Fatalf("payload = %s", encoded)
	}
}

func TestCommentsAreDrawnOnTheCanvas(t *testing.T) {
	m := commentEditor()
	canvas := canvasText(m)

	for _, want := range []string{"Billing", "Daily orders", "Ask finance", "before going live", "━"} {
		if !strings.Contains(canvas, want) {
			t.Fatalf("%q not on the canvas:\n%s", want, canvas)
		}
	}
}

func TestACommentIsNotCountedAsANode(t *testing.T) {
	m := commentEditor()
	m.clearSelection()

	if got := ansi.Strip(m.renderCanvasCaption()); !strings.Contains(got, "2 nodes") {
		t.Fatalf("caption = %q", got)
	}
}

func TestACommentHasNoPorts(t *testing.T) {
	m := commentEditor()

	if outputs := m.nodeOutputs(commentNodeKind); len(outputs) != 0 {
		t.Fatalf("outputs = %v", outputs)
	}
	if inputs := m.nodeInputs(commentNodeKind); len(inputs) != 0 {
		t.Fatalf("inputs = %v", inputs)
	}
}

func TestClicksReachANodeOnTopOfASection(t *testing.T) {
	m := commentEditor()
	r := m.nodeRects["a"]

	if got := m.nodeIndexAt(r.x+1, r.y+1); got != nodeIndex(t, m, "a") {
		t.Fatalf("clicked %q", m.editorNodes[got].id)
	}

	section := m.nodeRects["group"]
	if got := m.nodeIndexAt(section.x+1, section.y+section.h-1); got != nodeIndex(t, m, "group") {
		t.Fatalf("clicked %d", got)
	}
}

func TestPressingNAddsANoteAndOpensItsForm(t *testing.T) {
	m := commentEditor()
	before := len(m.editorNodes)

	m, _ = m.updateWorkflowEditorKey(keyPress("n"))
	if m.editorMode != editorModeModal {
		t.Fatalf("mode = %v", m.editorMode)
	}

	m = typeText(t, m, `first\nsecond`)
	m = pressEnter(m)

	if len(m.editorNodes) != before+1 {
		t.Fatalf("nodes = %d", len(m.editorNodes))
	}
	comments := m.workflowRequestPayload().Workflow.Comments
	got := comments[len(comments)-1]
	if got.Kind != commentNote || got.Color != "yellow" || got.Text != "first\nsecond" {
		t.Fatalf("comment = %+v", got)
	}
	if got.Width != 240 || got.Height != 140 {
		t.Fatalf("size = %vx%v", got.Width, got.Height)
	}
	if !m.editorDirty {
		t.Fatal("a new comment should ask to be saved")
	}
}

func TestTheFormChangesTheTypeAndColorOfAComment(t *testing.T) {
	m := commentEditor()
	m.selectOnly(nodeIndex(t, m, "note"))
	m = pressEnter(m)

	m, _ = m.updateWorkflowEditorKey(keyPress("tab"))
	m.cycleFormOption(-1)
	m, _ = m.updateWorkflowEditorKey(keyPress("tab"))
	m.cycleFormOption(1)
	m = pressEnter(m)

	node := m.editorNodes[nodeIndex(t, m, "note")]
	if node.commentKind() != commentText || node.commentField("color") != "green" {
		t.Fatalf("controls = %+v", node.controls)
	}
	if node.label != "Text" {
		t.Fatalf("label = %q", node.label)
	}
}

func TestACommentKeepsItsLineBreaksWhenEdited(t *testing.T) {
	m := commentEditor()
	m.selectOnly(nodeIndex(t, m, "note"))
	m = pressEnter(m)

	if got := m.modalInput.Value(); got != `Ask finance\nbefore going live` {
		t.Fatalf("field = %q", got)
	}
	m = pressEnter(m)

	if got := m.editorNodes[nodeIndex(t, m, "note")].apiComment().Text; got != "Ask finance\nbefore going live" {
		t.Fatalf("text = %q", got)
	}
}

func TestACommentGrowsToShowItsText(t *testing.T) {
	m := commentEditor()
	m.selectOnly(nodeIndex(t, m, "note"))
	m = pressEnter(m)

	m.modalInput.SetValue(strings.Repeat("word ", 80))
	m = pressEnter(m)

	got := m.editorNodes[nodeIndex(t, m, "note")]
	if got.h <= 6 || got.pxHeight != float64(got.h*pixelsPerRow) {
		t.Fatalf("height = %d rows, %vpx", got.h, got.pxHeight)
	}
}

func TestUndoBringsADeletedCommentBack(t *testing.T) {
	m := commentEditor()
	m.selectOnly(nodeIndex(t, m, "note"))
	m.deleteSelection()

	m.undo()

	if got := m.workflowRequestPayload().Workflow.Comments; len(got) != 3 || got[2].Text != "Ask finance\nbefore going live" {
		t.Fatalf("comments = %+v", got)
	}
}

func TestPastingACommentKeepsItsTextAndSize(t *testing.T) {
	m := commentEditor()
	m.selectOnly(nodeIndex(t, m, "note"))

	m.copySelection()
	m.pasteNearOriginal()

	comments := m.workflowRequestPayload().Workflow.Comments
	got := comments[len(comments)-1]
	if len(comments) != 4 || got.ID == "note" || got.Text != "Ask finance\nbefore going live" || got.Width != 240 {
		t.Fatalf("comments = %+v", comments)
	}
}

func TestMovingACommentSavesItsNewPlace(t *testing.T) {
	m := commentEditor()
	m.selectOnly(nodeIndex(t, m, "note"))

	m.moveSelectedEditorNode(2, 1)

	got := m.editorNodes[nodeIndex(t, m, "note")].apiComment().Position
	if got.X != 720 || got.Y != 325 {
		t.Fatalf("position = %+v", got)
	}
}

func TestTheMenusOfferEveryKindOfComment(t *testing.T) {
	m := commentEditor()
	m.editorOverlay = editorOverlayContext
	m.editorMenuTarget = -1

	found := map[string]bool{}
	for _, item := range m.editorMenuItems() {
		if item.action == editorActionAddComment {
			found[item.kind] = true
		}
	}
	for _, kind := range []string{commentTitle, commentText, commentNote, commentSection} {
		if !found[kind] {
			t.Fatalf("no item for %q", kind)
		}
	}
}

func TestAddingACommentFromTheMenuPutsItWhereItWasOpened(t *testing.T) {
	m := commentEditor()
	m.editorOverlay = editorOverlayContext
	m.editorMenuX, m.editorMenuY = 10, 5
	x, y := m.toWorld(10, 5)

	m.runEditorAction(editorMenuItem{action: editorActionAddComment, kind: commentTitle})

	got := m.editorNodes[len(m.editorNodes)-1]
	if got.x != x || got.y != y || got.commentKind() != commentTitle {
		t.Fatalf("comment = %+v", got)
	}
}
