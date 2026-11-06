package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	executionFinished = "succeeded"
	executionFailed   = "failed"

	// How many reads in a row can fail before Treadle stops following a run.
	executionPollRetries = 5

	runningLabel = "Running..."
)

var executionPollInterval = 500 * time.Millisecond

type apiExecution struct {
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Result any              `json:"result"`
	Error  map[string]any   `json:"error"`
	Logs   []map[string]any `json:"logs"`
}

type executionResponse struct {
	Data apiExecution `json:"data"`
}

type executionStartedMsg struct {
	workflowID int
	execution  apiExecution
	err        error
}

type executionPolledMsg struct {
	workflowID int
	execution  apiExecution
	err        error
}

func (e apiExecution) done() bool {
	return e.Status == executionFinished || e.Status == executionFailed
}

// runCurrentWorkflow does what the web editor's Run Flow button does: it saves
// what changed, then starts a manual run.
func (m *model) runCurrentWorkflow() tea.Cmd {
	if m.editorRunInFlight || m.editorRunAfterSave {
		return nil
	}
	if strings.TrimSpace(m.activeHost) == "" {
		m.editorStatusMessage = "Missing server connection"
		return nil
	}

	m.editorExecution = nil
	if m.editorSaving || m.editorDirty || m.editorWorkflowID == 0 {
		m.editorRunAfterSave = true
		return m.saveCurrentWorkflow()
	}
	return m.startExecution()
}

func (m *model) startExecution() tea.Cmd {
	m.editorRunInFlight = true
	m.editorPollFailures = 0
	m.editorStatusMessage = "Starting execution..."
	return startExecutionCmd(m.activeHost, m.activeAPIKey, m.editorWorkflowID)
}

func (m *model) forgetExecution() {
	m.editorExecution = nil
	m.editorRunInFlight = false
	m.editorRunAfterSave = false
	m.editorPollFailures = 0
}

func (m model) handleExecutionStarted(msg executionStartedMsg) (tea.Model, tea.Cmd) {
	if msg.workflowID != m.editorWorkflowID || !m.editorRunInFlight {
		return m, nil
	}
	if msg.err != nil {
		m.editorRunInFlight = false
		m.editorStatusMessage = "Can't run flow: " + truncatePlain(msg.err.Error(), 80)
		m.appendActivity("Failed to run workflow: " + truncatePlain(msg.err.Error(), 80))
		return m, nil
	}

	m.showExecution(msg.execution)
	return m, m.followExecution()
}

func (m model) handleExecutionPolled(msg executionPolledMsg) (tea.Model, tea.Cmd) {
	if msg.workflowID != m.editorWorkflowID || !m.editorRunInFlight {
		return m, nil
	}
	if msg.err != nil {
		m.editorPollFailures++
		if m.editorPollFailures >= executionPollRetries {
			m.editorRunInFlight = false
			m.editorStatusMessage = "Can't read the execution: " + truncatePlain(msg.err.Error(), 80)
			return m, nil
		}
		return m, m.followExecution()
	}

	m.editorPollFailures = 0
	m.showExecution(msg.execution)
	return m, m.followExecution()
}

func (m *model) showExecution(execution apiExecution) {
	m.editorExecution = &execution
	m.editorStatusMessage = executionNotice(execution.Status)
	if execution.done() {
		m.editorRunInFlight = false
		m.appendActivity(executionNotice(execution.Status) + ": " + m.editorWorkflowName)
	}
}

func (m model) followExecution() tea.Cmd {
	if !m.editorRunInFlight || m.editorExecution == nil {
		return nil
	}
	return pollExecutionCmd(m.activeHost, m.activeAPIKey, m.editorWorkflowID, m.editorExecution.ID)
}

func executionNotice(status string) string {
	switch status {
	case "queued":
		return "Execution queued"
	case executionFinished:
		return "Execution finished, press o to inspect"
	case executionFailed:
		return "Execution failed, press o to inspect"
	default:
		return "Execution running"
	}
}

func startExecutionCmd(host, apiKey string, workflowID int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		body, err := json.Marshal(map[string]any{"input": map[string]any{"trigger": "manual"}})
		if err != nil {
			return executionStartedMsg{workflowID: workflowID, err: err}
		}

		target := fmt.Sprintf("%s/api/v1/workflows/%d/executions", host, workflowID)
		execution, err := requestExecution(ctx, http.MethodPost, target, apiKey, body)
		return executionStartedMsg{workflowID: workflowID, execution: execution, err: err}
	}
}

func pollExecutionCmd(host, apiKey string, workflowID int, id string) tea.Cmd {
	return tea.Tick(executionPollInterval, func(time.Time) tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		target := fmt.Sprintf("%s/api/v1/workflows/%d/executions/%s", host, workflowID, id)
		execution, err := requestExecution(ctx, http.MethodGet, target, apiKey, nil)
		return executionPolledMsg{workflowID: workflowID, execution: execution, err: err}
	})
}

func requestExecution(ctx context.Context, method, target, apiKey string, body []byte) (apiExecution, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return apiExecution{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: connectTimeout}
	resp, err := client.Do(request)
	if err != nil {
		return apiExecution{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiExecution{}, fmt.Errorf("execution api returned %s", resp.Status)
	}

	var payload executionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return apiExecution{}, err
	}
	return payload.Data, nil
}

func (m *model) openExecutionResult() {
	if m.editorExecution == nil || !m.editorExecution.done() {
		return
	}
	m.editorOverlay = editorOverlayResult
	m.editorResultScroll = 0
	m.editorResultFull = false
}

func (m model) updateExecutionResultKey(msg tea.KeyPressMsg) model {
	switch msg.String() {
	case "esc", "o":
		m.closeEditorOverlay()
	case "i":
		m.editorResultFull = !m.editorResultFull
		m.editorResultScroll = 0
	case "up":
		m.scrollExecutionResult(-1)
	case "down":
		m.scrollExecutionResult(1)
	case "pgup":
		m.scrollExecutionResult(-m.resultLayout().rows)
	case "pgdown":
		m.scrollExecutionResult(m.resultLayout().rows)
	}
	return m
}

func (m *model) scrollExecutionResult(delta int) {
	last := max(len(m.executionResultLines())-m.resultLayout().rows, 0)
	m.editorResultScroll = clamp(m.editorResultScroll+delta, 0, last)
}

type resultLayout struct {
	width, height, rows int
}

func (m model) resultLayout() resultLayout {
	width := min(92, max(m.width-8, 28))
	height := max(m.height-4, 8)
	// The panel's borders and title, then a blank line and the hints.
	return resultLayout{width: width, height: height, rows: max(height-5, 1)}
}

func (m model) renderExecutionResult() string {
	l := m.resultLayout()
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)

	content := m.executionResultLines()
	first := clamp(m.editorResultScroll, 0, max(len(content)-1, 0))
	visible := content[first:min(first+l.rows, len(content))]

	lines := make([]string, 0, l.rows+2)
	lines = append(lines, visible...)
	for len(lines) < l.rows {
		lines = append(lines, "")
	}

	hint := "i full context"
	title := "Execution Output"
	if m.editorResultFull {
		hint = "i back to result"
		title = "Full Context"
	}
	lines = append(lines, "", muted.Render("↑↓ scroll  "+hint+"  esc close"))

	panel := renderSectionPanel(title, lines, l.width, l.height)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// executionResultLines is what the modal scrolls through, already wrapped to fit.
func (m model) executionResultLines() []string {
	execution := m.editorExecution
	if execution == nil {
		return nil
	}

	var lines []string
	if m.editorResultFull {
		lines = strings.Split(prettyJSON(map[string]any{
			"id":     execution.ID,
			"status": execution.Status,
			"result": execution.Result,
			"error":  execution.Error,
			"logs":   execution.Logs,
		}), "\n")
	} else {
		lines = m.executionOutputLines(*execution)
	}

	width := max(m.resultLayout().width-4, 1)
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapPlainPreserveStyle(line, width)...)
	}
	return wrapped
}

func (m model) executionOutputLines(execution apiExecution) []string {
	muted := lipgloss.NewStyle().Foreground(mutedTextColor)
	statusColor := successColor
	if execution.Status == executionFailed {
		statusColor = errorColor
	}

	lines := []string{
		muted.Render("STATUS  ") + lipgloss.NewStyle().Foreground(statusColor).Bold(true).Render(execution.Status),
		muted.Render("RUN     ") + execution.ID,
		"",
	}

	if message, ok := execution.Error["message"].(string); ok && message != "" {
		lines = append(lines, muted.Render("ERROR"))
		if label := m.nodeLabel(execution.Error["node_id"]); label != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(errorColor).Render("In "+label))
		}
		lines = append(lines, strings.Split(message, "\n")...)
		lines = append(lines, "")
	}

	lines = append(lines, muted.Render("OUTPUT"))
	if isEmptyOutput(execution.Result) {
		return append(lines, muted.Render("No output produced"))
	}
	if text, ok := execution.Result.(string); ok {
		return append(lines, strings.Split(text, "\n")...)
	}
	return append(lines, strings.Split(prettyJSON(execution.Result), "\n")...)
}

func (m model) nodeLabel(id any) string {
	target := apiRefString(id)
	for _, node := range m.editorNodes {
		if node.id == target {
			return node.label
		}
	}
	return target
}

func isEmptyOutput(result any) bool {
	switch value := result.(type) {
	case nil:
		return true
	case map[string]any:
		return len(value) == 0
	default:
		return false
	}
}

func prettyJSON(value any) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimRight(out.String(), "\n")
}
