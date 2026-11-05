package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

var options = Options{
	Binary:    "claude",
	APIKey:    "sk-ant",
	Prompt:    "hi",
	System:    "be brief",
	MCPURL:    "http://localhost:4000/mcp",
	Token:     "heddle-token",
	SessionID: "11111111-1111-4111-8111-111111111111",
	Budget:    "1",
}

func after(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "<missing>"
	}
	return args[i+1]
}

func TestArgsTurnBuiltInToolsOff(t *testing.T) {
	args := Args(options)

	if got := after(args, "--tools"); got != "" {
		t.Fatalf("--tools = %q, want empty", got)
	}
	if !slices.Contains(args, "--strict-mcp-config") {
		t.Fatal("other MCP servers would load")
	}
	if got := after(args, "--permission-mode"); got != "dontAsk" {
		t.Fatalf("--permission-mode = %q", got)
	}
}

func TestArgsDontAllowRunningFlowsByDefault(t *testing.T) {
	allowed := after(Args(options), "--allowedTools")

	if strings.Contains(allowed, "run_flow") {
		t.Fatalf("allowed without confirmation: %s", allowed)
	}
	if !strings.Contains(allowed, "mcp__heddle__update_flow") {
		t.Fatalf("can't edit: %s", allowed)
	}
}

func TestArgsAllowRunningOnceConfirmed(t *testing.T) {
	opts := options
	opts.AllowRun = true

	if allowed := after(Args(opts), "--allowedTools"); !strings.Contains(allowed, runFlowTool) {
		t.Fatalf("run_flow missing: %s", allowed)
	}
}

func TestArgsKeepAMessageFromBecomingAFlag(t *testing.T) {
	opts := options
	opts.Prompt = "--dangerously-skip-permissions"

	args := Args(opts)
	if got := args[len(args)-2:]; got[0] != "--" || got[1] != opts.Prompt {
		t.Fatalf("args end with %q", got)
	}
}

func TestArgsKeepTheTokenOut(t *testing.T) {
	args := Args(options)

	if strings.Contains(strings.Join(args, " "), options.Token) {
		t.Fatal("the token is readable by any process on the machine")
	}
	if config := after(args, "--mcp-config"); !strings.Contains(config, "${HEDDLE_MCP_TOKEN}") {
		t.Fatalf("config = %s", config)
	}
}

func TestArgsStartOrResumeTheSession(t *testing.T) {
	if got := after(Args(options), "--session-id"); got != options.SessionID {
		t.Fatalf("--session-id = %q", got)
	}

	opts := options
	opts.Resume = true
	args := Args(opts)
	if got := after(args, "--resume"); got != options.SessionID {
		t.Fatalf("--resume = %q", got)
	}
	if slices.Contains(args, "--session-id") {
		t.Fatal("a resumed session must not ask for a new id")
	}
}

func TestEnvLeavesEverythingElseBehind(t *testing.T) {
	env := Env(options, []string{
		"PATH=/usr/bin",
		"HOME=/home/me",
		"AWS_SECRET_ACCESS_KEY=secret",
		"GITHUB_TOKEN=secret",
		"ANTHROPIC_API_KEY=stale",
	})

	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "secret") || strings.Contains(joined, "stale") {
		t.Fatalf("leaked: %s", joined)
	}
	for _, want := range []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=sk-ant", "HEDDLE_MCP_TOKEN=heddle-token"} {
		if !slices.Contains(env, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
}

func TestProblemNamesWhatIsMissing(t *testing.T) {
	t.Setenv("TREADLE_CLAUDE_BIN", "/no/such/claude")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	if problem := Problem(); !strings.Contains(problem, "TREADLE_CLAUDE_BIN") {
		t.Fatalf("Problem() = %q", problem)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TREADLE_CLAUDE_BIN", self)
	t.Setenv("ANTHROPIC_API_KEY", "")
	if problem := Problem(); !strings.Contains(problem, "ANTHROPIC_API_KEY") {
		t.Fatalf("Problem() = %q", problem)
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	if problem := Problem(); problem != "" {
		t.Fatalf("Problem() = %q", problem)
	}
}

func TestNewSessionIDIsAUUID(t *testing.T) {
	id := NewSessionID()

	if len(id) != 36 || id[14] != '4' || strings.Count(id, "-") != 4 {
		t.Fatalf("id = %q", id)
	}
	if id == NewSessionID() {
		t.Fatal("ids repeat")
	}
}

// Recorded from `claude -p --output-format stream-json`.
func TestParseReadsText(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}}`

	events := Parse([]byte(line))
	if len(events) != 1 || events[0].Type != EventText || events[0].Text != "pong" {
		t.Fatalf("events = %+v", events)
	}
}

func TestParseMarksANewTurn(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"message_start"}}`

	events := Parse([]byte(line))
	if len(events) != 1 || events[0].Type != EventTurn {
		t.Fatalf("events = %+v", events)
	}
}

func TestParseReadsADeniedRun(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,"result":"no","permission_denials":[{"tool_name":"mcp__heddle__run_flow","tool_use_id":"x","tool_input":{"flow_id":"7"}}]}`

	events := Parse([]byte(line))
	if len(events) != 1 || events[0].Type != EventRunDenied || events[0].FlowID != "7" {
		t.Fatalf("events = %+v", events)
	}
}

func TestParseIgnoresOtherDenials(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,"permission_denials":[{"tool_name":"mcp__heddle__get_flow"}]}`

	if events := Parse([]byte(line)); len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
}

func TestParseExplainsTheSpendingLimit(t *testing.T) {
	line := `{"type":"result","subtype":"error_max_budget_usd","is_error":true}`

	events := Parse([]byte(line))
	if len(events) != 1 || events[0].Text != "This message reached the spending limit." {
		t.Fatalf("events = %+v", events)
	}
}

func TestParseDropsEverythingElse(t *testing.T) {
	for _, line := range []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"rate_limit_event"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"pong"}`,
		`a warning`,
	} {
		if events := Parse([]byte(line)); len(events) != 0 {
			t.Fatalf("%s gave %+v", line, events)
		}
	}
}

func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}

	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func collect(t *testing.T, events <-chan Event) []Event {
	t.Helper()

	var all []Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return all
			}
			all = append(all, event)
		case <-timeout:
			t.Fatal("the channel never closed")
		}
	}
}

func TestStartStreamsWhatTheProgramPrints(t *testing.T) {
	opts := options
	opts.Binary = fakeClaude(t, `echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}}'
echo 'noise'
`)

	events, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	got := collect(t, events)
	if len(got) != 1 || got[0].Text != "hi" {
		t.Fatalf("events = %+v", got)
	}
}

func TestStartReportsAFailure(t *testing.T) {
	opts := options
	opts.Binary = fakeClaude(t, "echo 'invalid api key' >&2\nexit 3\n")

	events, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	got := collect(t, events)
	if len(got) != 1 || got[0].Type != EventError || !strings.Contains(got[0].Text, "invalid api key") {
		t.Fatalf("events = %+v", got)
	}
}

func TestStartDoesntWaitOnStdin(t *testing.T) {
	opts := options
	opts.Binary = fakeClaude(t, "cat\n")

	events, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	if got := collect(t, events); len(got) != 0 {
		t.Fatalf("events = %+v", got)
	}
}

func TestCancellingKillsTheProgram(t *testing.T) {
	opts := options
	opts.Binary = fakeClaude(t, "exec sleep 60\n")

	ctx, cancel := context.WithCancel(context.Background())
	events, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	if got := collect(t, events); len(got) != 0 {
		t.Fatalf("a cancelled run isn't a failure: %+v", got)
	}
}
