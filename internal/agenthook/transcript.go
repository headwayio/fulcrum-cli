// Package agenthook turns a coding harness's own transcript into the turns
// Fulcrum records as agent work.
//
// This exists because THE MODEL CANNOT REPORT ITS OWN USAGE. No harness
// exposes token counts to the model it is running, so an MCP tool could never
// carry them — the only component that knows what a turn cost is the harness,
// and the only way to ask it is to read the transcript it writes. That is why
// telemetry is a hook and not a tool.
package agenthook

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// Usage is one turn's token cost. Cache reads are counted separately rather
// than folded in: they are cheap but not free, and a comparison that drops
// them understates the long conversations agents actually have.
type Usage struct {
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	Model               string
}

// Turn is one span of agent work: a prompt arriving, and everything the agent
// did before it stopped and handed control back.
type Turn struct {
	// Index is 1-based and STABLE. The transcript is append-only, so the Nth
	// turn is always the Nth — which is what lets the server dedupe on
	// (session_ref, turn_index) and a retried hook cost nothing.
	Index     int
	StartedAt time.Time
	EndedAt   time.Time
	Usage     Usage
	// Calls is how many model requests the turn took. Kept for the operator
	// reading `fulcrum hook stop --dry-run`; the server does not store it.
	Calls int
}

// Duration is the turn's wall clock: thinking and tool use together, which is
// the grain Fulcrum records. Separating them would be guessing.
func (t Turn) Duration() time.Duration { return t.EndedAt.Sub(t.StartedAt) }

type transcriptRecord struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Message   *transcriptMsg `json:"message"`
}

type transcriptMsg struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Usage   *transcriptUse  `json:"usage"`
	Content json.RawMessage `json:"content"`
}

type transcriptUse struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// ParseClaudeTranscript reads a Claude Code transcript (JSONL) into turns.
//
// TWO CORRECTNESS TRAPS LIVE HERE, and both silently produce plausible
// numbers rather than errors:
//
//  1. ONE MODEL RESPONSE IS WRITTEN AS SEVERAL RECORDS — one per content
//     block — and every one of them repeats the SAME usage object. Summing
//     per record inflates the headline number; measured on a real 471-call
//     session it overstated output tokens by 2.55x. Usage is therefore
//     counted once per message id, and the id set is global rather than
//     per-turn, because a response can straddle a turn boundary.
//
//  2. A TURN IS NOT A MODEL REQUEST. A single prompt produces many requests
//     as the agent calls tools and reads the results. Recording each request
//     as a turn would push all tool-execution time into the gaps BETWEEN
//     turns, where it reads as human-away time and vanishes from active time.
//     So a turn runs from the input that woke the agent to the last thing it
//     said before stopping, and only a non-tool-result message starts a new
//     one.
//
// Malformed lines are skipped rather than failing the parse: a transcript is
// something the harness owns and may extend, and a hook that dies on an
// unfamiliar record would take the whole session's telemetry with it.
func ParseClaudeTranscript(r io.Reader) ([]Turn, error) {
	reader := bufio.NewReader(r)

	var (
		turns   []Turn
		current *Turn
		// counted is global: see trap 1. A message id that appeared in an
		// earlier turn must not be paid for twice.
		counted  = map[string]bool{}
		previous time.Time
		havePrev bool
	)

	closeTurn := func() {
		if current != nil {
			current.Index = len(turns) + 1
			turns = append(turns, *current)
			current = nil
		}
	}

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			record, ok := decodeRecord(line)
			if ok {
				stamp, haveStamp := parseStamp(record.Timestamp)

				switch {
				case record.Type == "assistant":
					if current == nil {
						// The turn starts when the agent was handed its input,
						// not when it produced its first block — otherwise the
						// model's own latency is excluded from the work.
						start := stamp
						if havePrev {
							start = previous
						}
						current = &Turn{StartedAt: start, EndedAt: stamp}
					}
					if haveStamp && stamp.After(current.EndedAt) {
						current.EndedAt = stamp
					}
					if record.Message != nil && !counted[record.Message.ID] {
						counted[record.Message.ID] = true
						current.Calls++
						addUsage(&current.Usage, record.Message)
					}

				case record.Type == "user" && !isToolResult(record.Message):
					// New input, so whatever the agent was doing is over. A
					// tool result is NOT new input — it is the agent's own
					// work coming back to it.
					closeTurn()
				}

				if haveStamp {
					previous, havePrev = stamp, true
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
	}
	closeTurn()

	return turns, nil
}

func decodeRecord(line []byte) (transcriptRecord, bool) {
	var record transcriptRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return transcriptRecord{}, false
	}
	return record, true
}

func parseStamp(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	stamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return stamp.UTC(), true
}

func addUsage(into *Usage, message *transcriptMsg) {
	if message.Model != "" {
		into.Model = message.Model
	}
	if message.Usage == nil {
		return
	}
	into.InputTokens += message.Usage.InputTokens
	into.OutputTokens += message.Usage.OutputTokens
	into.CacheCreationTokens += message.Usage.CacheCreationInputTokens
	into.CacheReadTokens += message.Usage.CacheReadInputTokens
}

// isToolResult reports whether a user record is the agent's own tool output
// coming back, rather than someone giving it something new to do.
//
// Content is a string for typed input and an array of blocks for everything
// else, so the shape is checked before it is decoded.
func isToolResult(message *transcriptMsg) bool {
	if message == nil || len(message.Content) == 0 || message.Content[0] != '[' {
		return false
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(message.Content, &blocks); err != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "tool_result" {
			return true
		}
	}
	return false
}
