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

// transcriptRecord is one JSONL line from either harness. Claude Code puts the
// speaker in Type ("user" / "assistant"); OMP writes Type "message" and puts
// the speaker in Message.Role ("user" / "assistant" / "toolResult"). The
// struct carries both so one decode serves both; classify decides what a
// record means.
type transcriptRecord struct {
	Type string `json:"type"`
	// ID is OMP's record id: one record per model response, so it is the
	// dedupe key there. Claude Code keys responses by Message.ID instead.
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	Message   *transcriptMsg `json:"message"`
}

type transcriptMsg struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Usage   *transcriptUse  `json:"usage"`
	Content json.RawMessage `json:"content"`
}

// transcriptUse carries both harnesses' spellings of the same four numbers.
// Only one set is ever populated per record, so summing both is safe.
type transcriptUse struct {
	// Claude Code
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	// OMP
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// recordKind is what one transcript line means to turn accounting.
type recordKind int

const (
	kindOther     recordKind = iota // session metadata, tool output, custom entries
	kindAssistant                   // the model said something; carries usage
	kindInput                       // someone handed the agent new work
)

// classify maps a record from either harness onto the two things the parser
// cares about. Claude Code reports tool output as a "user" record whose
// content holds tool_result blocks; OMP gives it its own role, so no content
// inspection is needed there.
func classify(record transcriptRecord) recordKind {
	switch record.Type {
	case "assistant":
		return kindAssistant
	case "user":
		if isToolResult(record.Message) {
			return kindOther
		}
		return kindInput
	case "message":
		if record.Message == nil {
			return kindOther
		}
		switch record.Message.Role {
		case "assistant":
			return kindAssistant
		case "user":
			return kindInput
		}
	}
	return kindOther
}

// responseKey identifies one model response so its usage is counted once:
// Claude Code repeats Message.ID across the records of a response; OMP writes
// one record per response with its own ID. An empty key means the record
// cannot be deduped and is counted as it stands.
func responseKey(record transcriptRecord) string {
	if record.Message != nil && record.Message.ID != "" {
		return "message:" + record.Message.ID
	}
	if record.ID != "" {
		return "record:" + record.ID
	}
	return ""
}

// ParseTranscript reads a harness transcript (JSONL) into turns. Two shapes
// are understood and may not be mixed in one file: Claude Code's, and OMP's
// (see transcriptRecord). Both are detected per record, so nothing has to be
// told which harness wrote the file.
//
// TWO CORRECTNESS TRAPS LIVE HERE, and both silently produce plausible
// numbers rather than errors:
//
//  1. ONE MODEL RESPONSE CAN BE WRITTEN AS SEVERAL RECORDS — Claude Code
//     writes one per content block — and every one of them repeats the SAME
//     usage object. Summing per record inflates the headline number; measured
//     on a real 471-call session it overstated output tokens by 2.55x. Usage
//     is therefore counted once per response key (see responseKey), and the
//     key set is global rather than per-turn, because a response can straddle
//     a turn boundary.
//
//  2. A TURN IS NOT A MODEL REQUEST. A single prompt produces many requests
//     as the agent calls tools and reads the results. Recording each request
//     as a turn would push all tool-execution time into the gaps BETWEEN
//     turns, where it reads as human-away time and vanishes from active time.
//     So a turn runs from the input that woke the agent to the last thing it
//     said before stopping, and only new input — never tool output — starts
//     a new one.
//
// Malformed lines are skipped rather than failing the parse: a transcript is
// something the harness owns and may extend, and a hook that dies on an
// unfamiliar record would take the whole session's telemetry with it.
func ParseTranscript(r io.Reader) ([]Turn, error) {
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

				switch classify(record) {
				case kindAssistant:
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
					key := responseKey(record)
					if record.Message != nil && (key == "" || !counted[key]) {
						if key != "" {
							counted[key] = true
						}
						current.Calls++
						addUsage(&current.Usage, record.Message)
					}

				case kindInput:
					// New input, so whatever the agent was doing is over. Tool
					// output is NOT new input — it is the agent's own work
					// coming back to it — and classify never reports it here.
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
	into.InputTokens += message.Usage.InputTokens + message.Usage.Input
	into.OutputTokens += message.Usage.OutputTokens + message.Usage.Output
	into.CacheCreationTokens += message.Usage.CacheCreationInputTokens + message.Usage.CacheWrite
	into.CacheReadTokens += message.Usage.CacheReadInputTokens + message.Usage.CacheRead
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
