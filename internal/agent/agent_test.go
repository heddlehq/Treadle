package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func play(events ...Event) Transcript {
	var transcript Transcript
	for _, event := range events {
		transcript.Apply(event)
	}
	return transcript
}

func TestRepliesAreJoined(t *testing.T) {
	transcript := play(
		Event{Type: "user_message", Text: "hi"},
		Event{Type: "text_delta", Text: "Hel"},
		Event{Type: "text_delta", Text: "lo"},
	)

	if len(transcript.Items) != 2 || transcript.Items[1].Text != "Hello" {
		t.Fatalf("items = %+v", transcript.Items)
	}
}

func TestAReplyAfterAToolIsANewItem(t *testing.T) {
	transcript := play(
		Event{Type: "text_delta", Text: "Checking."},
		Event{Type: "tool_started", ID: "1", Name: "get_flow"},
		Event{Type: "tool_finished", ID: "1", OK: true, Output: "{}"},
		Event{Type: "text_delta", Text: "Done."},
	)

	if len(transcript.Items) != 3 || transcript.Items[1].Status != ToolOK || transcript.Items[2].Text != "Done." {
		t.Fatalf("items = %+v", transcript.Items)
	}
}

func TestIdsMatchTheServer(t *testing.T) {
	transcript := play(Event{Type: "user_message", Text: "a"}, Event{Type: "text_delta", Text: "b"})

	if transcript.Items[0].ID != "item-1" || transcript.Items[1].ID != "item-2" {
		t.Fatalf("items = %+v", transcript.Items)
	}
}

func TestAToolCutOffDoesNotKeepRunning(t *testing.T) {
	transcript := play(
		Event{Type: "tool_started", ID: "1", Name: "get_flow"},
		Event{Type: "status", Status: StatusIdle},
	)

	if transcript.Items[0].Status != ToolFailed {
		t.Fatalf("items = %+v", transcript.Items)
	}
}

func TestAnAnswerClearsThePermission(t *testing.T) {
	transcript := play(
		Event{Type: "permission", Permission: &Permission{FlowName: "Sum"}},
		Event{Type: "permission"},
	)

	if transcript.Permission != nil {
		t.Fatalf("permission = %+v", transcript.Permission)
	}
}

func TestAResetReplacesTheConversation(t *testing.T) {
	transcript := play(
		Event{Type: "user_message", Text: "old"},
		Event{Type: "reset", Items: []Item{{ID: "item-1", Role: RoleUser, Text: "other"}}},
	)

	if len(transcript.Items) != 1 || transcript.Items[0].Text != "other" {
		t.Fatalf("items = %+v", transcript.Items)
	}
}

func TestFollowReadsTheStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/7/agent/events" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request = %s %v", r.URL.Path, r.Header)
		}
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, "event: snapshot\ndata: {\"items\":[],\"status\":\"idle\",\"permission\":null}\n\n")
		fmt.Fprint(w, "event: tool_started\ndata: {\"type\":\"tool_started\",\"id\":\"1\",\"name\":\"get_flow\",\"args\":{\"flow_id\":\"7\"}}\n\n")
	}))
	defer server.Close()

	var updates []Update
	client := Client{BaseURL: server.URL, Token: "token"}

	if err := client.Follow(context.Background(), 7, func(u Update) { updates = append(updates, u) }); err != nil {
		t.Fatal(err)
	}

	if len(updates) != 2 || updates[0].Snapshot == nil || updates[0].Snapshot.Status != StatusIdle {
		t.Fatalf("updates = %+v", updates)
	}
	if event := updates[1].Event; event == nil || event.Name != "get_flow" || event.Args["flow_id"] != "7" {
		t.Fatalf("event = %+v", updates[1].Event)
	}
}

func TestSendPostsTheMessage(t *testing.T) {
	var body map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/7/agent/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "token"}
	if err := client.Send(context.Background(), 7, "hi"); err != nil {
		t.Fatal(err)
	}
	if body["content"] != "hi" {
		t.Fatalf("body = %+v", body)
	}
}

func TestErrorsSayWhatTheServerSaid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"message":"The agent is still working."}}`)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "token"}
	err := client.Send(context.Background(), 7, "hi")

	if err == nil || err.Error() != "The agent is still working." {
		t.Fatalf("err = %v", err)
	}
}

func TestARefusedTokenIsTold(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Token: "token"}
	err := client.Follow(context.Background(), 7, func(Update) {})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}
