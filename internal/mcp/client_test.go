package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeServer struct {
	sse      bool
	sessions int
	gone     bool
	calls    []map[string]any
	token    string
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.token = r.Header.Get("Authorization")

	var body struct {
		ID     int            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	switch body.Method {
	case "initialize":
		s.sessions++
		w.Header().Set("Mcp-Session-Id", fmt.Sprintf("session-%d", s.sessions))
		s.reply(w, body.ID, `{"protocolVersion":"2025-06-18"}`)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		if s.gone {
			s.gone = false
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Mcp-Session-Id") == "" {
			t := "no session"
			http.Error(w, t, http.StatusBadRequest)
			return
		}
		s.calls = append(s.calls, body.Params)
		if body.Params["name"] == "fails" {
			s.reply(w, body.ID, `{"isError":true,"content":[{"type":"text","text":"it failed"}]}`)
			return
		}
		s.reply(w, body.ID, `{"isError":false,"content":[{"type":"text","text":"hello"}]}`)
	}
}

func (s *fakeServer) reply(w http.ResponseWriter, id int, result string) {
	message := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, id, result)
	if s.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", message)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, message)
}

func start(t *testing.T, server *fakeServer) *Client {
	t.Helper()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return &Client{URL: httpServer.URL, Token: "secret"}
}

func TestCallReturnsTheText(t *testing.T) {
	server := &fakeServer{}
	client := start(t, server)

	text, err := client.Call(context.Background(), "chat", map[string]any{"a": 1})

	if err != nil || text != "hello" {
		t.Fatalf("text = %q err = %v", text, err)
	}
	if server.calls[0]["name"] != "chat" {
		t.Fatalf("calls = %+v", server.calls)
	}
	if server.token != "Bearer secret" {
		t.Fatalf("token = %q", server.token)
	}
}

func TestCallReadsAnEventStream(t *testing.T) {
	client := start(t, &fakeServer{sse: true})

	text, err := client.Call(context.Background(), "chat", nil)

	if err != nil || text != "hello" {
		t.Fatalf("text = %q err = %v", text, err)
	}
}

func TestCallKeepsTheSession(t *testing.T) {
	server := &fakeServer{}
	client := start(t, server)

	client.Call(context.Background(), "chat", nil)
	client.Call(context.Background(), "chat", nil)

	if server.sessions != 1 {
		t.Fatalf("sessions = %d", server.sessions)
	}
}

func TestCallReconnectsWhenTheSessionIsGone(t *testing.T) {
	server := &fakeServer{}
	client := start(t, server)
	client.Call(context.Background(), "chat", nil)

	server.gone = true
	text, err := client.Call(context.Background(), "chat", nil)

	if err != nil || text != "hello" {
		t.Fatalf("text = %q err = %v", text, err)
	}
	if server.sessions != 2 {
		t.Fatalf("sessions = %d", server.sessions)
	}
}

func TestCallReportsAToolFailure(t *testing.T) {
	client := start(t, &fakeServer{})

	_, err := client.Call(context.Background(), "fails", nil)

	if err == nil || err.Error() != "it failed" {
		t.Fatalf("err = %v", err)
	}
}

func TestCallReportsARefusedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	client := &Client{URL: server.URL, Token: "bad"}
	_, err := client.Call(context.Background(), "chat", nil)

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallStopsWithItsContext(t *testing.T) {
	client := start(t, &fakeServer{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.Call(ctx, "chat", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallDoesntHangOnAnEmptyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, strings.Repeat(": keepalive\n", 3))
	}))
	t.Cleanup(server.Close)

	client := &Client{URL: server.URL, Token: "x"}
	if _, err := client.Call(context.Background(), "chat", nil); err == nil {
		t.Fatal("should fail")
	}
}
