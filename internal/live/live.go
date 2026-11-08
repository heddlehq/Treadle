// Package live edits a flow together with everyone else who has it open, through the
// live API of Heddle: it follows the draft as server-sent events, sends what changed
// here, and says which nodes the person points at.
//
// What it sends is always a pair of whole graphs, the one the change was made on and
// the one it made, so the server applies only that difference, on top of what anyone
// else did meanwhile. Nothing here knows how a graph changes.
package live

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Graph is a draft as the API has it: nodes, connections and comments, left as JSON
// for whoever draws them.
type Graph struct {
	Nodes       json.RawMessage `json:"nodes"`
	Connections json.RawMessage `json:"connections"`
	Comments    json.RawMessage `json:"comments"`
}

// Person is someone with the flow open, on the web or in a terminal.
type Person struct {
	Client   string   `json:"client"`
	Kind     string   `json:"kind"`
	UserID   int      `json:"user_id"`
	Name     string   `json:"name"`
	Color    string   `json:"color"`
	Selected []string `json:"selected"`
	Editing  *string  `json:"editing"`
}

// Ready starts a stream, and starts it again when the server lost track of it: it has
// the whole draft.
type Ready struct {
	Client  string `json:"client"`
	CanEdit bool   `json:"can_edit"`
	UserID  int    `json:"user_id"`
	Seq     int    `json:"seq"`
	Epoch   any    `json:"epoch"`
	Graph   Graph  `json:"graph"`
}

// Change is what someone changed, with the draft after it.
type Change struct {
	Seq    int    `json:"seq"`
	Epoch  any    `json:"epoch"`
	UserID int    `json:"user_id"`
	Client string `json:"client"`
	Graph  Graph  `json:"graph"`
}

// Cursor is where someone's pointer is on a canvas of the web, in the coordinates of
// the flow. Nil coordinates are a pointer that left it.
type Cursor struct {
	Client string   `json:"client"`
	X      *float64 `json:"x"`
	Y      *float64 `json:"y"`
}

// Event is one of the events of a stream. Only one field is set.
type Event struct {
	Ready    *Ready
	Change   *Change
	Presence []Person
	Cursor   *Cursor
}

// Applied is the answer to a change: the draft right after it.
type Applied struct {
	Seq     int   `json:"seq"`
	Applied *int  `json:"applied"`
	Graph   Graph `json:"graph"`
}

// ErrConflict is a change that no longer applies, because what it changed is gone.
var ErrConflict = errors.New("someone else changed that meanwhile")

// ErrNotFound is a flow that isn't there, or a Heddle from before the live API.
var ErrNotFound = errors.New("not found")

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) url(flowID int, path string) string {
	return fmt.Sprintf("%s/api/v1/workflows/%d%s", c.BaseURL, flowID, path)
}

// Follow reads the stream of a flow until it ends or ctx is done.
func (c *Client) Follow(ctx context.Context, flowID int, event func(Event)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(flowID, "/live"), nil)
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

	if err := check(response); err != nil {
		return err
	}
	return read(response.Body, event)
}

// Change sends what changed here: graph is base after the change.
func (c *Client) Change(ctx context.Context, flowID int, client string, base, graph any) (Applied, error) {
	var answer struct {
		Data Applied `json:"data"`
	}
	err := c.post(ctx, flowID, "/changes", map[string]any{"client": client, "base": base, "graph": graph}, &answer)
	return answer.Data, err
}

// Focus says which nodes this person has selected, and which one they edit.
func (c *Client) Focus(ctx context.Context, flowID int, client string, selected []string, editing string) error {
	body := map[string]any{"client": client, "selected": selected, "editing": nil}
	if editing != "" {
		body["editing"] = editing
	}
	return c.post(ctx, flowID, "/focus", body, nil)
}

func (c *Client) post(ctx context.Context, flowID int, path string, body, into any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(flowID, path), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Token)

	response, err := c.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if err := check(response); err != nil {
		return err
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(into)
}

func check(response *http.Response) error {
	if response.StatusCode < 300 {
		return nil
	}
	switch response.StatusCode {
	case http.StatusConflict:
		return ErrConflict
	case http.StatusNotFound:
		return ErrNotFound
	}

	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		return errors.New(body.Error.Message)
	}
	return errors.New("the server answered " + response.Status)
}

// read takes server-sent events. A flow can be large, so a line can be too.
func read(body io.Reader, event func(Event)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)

	var name string
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if name != "" {
				dispatch(name, data.String(), event)
			}
			name = ""
			data.Reset()
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return scanner.Err()
}

func dispatch(name, data string, event func(Event)) {
	switch name {
	case "ready":
		var ready Ready
		if json.Unmarshal([]byte(data), &ready) == nil {
			event(Event{Ready: &ready})
		}
	case "change":
		var change Change
		if json.Unmarshal([]byte(data), &change) == nil {
			event(Event{Change: &change})
		}
	case "presence":
		var presence struct {
			People []Person `json:"people"`
		}
		if json.Unmarshal([]byte(data), &presence) == nil {
			event(Event{Presence: presence.People})
		}
	case "cursor":
		var cursor Cursor
		if json.Unmarshal([]byte(data), &cursor) == nil {
			event(Event{Cursor: &cursor})
		}
	}
}

// Backoff is how long to wait before following again, after tries failures in a row.
func Backoff(tries int) time.Duration {
	wait := time.Second << min(tries, 4)
	return min(wait, 15*time.Second)
}
