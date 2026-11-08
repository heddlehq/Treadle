package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// fakeLive is the live API of a Heddle: the test says what the stream sends, and what
// a change answers.
type fakeLive struct {
	t       *testing.T
	server  *httptest.Server
	events  chan string
	mu      sync.Mutex
	changes []map[string]any
	focus   []map[string]any
	runs    int
	status  int
	answer  func(body map[string]any) any
}

func newFakeLive(t *testing.T) *fakeLive {
	t.Helper()
	f := &fakeLive{t: t, events: make(chan string, 16), status: http.StatusOK}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/live"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			for {
				select {
				case event := <-f.events:
					fmt.Fprint(w, event)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		case strings.HasSuffix(r.URL.Path, "/changes"):
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.changes = append(f.changes, body)
			status, answer := f.status, f.answer
			f.mu.Unlock()
			w.WriteHeader(status)
			if status >= 300 {
				fmt.Fprint(w, `{"error":{"code":"conflict","message":"gone"}}`)
				return
			}
			graph := body["graph"]
			if answer != nil {
				graph = answer(body)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"seq": 10, "applied": 10, "graph": graph}})
		case strings.HasSuffix(r.URL.Path, "/focus"):
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.focus = append(f.focus, body)
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/executions") && r.Method == http.MethodPost:
			f.mu.Lock()
			f.runs++
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "run-1", "status": "queued"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeLive) send(name string, data any) {
	encoded, _ := json.Marshal(data)
	f.events <- fmt.Sprintf("event: %s\ndata: %s\n\n", name, encoded)
}

func (f *fakeLive) sentChanges() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.changes...)
}

func apiGraph(nodes ...map[string]any) map[string]any {
	return map[string]any{"nodes": nodes, "connections": []any{}, "comments": []any{}}
}

func apiNodeAt(id, kind, label string, x, y float64) map[string]any {
	return map[string]any{"id": id, "type": kind, "label": label, "controls": map[string]any{}, "position": map[string]any{"x": x, "y": y}}
}

// driver runs the commands of the model the way Bubble Tea does, each on its own.
type driver struct {
	t    *testing.T
	msgs chan tea.Msg
}

func newDriver(t *testing.T) *driver {
	return &driver{t: t, msgs: make(chan tea.Msg, 64)}
}

func (d *driver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() { d.msgs <- cmd() }()
}

func (d *driver) until(m model, what string, done func(model) bool) model {
	d.t.Helper()
	deadline := time.After(5 * time.Second)
	for !done(m) {
		select {
		case msg := <-d.msgs:
			switch msg := msg.(type) {
			case nil:
			case tea.BatchMsg:
				for _, cmd := range msg {
					d.run(cmd)
				}
			default:
				next, cmd := m.Update(msg)
				m = next.(model)
				d.run(cmd)
			}
		case <-deadline:
			d.t.Fatalf("never: %s", what)
		}
	}
	return m
}

// liveEditor opens flow 7 on a fake Heddle, and waits for its draft.
func liveEditor(t *testing.T, f *fakeLive, d *driver, graph map[string]any) model {
	t.Helper()
	m := appModel(140, 40)
	m.activeHost = f.server.URL
	m.availableNodes = serverNodes

	next, cmd := m.Update(workflowLoadedMsg{flow: apiFlow{ID: 7, Name: "Report"}})
	m = next.(model)
	d.run(cmd)
	t.Cleanup(func() { m.stopLive() })

	f.send("ready", map[string]any{"client": "me", "can_edit": true, "user_id": 1, "seq": 1, "epoch": "e", "graph": graph})
	return d.until(m, "the draft arrives", func(m model) bool { return m.liveState == liveOnline && len(m.editorNodes) > 0 })
}

func TestOpeningAFlowShowsTheDraftOfEveryone(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)

	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))

	if len(m.editorNodes) != 2 || m.editorNodes[1].label != "Note it" {
		t.Fatalf("nodes = %+v", m.editorNodes)
	}
	screen := strings.Join(screenLines(m), "\n")
	if !strings.Contains(screen, "Saved") || strings.Contains(screen, "Save ") {
		t.Fatalf("the top bar should say it is saved, not offer Save:\n%s", screenLines(m)[0])
	}
}

func TestAChangeHereSendsOnlyWhatChanged(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))
	// Meanwhile someone else added a node: the answer has it.
	f.answer = func(body map[string]any) any {
		graph := body["graph"].(map[string]any)
		graph["nodes"] = append(graph["nodes"].([]any), apiNodeAt("theirs", "Output", "Theirs", 600, 0))
		return graph
	}

	m.selectOnly(1)
	m = press(m, tea.KeyRight, tea.ModShift)
	next, cmd := m.Update(nil)
	m = next.(model)
	d.run(cmd)

	m = d.until(m, "the change is answered", func(m model) bool { return len(m.editorNodes) == 3 })

	changes := f.sentChanges()
	if len(changes) != 1 {
		t.Fatalf("sent %d changes", len(changes))
	}
	base := changes[0]["base"].(map[string]any)["nodes"].([]any)
	graph := changes[0]["graph"].(map[string]any)["nodes"].([]any)
	if moved := graph[1].(map[string]any)["position"].(map[string]any)["x"]; moved == 300.0 {
		t.Fatal("the change doesn't have the move")
	}
	if base[1].(map[string]any)["position"].(map[string]any)["x"] != 300.0 {
		t.Fatalf("the base isn't the draft the change was made on: %+v", base[1])
	}
	if changes[0]["client"] != "me" {
		t.Fatalf("client = %v", changes[0]["client"])
	}
	if m.selectedNode != 1 || m.liveUnsent() {
		t.Fatalf("selected = %d, unsent = %v", m.selectedNode, m.liveUnsent())
	}
}

func TestSomeoneElsesChangeShowsUpAndKeepsTheSelection(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))
	m.selectOnly(1)

	f.send("change", map[string]any{"seq": 2, "epoch": "e", "client": "web", "user_id": 2,
		"graph": apiGraph(apiNodeAt("new", "Output", "First", -300, 0), apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Renamed", 300, 0))})
	m = d.until(m, "their change shows", func(m model) bool { return len(m.editorNodes) == 3 })

	if node, _ := m.selectedEditorNode(); node.id != "log" || node.label != "Renamed" {
		t.Fatalf("selected = %+v", node)
	}
	if len(f.sentChanges()) != 0 {
		t.Fatal("taking their change sent something back")
	}
}

func TestAFormOpenWaitsForTheChangesOfOthers(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))
	m.selectOnly(1)
	m.openNodeModal()

	f.send("change", map[string]any{"seq": 2, "epoch": "e", "client": "web",
		"graph": apiGraph(apiNodeAt("start", "Start", "Start", 0, 0))})
	m = d.until(m, "the change arrives", func(m model) bool { return m.livePending != nil })
	if len(m.editorNodes) != 2 {
		t.Fatal("the node of the open form went away under it")
	}

	m.closeNodeForm()
	next, _ := m.Update(nil)
	if got := next.(model); len(got.editorNodes) != 1 {
		t.Fatalf("after the form, nodes = %d", len(got.editorNodes))
	}
}

func TestUndoTakesBackOnlyWhatThisPersonDid(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))

	m.selectOnly(1)
	m = press(m, tea.KeyRight, tea.ModShift)
	next, cmd := m.Update(nil)
	m = next.(model)
	d.run(cmd)
	m = d.until(m, "the move is saved", func(m model) bool { return len(m.liveUndo) == 1 && !m.liveSending })

	next, cmd = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	m = next.(model)
	d.run(cmd)
	m = d.until(m, "the undo is answered", func(m model) bool { return len(m.liveRedo) == 1 && !m.liveSending })

	changes := f.sentChanges()
	undo := changes[len(changes)-1]
	if fmt.Sprint(undo["base"]) != fmt.Sprint(changes[0]["graph"]) || fmt.Sprint(undo["graph"]) != fmt.Sprint(changes[0]["base"]) {
		t.Fatal("undo didn't send the move backwards")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	m = next.(model)
	d.run(cmd)
	m = d.until(m, "the redo is answered", func(m model) bool { return len(m.liveUndo) == 1 && len(m.liveRedo) == 0 && !m.liveSending })

	redo := f.sentChanges()[2]
	if fmt.Sprint(redo["graph"]) != fmt.Sprint(changes[0]["graph"]) {
		t.Fatal("redo didn't send the move again")
	}
}

func TestAChangeThatNoLongerAppliesTakesTheFlowAsItIs(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))
	f.status = http.StatusConflict

	m.selectOnly(1)
	m = press(m, tea.KeyRight, tea.ModShift)
	next, cmd := m.Update(nil)
	m = next.(model)
	d.run(cmd)
	m = d.until(m, "the conflict is said", func(m model) bool {
		return strings.Contains(m.editorStatusMessage, "Someone else changed that")
	})
}

func TestOthersShowInTheTopBarAndOnWhatTheySelected(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))

	f.send("presence", map[string]any{"people": []any{
		map[string]any{"client": "me", "kind": "person", "user_id": 1, "name": "pedro", "color": "#2f7a9e", "selected": []any{}},
		map[string]any{"client": "web", "kind": "person", "user_id": 2, "name": "ana", "color": "#b5532a", "selected": []any{"log"}},
	}})
	m = d.until(m, "ana shows", func(m model) bool { return len(m.livePeople) == 1 })

	screen := strings.Join(screenLines(m), "\n")
	if !strings.Contains(screenLines(m)[0], "● ana") || strings.Contains(screenLines(m)[0], "pedro") {
		t.Fatalf("top bar = %q", screenLines(m)[0])
	}
	if !strings.Contains(screen, " ana ") {
		t.Fatal("the node ana selected doesn't say so")
	}
}

func TestSelectingANodeTellsTheOthers(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))

	m = press(m, tea.KeyRight, 0)
	next, cmd := m.Update(nil)
	m = next.(model)
	d.run(cmd)
	d.until(m, "the focus is sent", func(model) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.focus) > 0
	})

	f.mu.Lock()
	defer f.mu.Unlock()
	last := f.focus[len(f.focus)-1]
	if last["client"] != "me" || fmt.Sprint(last["selected"]) != "[log]" {
		t.Fatalf("focus = %+v", last)
	}
}

func TestRunningSendsWhatChangedFirst(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))

	m.selectOnly(1)
	m = press(m, tea.KeyRight, tea.ModShift)
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = next.(model)
	d.run(cmd)

	m = d.until(m, "the run starts", func(model) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.runs == 1
	})
	if len(f.sentChanges()) != 1 {
		t.Fatalf("changes before the run = %d", len(f.sentChanges()))
	}
}

// A Heddle from before the live API answers 404, with its own message, and is saved
// whole a moment after each change.
func TestAServerWithoutTheLiveAPISavesByItself(t *testing.T) {
	var mu sync.Mutex
	saves := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			mu.Lock()
			saves++
			mu.Unlock()
			json.NewEncoder(w).Encode(workflowResponse{Data: apiFlow{ID: 7, Name: "Report"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"code":"not_found","message":"Not Found"}}`)
	}))
	t.Cleanup(server.Close)
	d := newDriver(t)
	m := appModel(140, 40)
	m.activeHost = server.URL
	m.availableNodes = serverNodes

	next, cmd := m.Update(workflowLoadedMsg{flow: apiFlow{ID: 7, Name: "Report", Nodes: []apiNode{{ID: "start", Type: "Start"}, {ID: "log", Type: "Logger", Position: apiPosition{X: 300}}}}})
	m = next.(model)
	d.run(cmd)
	m = d.until(m, "it knows", func(m model) bool { return m.liveState == liveUnsupported })
	if top := screenLines(m)[0]; !strings.Contains(top, "Saved") {
		t.Fatalf("top bar = %q", top)
	}

	m.selectOnly(1)
	m = press(m, tea.KeyRight, tea.ModShift)
	next, cmd = m.Update(nil)
	m = next.(model)
	d.run(cmd)
	m = d.until(m, "it saves", func(m model) bool { return !m.editorDirty && !m.editorSaving })

	mu.Lock()
	defer mu.Unlock()
	if saves != 1 {
		t.Fatalf("saves = %d", saves)
	}
}

func TestOnlyTheChangesOfOthersSaySo(t *testing.T) {
	f, d := newFakeLive(t), newDriver(t)
	m := liveEditor(t, f, d, apiGraph(apiNodeAt("start", "Start", "Start", 0, 0), apiNodeAt("log", "Logger", "Note it", 300, 0)))
	if strings.Contains(m.editorStatusMessage, "someone else") {
		t.Fatalf("opening says %q", m.editorStatusMessage)
	}

	f.send("change", map[string]any{"seq": 2, "epoch": "e", "client": "web",
		"graph": apiGraph(apiNodeAt("start", "Start", "Start", 0, 0))})
	m = d.until(m, "their change shows", func(m model) bool { return len(m.editorNodes) == 1 })
	if m.editorStatusMessage != "Changed by someone else" {
		t.Fatalf("status = %q", m.editorStatusMessage)
	}
}
