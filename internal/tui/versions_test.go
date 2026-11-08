package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type fakeVersions struct {
	mu       sync.Mutex
	requests []string
	notes    []string
}

func (f *fakeVersions) seen(request string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if r == request {
			return true
		}
	}
	return false
}

func versionsEditor(t *testing.T) (model, *fakeVersions) {
	t.Helper()
	f := &fakeVersions{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if note, ok := body["note"].(string); ok {
			f.notes = append(f.notes, note)
		}
		f.mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			json.NewEncoder(w).Encode(map[string]any{"data": []any{
				map[string]any{"id": 11, "number": 1, "kind": "publish", "live": false, "note": "first", "inserted_at": "2026-11-01T10:00:00Z",
					"author": map[string]any{"id": 1, "username": "pedro"}, "changes": map[string]any{"nodes_added": 2}},
				map[string]any{"id": 12, "number": 2, "kind": "publish", "live": true, "note": "faster", "inserted_at": "2026-11-02T10:00:00Z",
					"author": map[string]any{"id": 2, "email": "ana@example.com"}, "changes": map[string]any{"nodes_changed": 1},
					"contributors": []any{map[string]any{"id": 2, "email": "ana@example.com"}}},
			}})
		case strings.HasSuffix(r.URL.Path, "/publish"):
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 13, "number": 3, "kind": "publish", "live": true}})
		case strings.HasSuffix(r.URL.Path, "/restore"):
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 7}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	m := editorModel(140, 40)
	m.activeHost = server.URL
	m.editorWorkflowID = 7
	m.editorWorkflowName = "Report"
	return m, f
}

func TestPPublishesTheDraftWithANote(t *testing.T) {
	m, f := versionsEditor(t)
	d := newDriver(t)

	m, _ = m.updateWorkflowEditorKey(keyPress("p"))
	if m.editorOverlay != editorOverlayPublish {
		t.Fatalf("overlay = %q", m.editorOverlay)
	}
	m = typeIntoPublish(m, "new step")
	m, cmd := m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	d.run(cmd)
	m = d.until(m, "it is published", func(m model) bool { return strings.Contains(m.editorStatusMessage, "Published version 3") })

	if !f.seen("POST /api/v1/workflows/7/publish") || f.notes[0] != "new step" {
		t.Fatalf("requests = %v, notes = %v", f.requests, f.notes)
	}
}

func typeIntoPublish(m model, text string) model {
	for _, r := range text {
		m, _ = m.updateWorkflowEditorKey(keyPress(string(r)))
	}
	return m
}

func TestVersionsShowWhoChangedWhatAndWhichIsLive(t *testing.T) {
	m, _ := versionsEditor(t)
	d := newDriver(t)

	m, cmd := m.updateWorkflowEditorKey(keyPress("v"))
	d.run(cmd)
	m = d.until(m, "the versions load", func(m model) bool { return len(m.versions) == 2 })

	screen := strings.Join(screenLines(m), "\n")
	for _, want := range []string{"v2", "v1", "faster", "live", "ana", "1 node changed"} {
		if !strings.Contains(screen, want) {
			t.Errorf("%q is not on screen", want)
		}
	}
	if m.versions[0].Number != 2 {
		t.Fatal("the newest version isn't first")
	}
}

func TestRestoringAVersionAsksFirst(t *testing.T) {
	m, f := versionsEditor(t)
	d := newDriver(t)
	m, cmd := m.updateWorkflowEditorKey(keyPress("v"))
	d.run(cmd)
	m = d.until(m, "the versions load", func(m model) bool { return len(m.versions) == 2 })

	m, _ = m.updateWorkflowEditorKey(keyPress("j"))
	m, _ = m.updateWorkflowEditorKey(keyPress("r"))
	if f.seen("POST /api/v1/workflows/7/versions/11/restore") {
		t.Fatal("restored without asking")
	}
	if !strings.Contains(strings.Join(screenLines(m), "\n"), "Put version 1 in the draft") {
		t.Fatal("it doesn't ask")
	}

	m, cmd = m.updateWorkflowEditorKey(keyPress("y"))
	d.run(cmd)
	m = d.until(m, "it is restored", func(m model) bool { return strings.Contains(m.editorStatusMessage, "Version 1 is the draft") })
	if !f.seen("POST /api/v1/workflows/7/versions/11/restore") {
		t.Fatalf("requests = %v", f.requests)
	}
}

func TestAnOlderVersionCanBePublishedAgain(t *testing.T) {
	m, f := versionsEditor(t)
	d := newDriver(t)
	m, cmd := m.updateWorkflowEditorKey(keyPress("v"))
	d.run(cmd)
	m = d.until(m, "the versions load", func(m model) bool { return len(m.versions) == 2 })

	m, _ = m.updateWorkflowEditorKey(keyPress("j"))
	m, _ = m.updateWorkflowEditorKey(keyPress("p"))
	m, cmd = m.updateWorkflowEditorKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	d.run(cmd)
	d.until(m, "it is published", func(model) bool { return f.seen("POST /api/v1/workflows/7/versions/11/publish") })
}
