package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// pagedServer answers like the Heddle API: never more than the page asked for.
func pagedServer(t *testing.T, total int, item func(int) any) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if page == 0 {
			page = 1
		}
		if perPage == 0 {
			perPage = 20
		}

		data := []any{}
		for i := (page - 1) * perPage; i < min(page*perPage, total); i++ {
			data = append(data, item(i))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": data,
			"meta": map[string]any{"total_pages": (total + perPage - 1) / perPage},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestLoadsEveryNodeOfTheServer(t *testing.T) {
	server := pagedServer(t, 57, func(i int) any {
		return apiNodeDef{Type: fmt.Sprintf("Node %02d", i), Title: fmt.Sprintf("Node %02d", i)}
	})

	nodes, err := loadNodes(context.Background(), server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 57 {
		t.Fatalf("got %d nodes", len(nodes))
	}
}

func TestLoadsEveryWorkflowAcrossPages(t *testing.T) {
	server := pagedServer(t, 230, func(i int) any { return apiFlow{ID: i + 1} })

	workflows, err := loadWorkflows(context.Background(), server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(workflows) != 230 {
		t.Fatalf("got %d workflows", len(workflows))
	}
}

func TestLoadsFromAServerWithoutPagination(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{"data": []apiNodeDef{{Type: "Start"}}})
	}))
	defer server.Close()

	nodes, err := loadNodes(context.Background(), server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || calls != 1 {
		t.Fatalf("%d nodes in %d calls", len(nodes), calls)
	}
}

func TestARefusedKeyStopsTheLoad(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	if _, err := loadWorkflows(context.Background(), server.URL, "bad"); !errors.Is(err, errUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func manyNodesEditor(width, height int) model {
	m := editorModel(width, height)
	m.availableNodes = nil
	for i := range 57 {
		title := fmt.Sprintf("Node %02d", i)
		m.availableNodes = append(m.availableNodes, apiNodeDef{Type: title, Title: title, Inputs: []any{"exec"}, Outputs: []any{"exec"}})
	}
	m.editorOverlay = editorOverlayCreate
	return m
}

func TestThePaletteFollowsTheSelectionPastTheScreen(t *testing.T) {
	m := manyNodesEditor(110, 24)

	if screen := strings.Join(screenLines(m), "\n"); strings.Contains(screen, "Add Node 56") {
		t.Fatal("the last node is on screen before scrolling")
	}

	for range 56 {
		m, _ = m.updateEditorOverlayKey(keyPress("down"))
	}

	screen := strings.Join(screenLines(m), "\n")
	if !strings.Contains(screen, "Add Node 56") || !strings.Contains(screen, "57 of 57") {
		t.Fatalf("the selected node is not on screen:\n%s", screen)
	}
}

func TestClickingAScrolledPaletteAddsTheNodeDrawnThere(t *testing.T) {
	m := manyNodesEditor(110, 24)
	for range 30 {
		m, _ = m.updateEditorOverlayKey(keyPress("down"))
	}

	row := -1
	for i, line := range screenLines(m) {
		if strings.Contains(line, "Add Node 32") {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("Node 32 is not on screen")
	}

	before := len(m.editorNodes)
	r := m.editorMenuRect()
	m.handleEditorOverlayClick(r.x+3, row)

	if len(m.editorNodes) != before+1 || m.editorNodes[before].kind != "Node 32" {
		t.Fatalf("nodes = %+v", m.editorNodes[before:])
	}
}
