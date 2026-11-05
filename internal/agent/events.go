package agent

import "encoding/json"

// EventType says what happened in a message.
type EventType int

const (
	// EventText carries a piece of the reply.
	EventText EventType = iota
	// EventError ends the message with a failure.
	EventError
	// EventRunDenied means the agent asked to run a flow and needs a person to allow it.
	EventRunDenied
	// EventTurn starts another turn of the agent, usually after a tool, so whatever it
	// says next is a new paragraph.
	EventTurn
)

// Event is something the agent did.
type Event struct {
	Type   EventType
	Text   string
	FlowID string
}

type streamLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Event   struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	PermissionDenials []struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			FlowID json.RawMessage `json:"flow_id"`
		} `json:"tool_input"`
	} `json:"permission_denials"`
}

// Parse reads one line of `claude -p --output-format stream-json`. Anything that
// isn't an event it knows produces nothing.
func Parse(line []byte) []Event {
	var parsed streamLine
	if err := json.Unmarshal(line, &parsed); err != nil {
		return nil
	}

	switch parsed.Type {
	case "stream_event":
		if parsed.Event.Type == "message_start" {
			return []Event{{Type: EventTurn}}
		}
		if parsed.Event.Type == "content_block_delta" && parsed.Event.Delta.Type == "text_delta" {
			return []Event{{Type: EventText, Text: parsed.Event.Delta.Text}}
		}
	case "result":
		return parseResult(parsed)
	}
	return nil
}

func parseResult(result streamLine) []Event {
	var events []Event
	for _, denial := range result.PermissionDenials {
		if denial.ToolName == runFlowTool {
			events = append(events, Event{Type: EventRunDenied, FlowID: flowID(denial.ToolInput.FlowID)})
			break
		}
	}
	if result.IsError {
		events = append(events, Event{Type: EventError, Text: errorMessage(result)})
	}
	return events
}

func flowID(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

func errorMessage(result streamLine) string {
	switch {
	case result.Subtype == "error_max_budget_usd":
		return "This message reached the spending limit."
	case result.Result != "":
		return result.Result
	case result.Subtype != "":
		return "The agent stopped: " + result.Subtype + "."
	default:
		return "The agent stopped."
	}
}
