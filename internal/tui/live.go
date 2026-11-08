package tui

import (
	"context"
	"encoding/json"
	"errors"
	"image/color"
	"reflect"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"treadle/internal/live"
)

// Editing a flow with everyone else who has it open, on the web or in another
// terminal, the way the web editor does: there is no save. What changes here goes to
// the server a moment later, as the graph it was made on and the graph it made, so the
// server applies only that, after whatever others did. What others change comes back
// as the whole draft, which replaces the canvas unless something here is still on its
// way. Undo takes back only what this person did.
//
// A server without the live API is edited the way it was before: ctrl+s saves it.

const (
	liveSendAfter  = 150 * time.Millisecond
	liveFocusAfter = 200 * time.Millisecond
	// A cursor of the web that hasn't moved for this long is no longer drawn.
	liveCursorAge = 10 * time.Second
	liveUndoMax   = 50
)

type liveState string

const (
	liveOff         liveState = ""
	liveConnecting  liveState = "connecting"
	liveOnline      liveState = "online"
	liveOffline     liveState = "offline"
	liveUnsupported liveState = "unsupported"
)

// liveDelta is one change this person sent: the graph it was made on, and the graph it
// made. Sending it backwards undoes it.
type liveDelta struct {
	base  workflowPayload
	graph workflowPayload
}

type liveCursor struct {
	x, y float64
	at   time.Time
}

type liveEventMsg struct {
	gen   int
	event live.Event
	down  error
}

type liveSendMsg struct{ gen int }

type liveFocusMsg struct{ gen int }

type liveAppliedMsg struct {
	gen     int
	kind    string
	delta   liveDelta
	applied live.Applied
	err     error
}

const (
	liveKindEdit = "edit"
	liveKindUndo = "undo"
	liveKindRedo = "redo"
	liveKindRead = "read"
)

// liveSession is what outlives a copy of the model: the stream of the flow.
type liveSession struct {
	events chan liveEventMsg
	cancel context.CancelFunc
}

func (m model) liveClient() *live.Client {
	base, _ := normalizeBaseURL(m.activeHost)
	return &live.Client{BaseURL: base, Token: m.activeAPIKey}
}

// liveActive is the flow being edited with others, now or once the connection is back.
func (m model) liveActive() bool {
	return m.liveState == liveConnecting || m.liveState == liveOnline || m.liveState == liveOffline
}

// startLive follows the open flow until the editor leaves it.
func (m *model) startLive() tea.Cmd {
	m.stopLive()
	if m.editorWorkflowID == 0 || strings.TrimSpace(m.activeHost) == "" {
		return nil
	}

	m.liveGen++
	gen, flowID, client := m.liveGen, m.editorWorkflowID, m.liveClient()
	ctx, cancel := context.WithCancel(context.Background())
	session := &liveSession{events: make(chan liveEventMsg, 64), cancel: cancel}
	m.live = session
	m.liveState = liveConnecting
	m.liveBase = m.workflowRequestPayload().Workflow
	m.liveBaseSnapshot = m.snapshot()
	m.liveUndo, m.liveRedo = nil, nil
	m.livePeople, m.liveCursors = nil, map[string]liveCursor{}

	go func() {
		defer close(session.events)
		for tries := 0; ; tries++ {
			err := client.Follow(ctx, flowID, func(event live.Event) {
				select {
				case session.events <- liveEventMsg{gen: gen, event: event}:
				case <-ctx.Done():
				}
			})
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				tries = 0
			}
			select {
			case session.events <- liveEventMsg{gen: gen, down: liveDown(err)}:
			case <-ctx.Done():
				return
			}
			if unsupported(err) {
				return
			}
			select {
			case <-time.After(live.Backoff(tries)):
			case <-ctx.Done():
				return
			}
		}
	}()

	return listenLive(session, gen)
}

func liveDown(err error) error {
	if err == nil {
		return errors.New("the connection ended")
	}
	return err
}

// unsupported is a server from before the live API, which has no such route.
func unsupported(err error) bool {
	return errors.Is(err, live.ErrNotFound)
}

func listenLive(session *liveSession, gen int) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-session.events
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *model) stopLive() {
	if m.live != nil {
		m.live.cancel()
	}
	m.live = nil
	m.liveState = liveOff
	m.liveClientID = ""
	m.livePending = nil
	m.liveSending = false
	m.liveSendQueued = false
	m.liveFocusQueued = false
	m.liveFocusSent = ""
	m.livePeople = nil
	m.liveCursors = nil
}

func (m model) handleLiveEvent(msg liveEventMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.liveGen || m.live == nil {
		return m, nil
	}
	next := listenLive(m.live, m.liveGen)

	switch {
	case msg.down != nil:
		if unsupported(msg.down) {
			m.stopLive()
			m.liveState = liveUnsupported
			return m, nil
		}
		m.liveState = liveOffline
		m.livePeople, m.liveCursors = nil, map[string]liveCursor{}
		return m, next

	case msg.event.Ready != nil:
		ready := msg.event.Ready
		m.liveState = liveOnline
		m.liveClientID = ready.Client
		m.liveCanEdit = ready.CanEdit
		m.liveSeq = ready.Seq
		m.liveFocusSent = ""
		m.offerDraft(ready.Graph, ready.Seq, false)
		return m, tea.Batch(next, m.queueSend())

	case msg.event.Change != nil:
		change := msg.event.Change
		if change.Seq <= m.liveSeq {
			return m, next
		}
		m.liveSeq = change.Seq
		if change.Client != "" && change.Client == m.liveClientID {
			// This person's own change: its answer brings the draft.
			return m, next
		}
		m.offerDraft(change.Graph, change.Seq, true)
		return m, next

	case msg.event.Presence != nil:
		m.livePeople = m.livePeople[:0]
		for _, person := range msg.event.Presence {
			if person.Client != m.liveClientID {
				m.livePeople = append(m.livePeople, person)
			}
		}
		sort.SliceStable(m.livePeople, func(i, j int) bool { return m.livePeople[i].Name < m.livePeople[j].Name })
		return m, next

	case msg.event.Cursor != nil:
		cursor := msg.event.Cursor
		if m.liveCursors == nil {
			m.liveCursors = map[string]liveCursor{}
		}
		if cursor.X == nil || cursor.Y == nil {
			delete(m.liveCursors, cursor.Client)
		} else {
			m.liveCursors[cursor.Client] = liveCursor{x: *cursor.X, y: *cursor.Y, at: time.Now()}
		}
		return m, next
	}
	return m, next
}

// offerDraft takes the draft as the server has it, now if nothing here is on its way
// and nothing is half done, or once that is over.
func (m *model) offerDraft(graph live.Graph, seq int, theirs bool) {
	m.livePending, m.livePendingSeq = &graph, seq
	m.livePendingTheirs = m.livePendingTheirs || theirs
	m.adoptPending()
}

func (m *model) adoptPending() {
	if m.livePending == nil || m.liveBusy() || m.liveUnsent() {
		return
	}
	graph, theirs := *m.livePending, m.livePendingTheirs
	m.livePending, m.livePendingTheirs = nil, false
	if m.adoptDraft(graph) && theirs {
		m.editorStatusMessage = "Changed by someone else"
	}
}

// liveBusy is something half done on the canvas, which a new draft would pull from
// under the person: a form, a drag, a wire, a connection being made.
func (m model) liveBusy() bool {
	return m.liveSending || m.editorMode == editorModeModal || m.editorMode == editorModeConnect ||
		m.draggingNode >= 0 || m.wireActive || m.marqueeActive
}

// liveUnsent is a change here the server doesn't have yet.
func (m model) liveUnsent() bool {
	return !reflect.DeepEqual(m.workflowRequestPayload().Workflow, m.liveBase)
}

// adoptDraft puts the draft on the canvas, keeping what is selected and where the view
// is. It says whether that changed the canvas.
func (m *model) adoptDraft(graph live.Graph) bool {
	flow, err := decodeGraph(graph)
	if err != nil {
		m.editorStatusMessage = "Can't read the flow from the server: " + err.Error()
		return false
	}

	selected := map[string]bool{}
	for _, index := range m.selectedIndices() {
		selected[m.editorNodes[index].id] = true
	}
	primary := ""
	if node, ok := m.selectedEditorNode(); ok {
		primary = node.id
	}

	nodes, connections := m.mapAPIFlowToEditor(flow)
	before := m.workflowRequestPayload().Workflow
	m.editorNodes, m.editorConnections = nodes, connections
	m.liveBase = m.workflowRequestPayload().Workflow
	m.liveBaseSnapshot = m.snapshot()
	m.editorDirty = false

	var indices []int
	for i, node := range m.editorNodes {
		if selected[node.id] {
			indices = append(indices, i)
		}
	}
	m.setSelection(indices)
	if index, ok := m.editorNodeIndex(primary); ok && selected[primary] {
		m.selectedNode = index
		delete(m.extraSelection, primary)
	}
	m.rebuildNodeRects()
	return !reflect.DeepEqual(before, m.liveBase)
}

func decodeGraph(graph live.Graph) (apiFlow, error) {
	var flow apiFlow
	for _, part := range []struct {
		raw  json.RawMessage
		into any
	}{
		{graph.Nodes, &flow.Nodes},
		{graph.Connections, &flow.Connections},
		{graph.Comments, &flow.Comments},
	} {
		if len(part.raw) == 0 || string(part.raw) == "null" {
			continue
		}
		if err := json.Unmarshal(part.raw, part.into); err != nil {
			return apiFlow{}, err
		}
	}
	return flow, nil
}

// queueSend sends what changed here a moment from now, so a burst of keys is one change.
func (m *model) queueSend() tea.Cmd {
	if m.liveState != liveOnline || !m.liveCanEdit || m.liveSendQueued || m.liveSending || !m.liveUnsent() {
		return nil
	}
	m.liveSendQueued = true
	gen := m.liveGen
	return tea.Tick(liveSendAfter, func(time.Time) tea.Msg { return liveSendMsg{gen: gen} })
}

func (m model) handleLiveSend(msg liveSendMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.liveGen {
		return m, nil
	}
	m.liveSendQueued = false
	if m.liveState == liveUnsupported {
		return m, m.saveCurrentWorkflow()
	}
	return m, m.sendNow()
}

// queueAutosave saves a Heddle from before the live API a moment after each change,
// the whole flow at once, since that Heddle can't take less.
func (m *model) queueAutosave() tea.Cmd {
	if !m.editorDirty || m.editorSaving || m.liveSendQueued || m.autosaveHeld || m.editorWorkflowID == 0 {
		return nil
	}
	m.liveSendQueued = true
	gen := m.liveGen
	return tea.Tick(liveSendAfter, func(time.Time) tea.Msg { return liveSendMsg{gen: gen} })
}

// sendNow sends what changed here since the last draft or the last change sent.
func (m *model) sendNow() tea.Cmd {
	if m.liveState != liveOnline || !m.liveCanEdit || m.liveSending {
		return nil
	}
	graph := m.workflowRequestPayload().Workflow
	if reflect.DeepEqual(graph, m.liveBase) {
		m.markLiveSaved()
		return nil
	}
	return m.sendDelta(liveKindEdit, liveDelta{base: m.liveBase, graph: graph})
}

func (m *model) sendDelta(kind string, delta liveDelta) tea.Cmd {
	m.liveSending = true
	m.editorStatusMessage = ""
	gen, flowID, client, id := m.liveGen, m.editorWorkflowID, m.liveClient(), m.liveClientID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		applied, err := client.Change(ctx, flowID, id, delta.base, delta.graph)
		return liveAppliedMsg{gen: gen, kind: kind, delta: delta, applied: applied, err: err}
	}
}

func (m model) handleLiveApplied(msg liveAppliedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.liveGen {
		return m, nil
	}
	m.liveSending = false

	if msg.err != nil {
		switch {
		case msg.kind == liveKindRead:
			// The stream brings the draft anyway.
		case errors.Is(msg.err, live.ErrConflict) && msg.kind != liveKindEdit:
			m.editorStatusMessage = "Can't " + msg.kind + ": someone else changed that since"
		case errors.Is(msg.err, live.ErrConflict):
			m.liveBase = m.workflowRequestPayload().Workflow
			read := m.readDraft()
			m.editorStatusMessage = "Someone else changed that meanwhile: the flow is as they left it"
			return m, read
		default:
			m.editorStatusMessage = "Not sent: " + truncatePlain(msg.err.Error(), 48) + ". It goes when the connection is back."
			m.liveState = liveOffline
			m.editorRunAfterSave = false
		}
		if m.agentWaitingForSave {
			m.abortPendingAgent()
		}
		m.publishAfterSend = false
		m.adoptPending()
		return m, nil
	}

	switch msg.kind {
	case liveKindEdit:
		m.liveUndo = append(m.liveUndo, msg.delta)
		if len(m.liveUndo) > liveUndoMax {
			m.liveUndo = m.liveUndo[len(m.liveUndo)-liveUndoMax:]
		}
		m.liveRedo = nil
	case liveKindUndo:
		m.liveRedo = append(m.liveRedo, msg.delta.backwards())
	case liveKindRedo:
		m.liveUndo = append(m.liveUndo, msg.delta)
	}
	m.liveSeq = max(m.liveSeq, msg.applied.Seq)

	sent := msg.delta.graph
	if msg.kind != liveKindEdit || reflect.DeepEqual(m.workflowRequestPayload().Workflow, sent) {
		// Nothing changed here meanwhile: the draft of the answer has everyone's changes.
		if m.livePending != nil && m.livePendingSeq > msg.applied.Seq {
			m.adoptPending()
		} else {
			m.livePending = nil
			m.adoptDraft(msg.applied.Graph)
		}
		m.markLiveSaved()
	} else {
		// More changed here while this was on its way: that goes next, from here.
		m.liveBase = sent
	}

	cmds := []tea.Cmd{m.queueSend()}
	if m.editorRunAfterSave && !m.liveUnsent() {
		m.editorRunAfterSave = false
		cmds = append(cmds, m.startExecution())
	}
	if m.publishAfterSend && !m.liveUnsent() {
		m.publishAfterSend = false
		cmds = append(cmds, m.publish(0, m.publishNote))
	}
	if m.agentWaitingForSave && !m.liveUnsent() {
		m.agentWaitingForSave = false
		next, ask := m.askAgent()
		return next, tea.Batch(append(cmds, ask)...)
	}
	return m, tea.Batch(cmds...)
}

// readDraft asks for the draft as it is, by sending a change of nothing.
func (m *model) readDraft() tea.Cmd {
	return m.sendDelta(liveKindRead, liveDelta{base: m.liveBase, graph: m.liveBase})
}

func (m *model) markLiveSaved() {
	m.editorDirty = false
	m.editorSaving = false
}

// liveUndoStep takes back the last change of this person, and only that: what they
// haven't sent yet, or else the last change they sent, applied to the flow as it is.
func (m *model) liveUndoStep() tea.Cmd {
	if m.liveSending {
		return nil
	}
	if m.liveUnsent() {
		m.liveSendQueued = false
		m.editorNodes = copyNodes(m.liveBaseSnapshot.nodes)
		m.editorConnections = append([]editorConnection(nil), m.liveBaseSnapshot.connections...)
		m.clearSelection()
		m.rebuildNodeRects()
		m.markLiveSaved()
		return nil
	}
	if len(m.liveUndo) == 0 {
		return nil
	}
	last := m.liveUndo[len(m.liveUndo)-1]
	m.liveUndo = m.liveUndo[:len(m.liveUndo)-1]
	return m.sendDelta(liveKindUndo, last.backwards())
}

func (m *model) liveRedoStep() tea.Cmd {
	if m.liveSending || len(m.liveRedo) == 0 || m.liveUnsent() {
		return nil
	}
	last := m.liveRedo[len(m.liveRedo)-1]
	m.liveRedo = m.liveRedo[:len(m.liveRedo)-1]
	return m.sendDelta(liveKindRedo, last)
}

// backwards is the change that undoes this one.
func (d liveDelta) backwards() liveDelta {
	return liveDelta{base: d.graph, graph: d.base}
}

// liveFocus is what this person points at: what they selected, and what they edit.
func (m model) liveFocus() (string, []string, string) {
	selected := []string{}
	for _, index := range m.selectedIndices() {
		selected = append(selected, m.editorNodes[index].id)
	}
	editing := ""
	if m.editorMode == editorModeModal {
		if node, ok := m.selectedEditorNode(); ok {
			editing = node.id
		}
	}
	return strings.Join(selected, ",") + "|" + editing, selected, editing
}

func (m *model) queueFocus() tea.Cmd {
	if m.liveState != liveOnline || m.liveFocusQueued {
		return nil
	}
	if key, _, _ := m.liveFocus(); key == m.liveFocusSent {
		return nil
	}
	m.liveFocusQueued = true
	gen := m.liveGen
	return tea.Tick(liveFocusAfter, func(time.Time) tea.Msg { return liveFocusMsg{gen: gen} })
}

func (m model) handleLiveFocus(msg liveFocusMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.liveGen || m.liveState != liveOnline {
		return m, nil
	}
	m.liveFocusQueued = false
	key, selected, editing := m.liveFocus()
	if key == m.liveFocusSent {
		return m, nil
	}
	m.liveFocusSent = key
	client, flowID, id := m.liveClient(), m.editorWorkflowID, m.liveClientID
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		_ = client.Focus(ctx, flowID, id, selected, editing)
		return nil
	}
}

// afterLiveUpdate runs after every message in the editor: it sends what changed, says
// what is selected, and takes a draft that waited for something to finish.
func (m model) afterLiveUpdate() (model, tea.Cmd) {
	if m.screen != screenWorkflowEditor {
		if m.live != nil {
			m.stopLive()
		}
		return m, nil
	}
	if m.liveState == liveUnsupported {
		return m, m.queueAutosave()
	}
	if !m.liveActive() {
		return m, nil
	}
	m.adoptPending()
	if m.liveUnsent() {
		m.editorDirty = true
	}
	return m, tea.Batch(m.queueSend(), m.queueFocus())
}

// liveStatus is what the top bar says of the connection, in place of Save.
func (m model) liveStatus() (string, color.Color) {
	switch {
	case m.liveState == liveUnsupported && m.autosaveHeld && m.editorDirty:
		return "Not saved", errorColor
	case m.liveState == liveUnsupported && (m.editorSaving || m.editorDirty):
		return "Saving...", mutedTextColor
	case m.liveState == liveUnsupported:
		return "Saved", mutedTextColor
	case m.liveState == liveOffline:
		return "Offline", warmColor
	case m.liveState == liveConnecting:
		return "Connecting...", mutedTextColor
	case !m.liveCanEdit:
		return "View only", mutedTextColor
	case m.liveSending || m.liveSendQueued || m.liveUnsent():
		return "Saving...", mutedTextColor
	default:
		return "Saved", mutedTextColor
	}
}

// renderPeople are the others who have the flow open, in their colors.
func (m model) renderPeople(width int) string {
	if len(m.livePeople) == 0 || width < 4 {
		return ""
	}
	parts := []string{}
	seen := map[int]bool{}
	for _, person := range m.livePeople {
		if person.Kind == "person" && seen[person.UserID] {
			continue
		}
		seen[person.UserID] = true
		name := person.Name
		if person.Kind == "agent" {
			name = "agent"
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(personColor(person)).Render("● "+name))
	}
	row := strings.Join(parts, " ")
	for lipgloss.Width(row) > width && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		row = strings.Join(parts, " ") + lipgloss.NewStyle().Foreground(mutedTextColor).Render(" +")
	}
	return row
}

func personColor(person live.Person) color.Color {
	if person.Color == "" {
		return accentColor
	}
	return lipgloss.Color(person.Color)
}

// selectedBy is someone else who has the node selected or open, for the canvas to
// draw it in their color.
func (m model) selectedBy(id string) (live.Person, bool) {
	for _, person := range m.livePeople {
		if person.Editing != nil && *person.Editing == id {
			return person, true
		}
		for _, selected := range person.Selected {
			if selected == id {
				return person, true
			}
		}
	}
	return live.Person{}, false
}

// drawLiveCursors draws where the pointers of the web are on the flow.
func (m model) drawLiveCursors(grid [][]canvasCell) {
	now := time.Now()
	for client, cursor := range m.liveCursors {
		if now.Sub(cursor.at) > liveCursorAge {
			continue
		}
		person, ok := m.personByClient(client)
		if !ok {
			continue
		}
		wx := int(cursor.x / pixelsPerColumn)
		wy := int(cursor.y / pixelsPerRow)
		x, y := m.toScreen(wx, wy)
		style := lipgloss.NewStyle().Foreground(personColor(person)).Bold(true)
		drawStyledRune(grid, x, y, '▲', &style)
		label := lipgloss.NewStyle().Foreground(personColor(person))
		for i, r := range []rune(person.Name) {
			drawStyledRune(grid, x+2+i, y, r, &label)
		}
	}
}

func (m model) personByClient(client string) (live.Person, bool) {
	for _, person := range m.livePeople {
		if person.Client == client {
			return person, true
		}
	}
	return live.Person{}, false
}
