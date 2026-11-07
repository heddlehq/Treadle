// Package agent talks to the agent of a Heddle server. The agent runs on the server:
// this only sends what the user says and follows what comes back.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ErrUnauthorized means the server refused the token.
var ErrUnauthorized = errors.New("unauthorized")

// Decisions on a request to run a flow.
const (
	DecideOnce   = "once"
	DecideAlways = "always"
	DecideDeny   = "deny"
)

// What an item of the conversation can be.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleError     = "error"
)

// How a tool call can be.
const (
	ToolRunning = "running"
	ToolOK      = "ok"
	ToolFailed  = "error"
)

const (
	StatusIdle    = "idle"
	StatusRunning = "running"
)

// Item is a part of the conversation.
type Item struct {
	ID     string         `json:"id"`
	Role   string         `json:"role"`
	Text   string         `json:"text"`
	Name   string         `json:"name"`
	Args   map[string]any `json:"args"`
	Status string         `json:"status"`
	Output string         `json:"output"`
}

// Permission is the agent asking to run a flow.
type Permission struct {
	FlowName string `json:"flow_name"`
}

// Transcript is the conversation. The server keeps the same one, built from the same
// events, in HeddleUI.Agent.Transcript.
type Transcript struct {
	Items      []Item      `json:"items"`
	Status     string      `json:"status"`
	Permission *Permission `json:"permission"`
}

// Event is one thing that happened in the conversation.
type Event struct {
	Type       string         `json:"type"`
	Text       string         `json:"text"`
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Args       map[string]any `json:"args"`
	OK         bool           `json:"ok"`
	Output     string         `json:"output"`
	Message    string         `json:"message"`
	Status     string         `json:"status"`
	Permission *Permission    `json:"permission"`
	Items      []Item         `json:"items"`
}

// Apply brings the transcript up to date with an event.
func (t *Transcript) Apply(event Event) {
	switch event.Type {
	case "user_message":
		t.push(Item{Role: RoleUser, Text: event.Text})
	case "text_delta":
		if last := len(t.Items) - 1; last >= 0 && t.Items[last].Role == RoleAssistant {
			t.Items[last].Text += event.Text
		} else {
			t.push(Item{Role: RoleAssistant, Text: event.Text})
		}
	case "tool_started":
		t.Items = append(t.Items, Item{
			ID: event.ID, Role: RoleTool, Name: event.Name, Args: event.Args, Status: ToolRunning,
		})
	case "tool_finished":
		for i := range t.Items {
			if t.Items[i].Role == RoleTool && t.Items[i].ID == event.ID {
				t.Items[i].Status = ToolFailed
				if event.OK {
					t.Items[i].Status = ToolOK
				}
				t.Items[i].Output = event.Output
			}
		}
	case "failed":
		t.push(Item{Role: RoleError, Text: event.Message})
	case "status":
		t.Status = event.Status
		if event.Status == StatusIdle {
			t.settle()
		}
	case "permission":
		t.Permission = event.Permission
	case "reset":
		*t = Transcript{Items: event.Items, Status: StatusIdle}
	}
}

// settle closes the tools a stop cut off, which will never report back.
func (t *Transcript) settle() {
	for i := range t.Items {
		if t.Items[i].Role == RoleTool && t.Items[i].Status == ToolRunning {
			t.Items[i].Status = ToolFailed
		}
	}
}

func (t *Transcript) push(item Item) {
	item.ID = "item-" + strconv.Itoa(len(t.Items)+1)
	t.Items = append(t.Items, item)
}

// Update is what Follow reports: the whole conversation, or an event of it.
type Update struct {
	Snapshot *Transcript
	Event    *Event
}

// Client reaches the agent of one server.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c *Client) url(flowID int, path string) string {
	return fmt.Sprintf("%s/api/v1/workflows/%d/agent%s", c.BaseURL, flowID, path)
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Send gives the agent a message. It answers through Follow.
func (c *Client) Send(ctx context.Context, flowID int, content string) error {
	return c.post(ctx, flowID, "/messages", map[string]string{"content": content})
}

// Decide answers a request to run a flow.
func (c *Client) Decide(ctx context.Context, flowID int, decision string) error {
	return c.post(ctx, flowID, "/decision", map[string]string{"decision": decision})
}

// New starts a new conversation, unless the agent is working on the current one.
func (c *Client) New(ctx context.Context, flowID int) error {
	return c.post(ctx, flowID, "/new", map[string]string{})
}

// Stop ends what the agent is doing.
func (c *Client) Stop(ctx context.Context, flowID int) error {
	return c.post(ctx, flowID, "/stop", map[string]string{})
}

func (c *Client) post(ctx context.Context, flowID int, path string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(flowID, path), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, err := c.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	return checkStatus(response)
}

func checkStatus(response *http.Response) error {
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case response.StatusCode >= http.StatusBadRequest:
		return errors.New(errorMessage(response))
	}
	return nil
}

func errorMessage(response *http.Response) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if json.Unmarshal(data, &body) == nil && body.Error.Message != "" {
		return body.Error.Message
	}
	return "the server answered " + response.Status
}

// Follow reads the conversation until the connection ends or ctx is done, starting
// with all of it.
func (c *Client) Follow(ctx context.Context, flowID int, update func(Update)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(flowID, "/events"), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, err := c.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if err := checkStatus(response); err != nil {
		return err
	}
	return read(response.Body, update)
}

// read takes server-sent events. A reply can be long, so a line can be too.
func read(body io.Reader, update func(Update)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var name, data string
	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case line == "":
			if name != "" {
				dispatch(name, data, update)
			}
			name, data = "", ""
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	return scanner.Err()
}

func dispatch(name, data string, update func(Update)) {
	if name == "snapshot" {
		var transcript Transcript
		if json.Unmarshal([]byte(data), &transcript) == nil {
			update(Update{Snapshot: &transcript})
		}
		return
	}

	var event Event
	if json.Unmarshal([]byte(data), &event) == nil {
		update(Update{Event: &event})
	}
}
