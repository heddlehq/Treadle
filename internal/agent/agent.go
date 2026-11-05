// Package agent runs the claude CLI as a copilot for Heddle flows.
//
// Each message is one `claude -p`, tied to the previous ones by a session id, and
// limited to the MCP tools of the connected Heddle server: the built-in tools are
// off, so it can't read files or run commands.
//
// Anthropic doesn't allow third-party products to offer claude.ai login, so this
// only takes an API key, and it is shown to people as "Claude Agent".
package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultBudgetUSD = "1"
	tokenVariable    = "HEDDLE_MCP_TOKEN"
	maxLineBytes     = 8 << 20
	stderrTailBytes  = 400
)

// runFlowTool is left out of every message until a person allows it: running a flow
// can have real side effects.
const runFlowTool = "mcp__heddle__run_flow"

var safeTools = []string{
	"mcp__heddle__list_flows",
	"mcp__heddle__get_flow",
	"mcp__heddle__create_flow",
	"mcp__heddle__update_flow",
	"mcp__heddle__list_nodes",
	"mcp__heddle__list_executions",
	"mcp__heddle__get_execution",
}

// inheritedEnv is what the binary needs to start. Everything else stays behind.
var inheritedEnv = []string{
	"PATH", "HOME", "LANG", "LC_ALL", "LANGUAGE", "TMPDIR",
	"USERPROFILE", "APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "TEMP", "TMP",
}

// Options describes one message to the agent.
type Options struct {
	Binary    string
	APIKey    string
	Prompt    string
	System    string
	MCPURL    string
	Token     string
	SessionID string
	Resume    bool
	AllowRun  bool
	Budget    string
}

// Problem says why the agent can't run here, or returns "" when it can.
func Problem() string {
	if Binary() == "" {
		return "Claude Agent needs the claude binary: install it or set TREADLE_CLAUDE_BIN."
	}
	if APIKey() == "" {
		return "Claude Agent needs ANTHROPIC_API_KEY. Claude subscriptions can't be used here."
	}
	return ""
}

// Binary finds the claude executable.
func Binary() string {
	if path := os.Getenv("TREADLE_CLAUDE_BIN"); path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
		return ""
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return ""
	}
	return path
}

// APIKey returns the key the agent runs with.
func APIKey() string {
	return os.Getenv("ANTHROPIC_API_KEY")
}

// Budget returns the most one message may spend, in dollars.
func Budget() string {
	if budget := os.Getenv("TREADLE_CLAUDE_MAX_BUDGET_USD"); budget != "" {
		return budget
	}
	return defaultBudgetUSD
}

// NewSessionID returns the UUID of a new conversation.
func NewSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// SystemPrompt tells the agent which flow is open.
func SystemPrompt(flowID int, flowName string) string {
	return fmt.Sprintf(`You are the copilot of Treadle, a terminal editor for Heddle workflows. You work
through the heddle tools, and they are the only way you can act.

The user has the flow %q (id %d) open. Assume requests are about it unless told
otherwise.

- Read the flow with get_flow before changing it, and call list_nodes when you need
  a node type.
- A flow starts at exactly one Start or Webhook node and ends with an Output node.
- update_flow saves, and the editor refreshes when you finish: never ask the user to
  reload.
- run_flow needs the user's confirmation. If it is denied, don't look for a way
  around it and don't mention permission modes: say in one sentence that you will
  run the flow once they confirm, and stop.
- To debug, read the latest execution with list_executions and get_execution.
- You can't delete flows.

Answer in the language of the user, and be brief: this is a small terminal panel.`, flowName, flowID)
}

// Args builds the command line of a message.
func Args(opts Options) []string {
	tools := append([]string{}, safeTools...)
	if opts.AllowRun {
		tools = append(tools, runFlowTool)
	}

	session := []string{"--session-id", opts.SessionID}
	if opts.Resume {
		session = []string{"--resume", opts.SessionID}
	}

	args := []string{
		"-p",
		"--bare",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--tools", "",
		"--strict-mcp-config",
		"--mcp-config", mcpConfig(opts.MCPURL),
		"--allowedTools", strings.Join(tools, ","),
		"--permission-mode", "dontAsk",
		"--max-budget-usd", opts.Budget,
		"--append-system-prompt", opts.System,
	}
	args = append(args, session...)

	// A message that looks like a flag must stay a message.
	return append(args, "--", opts.Prompt)
}

// Env builds the environment of a message from the current one.
func Env(opts Options, environ []string) []string {
	var env []string
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		for _, allowed := range inheritedEnv {
			if strings.EqualFold(name, allowed) {
				env = append(env, entry)
				break
			}
		}
	}
	return append(env, "ANTHROPIC_API_KEY="+opts.APIKey, tokenVariable+"="+opts.Token)
}

// The token stays out of the arguments, which any process on the machine can read.
func mcpConfig(url string) string {
	config, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"heddle": map[string]any{
				"type":    "http",
				"url":     url,
				"headers": map[string]string{"Authorization": "Bearer ${" + tokenVariable + "}"},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(config)
}

// Start sends a message. The channel closes when the process is gone, and cancelling
// ctx kills it.
func Start(ctx context.Context, opts Options) (<-chan Event, error) {
	dir := filepath.Join(os.TempDir(), "treadle-agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, opts.Binary, Args(opts)...)
	cmd.Dir = dir
	cmd.Env = Env(opts, os.Environ())
	cmd.WaitDelay = 2 * time.Second

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	events := make(chan Event)
	go func() {
		defer close(events)

		send := func(event Event) {
			select {
			case events <- event:
			case <-ctx.Done():
			}
		}

		failed := false
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
		for scanner.Scan() {
			for _, event := range Parse(scanner.Bytes()) {
				failed = failed || event.Type == EventError
				send(event)
			}
		}

		err := cmd.Wait()
		if err != nil && !failed && !errors.Is(ctx.Err(), context.Canceled) {
			send(Event{Type: EventError, Text: failureMessage(err, stderr.String())})
		}
	}()

	return events, nil
}

func failureMessage(err error, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if len(stderr) > stderrTailBytes {
		stderr = stderr[len(stderr)-stderrTailBytes:]
	}
	if stderr == "" {
		return "The agent stopped: " + err.Error()
	}
	return "The agent stopped: " + stderr
}
