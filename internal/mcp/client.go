// Package mcp is a small client for the MCP server of a Heddle instance.
package mcp

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
	"sync"
)

const (
	protocolVersion = "2025-06-18"
	sessionHeader   = "Mcp-Session-Id"
	maxLineBytes    = 8 << 20
)

// ErrUnauthorized means the server refused the token.
var ErrUnauthorized = errors.New("unauthorized")

// Client talks to one server. It connects on the first call and is safe to share.
type Client struct {
	URL   string
	Token string

	mu      sync.Mutex
	session string
	nextID  int
}

type rpcResponse struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type toolResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// Call runs a tool and returns its text. A failure the tool reports comes back as an
// error with its message.
func (c *Client) Call(ctx context.Context, tool string, arguments any) (string, error) {
	text, err := c.call(ctx, tool, arguments)

	// The server forgets sessions, for example when it restarts.
	if errors.Is(err, errSessionGone) {
		c.setSession("")
		return c.call(ctx, tool, arguments)
	}
	return text, err
}

var errSessionGone = errors.New("session gone")

func (c *Client) call(ctx context.Context, tool string, arguments any) (string, error) {
	if err := c.connect(ctx); err != nil {
		return "", err
	}

	raw, err := c.request(ctx, "tools/call", map[string]any{"name": tool, "arguments": arguments})
	if err != nil {
		return "", err
	}

	var result toolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("invalid tool result: %w", err)
	}

	parts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		parts = append(parts, item.Text)
	}
	text := strings.Join(parts, "\n")

	if result.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

func (c *Client) connect(ctx context.Context) error {
	if c.currentSession() != "" {
		return nil
	}

	_, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "treadle", "version": "1"},
	})
	if err != nil {
		return err
	}
	return c.notify(ctx, "notifications/initialized")
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.takeID()

	response, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if session := response.Header.Get(sessionHeader); session != "" {
		c.setSession(session)
	}

	message, err := readResponse(response, id)
	if err != nil {
		return nil, err
	}
	if message.Error != nil {
		return nil, errors.New(message.Error.Message)
	}
	return message.Result, nil
}

func (c *Client) notify(ctx context.Context, method string) error {
	response, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "method": method, "params": map[string]any{}})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func (c *Client) post(ctx context.Context, body any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+c.Token)
	if session := c.currentSession(); session != "" {
		request.Header.Set(sessionHeader, session)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}

	switch {
	case response.StatusCode == http.StatusUnauthorized:
		response.Body.Close()
		return nil, ErrUnauthorized
	case response.StatusCode == http.StatusNotFound && c.currentSession() != "":
		response.Body.Close()
		return nil, errSessionGone
	case response.StatusCode >= http.StatusBadRequest:
		response.Body.Close()
		return nil, fmt.Errorf("the server answered %s", response.Status)
	}
	return response, nil
}

// readResponse handles both ways a server may answer: plain JSON, or an event stream
// that carries the response among other messages.
func readResponse(response *http.Response, id int) (rpcResponse, error) {
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		var message rpcResponse
		if err := json.NewDecoder(response.Body).Decode(&message); err != nil {
			return rpcResponse{}, fmt.Errorf("invalid response: %w", err)
		}
		return message, nil
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}

		var message rpcResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &message); err != nil {
			continue
		}
		if message.ID != nil && *message.ID == id {
			return message, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{}, errors.New("the server closed the stream without answering")
}

func (c *Client) takeID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *Client) currentSession() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

func (c *Client) setSession(session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = session
}
