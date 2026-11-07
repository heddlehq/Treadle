package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// errUnauthorized means the server is reachable but refused the API key.
var errUnauthorized = errors.New("unauthorized")

// maxPerPage is the most items the API returns at once.
const maxPerPage = 100

type pageResponse[T any] struct {
	Data []T `json:"data"`
	Meta struct {
		TotalPages int `json:"total_pages"`
	} `json:"meta"`
}

type workflowResponse struct {
	Data apiFlow `json:"data"`
}

type workflowRequest struct {
	Workflow workflowPayload `json:"workflow"`
}

type workflowPayload struct {
	Name        string              `json:"name"`
	Nodes       []apiNode           `json:"nodes"`
	Connections []apiSaveConnection `json:"connections"`
	Comments    []apiComment        `json:"comments"`
}

type apiFlow struct {
	ID          int             `json:"id"`
	Name        string          `json:"name"`
	Nodes       []apiNode       `json:"nodes"`
	InsertedAt  string          `json:"inserted_at"`
	UpdatedAt   string          `json:"updated_at"`
	Connections []apiConnection `json:"connections"`
	Comments    []apiComment    `json:"comments"`
}

type apiComment struct {
	ID       any         `json:"id"`
	Kind     string      `json:"kind"`
	Color    string      `json:"color"`
	Text     string      `json:"text"`
	Position apiPosition `json:"position"`
	Width    float64     `json:"width"`
	Height   float64     `json:"height"`
}

type apiNode struct {
	ID       any            `json:"id"`
	Type     string         `json:"type"`
	Label    string         `json:"label"`
	Position apiPosition    `json:"position"`
	Controls map[string]any `json:"controls"`
}

type apiPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type apiConnection struct {
	Source       any    `json:"source"`
	SourceOutput string `json:"sourceOutput"`
	Target       any    `json:"target"`
	TargetInput  string `json:"targetInput"`
}

type apiSaveConnection struct {
	Source       any    `json:"source"`
	SourceOutput string `json:"sourceOutput"`
	Target       any    `json:"target"`
	TargetInput  string `json:"targetInput"`
}

type apiNodeDef struct {
	Type        string       `json:"type"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Category    string       `json:"category"`
	Icon        string       `json:"icon"`
	Inputs      []any        `json:"inputs"`
	Outputs     []any        `json:"outputs"`
	UIFields    []apiUIField `json:"ui_fields"`
}

func (m model) startConnection() (model, tea.Cmd) {
	server := strings.TrimSpace(m.hostInput.Value())
	if server == "" {
		server = defaultServer
		m.hostInput.SetValue(server)
	}

	apiKey := strings.TrimSpace(m.apiKeyInput.Value())

	m.activeMethod = loginAPIKey
	m.connecting = true
	m.connected = false
	m.connectError = ""
	m.hostInput.Blur()
	m.apiKeyInput.Blur()

	return m, tea.Batch(m.spinner.Tick, connectCmd(server, apiKey))
}

func connectCmd(server, apiKey string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		baseURL, err := normalizeBaseURL(server)
		if err != nil {
			return connectionResultMsg{err: err}
		}

		if err := tryConnect(ctx, baseURL); err != nil {
			return connectionResultMsg{err: err}
		}

		return connectionResultMsg{host: baseURL, apiKey: apiKey}
	}
}

func loadWorkflowsCmd(host, apiKey string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		workflows, err := loadWorkflows(ctx, host, apiKey)
		if err != nil {
			return workflowsLoadedMsg{err: err}
		}

		nodes, err := loadNodes(ctx, host, apiKey)

		return workflowsLoadedMsg{
			workflows: workflows,
			nodes:     nodes,
			err:       err,
		}
	}
}

func tryConnect(ctx context.Context, baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	address := u.Host
	if !strings.Contains(address, ":") {
		if u.Scheme == "https" {
			address += ":443"
		} else {
			address += ":80"
		}
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}

	return conn.Close()
}

func normalizeBaseURL(server string) (string, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", fmt.Errorf("empty server")
	}

	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		server = "http://" + server
	}

	u, err := url.Parse(server)
	if err != nil {
		return "", fmt.Errorf("invalid server: %w", err)
	}

	base := strings.TrimRight(u.String(), "/")

	return base, nil
}

// fetchAllPages walks a paginated list. The API stops at 20 items unless asked for
// more, and 100 is the most it gives at once.
func fetchAllPages[T any](ctx context.Context, baseURL, apiKey, resource string) ([]T, error) {
	var items []T
	for page := 1; ; page++ {
		payload, err := fetchPage[T](ctx, baseURL, apiKey, resource, page)
		if err != nil {
			return nil, err
		}
		items = append(items, payload.Data...)
		if page >= payload.Meta.TotalPages {
			return items, nil
		}
	}
}

func fetchPage[T any](ctx context.Context, baseURL, apiKey, resource string, page int) (pageResponse[T], error) {
	var payload pageResponse[T]

	target := fmt.Sprintf("%s/api/v1/%s?page=%d&per_page=%d", baseURL, resource, page, maxPerPage)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return payload, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: connectTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return payload, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return payload, errUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return payload, fmt.Errorf("%s api returned %s", resource, resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&payload)
	return payload, err
}

func loadWorkflows(ctx context.Context, baseURL, apiKey string) ([]workflow, error) {
	flows, err := fetchAllPages[apiFlow](ctx, baseURL, apiKey, "workflows")
	if err != nil {
		return nil, err
	}

	workflows := make([]workflow, 0, len(flows))
	for _, item := range flows {
		workflows = append(workflows, mapFlow(item))
	}
	return workflows, nil
}

func loadNodes(ctx context.Context, baseURL, apiKey string) ([]apiNodeDef, error) {
	nodes, err := fetchAllPages[apiNodeDef](ctx, baseURL, apiKey, "nodes")
	if err != nil {
		return nil, err
	}

	// Group them properly, ignoring case for categories
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			catI := strings.ToUpper(nodes[i].Category)
			catJ := strings.ToUpper(nodes[j].Category)
			if catI > catJ || (catI == catJ && nodes[i].Title > nodes[j].Title) {
				nodes[i], nodes[j] = nodes[j], nodes[i]
			}
		}
	}

	return nodes, nil
}

func loadWorkflowCmd(host, apiKey string, workflowID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		flow, err := loadWorkflow(ctx, host, apiKey, workflowID)
		return workflowLoadedMsg{flow: flow, err: err}
	}
}

func loadWorkflow(ctx context.Context, baseURL, apiKey string, workflowID int) (apiFlow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/workflows/%d", baseURL, workflowID), nil)
	if err != nil {
		return apiFlow{}, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: connectTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return apiFlow{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiFlow{}, fmt.Errorf("workflow api returned %s", resp.Status)
	}

	var payload workflowResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return apiFlow{}, err
	}

	return payload.Data, nil
}

func saveWorkflowCmd(host, apiKey string, workflowID int, req workflowRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		flow, err := saveWorkflow(ctx, host, apiKey, workflowID, req)
		return workflowSavedMsg{flow: flow, err: err}
	}
}

func saveWorkflow(ctx context.Context, baseURL, apiKey string, workflowID int, req workflowRequest) (apiFlow, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return apiFlow{}, err
	}

	// A workflow with no id only exists here, so the first save creates it.
	method, target := http.MethodPost, baseURL+"/api/v1/workflows"
	if workflowID != 0 {
		method, target = http.MethodPatch, fmt.Sprintf("%s/api/v1/workflows/%d", baseURL, workflowID)
	}

	request, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(string(body)))
	if err != nil {
		return apiFlow{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: connectTimeout}
	resp, err := client.Do(request)
	if err != nil {
		return apiFlow{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiFlow{}, fmt.Errorf("save workflow api returned %s", resp.Status)
	}

	var payload workflowResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return apiFlow{}, err
	}

	return payload.Data, nil
}

func mapFlow(item apiFlow) workflow {
	updatedAt := parseAPITime(item.UpdatedAt)
	insertedAt := parseAPITime(item.InsertedAt)
	return workflow{
		id:          item.ID,
		name:        fallbackString(item.Name, fmt.Sprintf("Flow %d", item.ID)),
		status:      deriveWorkflowStatus(item),
		updated:     relativeTime(updatedAt),
		created:     relativeTime(insertedAt),
		nodes:       len(item.Nodes),
		connections: len(item.Connections),
	}
}

func deriveWorkflowStatus(item apiFlow) string {
	status := ""
	for _, node := range item.Nodes {
		if raw, ok := node.Controls["status"]; ok {
			if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
				status = strings.TrimSpace(value)
			}
		}
	}

	switch strings.ToLower(status) {
	case "failed", "error":
		return "Failed"
	case "running":
		return "Running"
	case "success":
		return "Active"
	}

	if len(item.Nodes) > 0 {
		return "Active"
	}
	return "Idle"
}

func parseAPITime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
