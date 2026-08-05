package agenthook_test

import (
	"strings"
	"testing"
	"time"

	"github.com/headwayio/fulcrum-cli/internal/agenthook"
)

// assistant builds a transcript record. Several records sharing an id is the
// normal shape, not an edge case: the harness writes one per content block.
func assistant(stamp, id string, output int64) string {
	return `{"type":"assistant","timestamp":"` + stamp + `","message":{"id":"` + id +
		`","model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":` +
		itoa(output) + `,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}}}`
}

func prompt(stamp, text string) string {
	return `{"type":"user","timestamp":"` + stamp + `","message":{"content":"` + text + `"}}`
}

func toolResult(stamp string) string {
	return `{"type":"user","timestamp":"` + stamp +
		`","message":{"content":[{"type":"tool_result","content":"ok"}]}}`
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

func parse(t *testing.T, lines ...string) []agenthook.Turn {
	t.Helper()
	turns, err := agenthook.ParseClaudeTranscript(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return turns
}

// THE trap. One model response is written as several records, each repeating
// the same usage object; summing per record overstated a real session's output
// tokens by 2.55x.
func TestUsageIsCountedOncePerResponse(t *testing.T) {
	turns := parse(t,
		prompt("2026-08-05T10:00:00.000Z", "go"),
		assistant("2026-08-05T10:00:05.000Z", "msg_1", 100),
		assistant("2026-08-05T10:00:06.000Z", "msg_1", 100),
		assistant("2026-08-05T10:00:07.000Z", "msg_1", 100),
	)

	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %d", len(turns))
	}
	if got := turns[0].Usage.OutputTokens; got != 100 {
		t.Errorf("output tokens counted per record, not per response: got %d, want 100", got)
	}
	if got := turns[0].Calls; got != 1 {
		t.Errorf("three records are one model call: got %d", got)
	}
}

// A response that straddles a turn boundary must not be paid for twice, which
// is why the id set is global rather than per-turn.
func TestUsageIsNotCountedAgainInALaterTurn(t *testing.T) {
	turns := parse(t,
		prompt("2026-08-05T10:00:00.000Z", "go"),
		assistant("2026-08-05T10:00:05.000Z", "msg_1", 100),
		prompt("2026-08-05T10:00:06.000Z", "again"),
		assistant("2026-08-05T10:00:07.000Z", "msg_1", 100),
		assistant("2026-08-05T10:00:08.000Z", "msg_2", 40),
	)

	total := int64(0)
	for _, turn := range turns {
		total += turn.Usage.OutputTokens
	}
	if total != 140 {
		t.Errorf("a response spanning a boundary was counted twice: got %d, want 140", total)
	}
}

// Tool results are the agent's own work coming back, so they must not break a
// turn — otherwise every tool call ends one, and all the tool-execution time
// lands in the gaps between turns where it reads as human-away time.
func TestToolResultsDoNotStartANewTurn(t *testing.T) {
	turns := parse(t,
		prompt("2026-08-05T10:00:00.000Z", "go"),
		assistant("2026-08-05T10:00:05.000Z", "msg_1", 10),
		toolResult("2026-08-05T10:00:20.000Z"),
		assistant("2026-08-05T10:00:30.000Z", "msg_2", 10),
		toolResult("2026-08-05T10:00:40.000Z"),
		assistant("2026-08-05T10:00:50.000Z", "msg_3", 10),
	)

	if len(turns) != 1 {
		t.Fatalf("expected one turn covering the whole exchange, got %d", len(turns))
	}
	if got := turns[0].Duration(); got != 50*time.Second {
		t.Errorf("tool time was dropped from the turn: got %s, want 50s", got)
	}
	if got := turns[0].Calls; got != 3 {
		t.Errorf("expected 3 model calls in the turn, got %d", got)
	}
}

func TestANewPromptStartsANewTurn(t *testing.T) {
	turns := parse(t,
		prompt("2026-08-05T10:00:00.000Z", "first"),
		assistant("2026-08-05T10:00:05.000Z", "msg_1", 10),
		prompt("2026-08-05T10:05:00.000Z", "second"),
		assistant("2026-08-05T10:05:05.000Z", "msg_2", 10),
	)

	if len(turns) != 2 {
		t.Fatalf("expected two turns, got %d", len(turns))
	}
	if turns[0].Index != 1 || turns[1].Index != 2 {
		t.Errorf("turn indexes are not 1-based and sequential: %d, %d", turns[0].Index, turns[1].Index)
	}
	// The five idle minutes between the turns belong to neither actor.
	if got := turns[0].Duration() + turns[1].Duration(); got != 10*time.Second {
		t.Errorf("human-away time leaked into active time: got %s, want 10s", got)
	}
}

// The turn starts when the agent was handed its input, not when it produced
// its first block — otherwise the model's own latency is not counted as work.
func TestTurnStartsAtTheInputThatWokeTheAgent(t *testing.T) {
	turns := parse(t,
		prompt("2026-08-05T10:00:00.000Z", "go"),
		assistant("2026-08-05T10:00:12.000Z", "msg_1", 10),
	)

	if got := turns[0].Duration(); got != 12*time.Second {
		t.Errorf("model latency was excluded: got %s, want 12s", got)
	}
}

func TestMalformedLinesAreSkippedRatherThanFatal(t *testing.T) {
	turns := parse(t,
		prompt("2026-08-05T10:00:00.000Z", "go"),
		"{not json at all",
		"",
		`{"type":"assistant"}`, // no timestamp, no message
		assistant("2026-08-05T10:00:05.000Z", "msg_1", 10),
	)

	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %d", len(turns))
	}
	if turns[0].Usage.OutputTokens != 10 {
		t.Errorf("usage lost to a malformed neighbour: %d", turns[0].Usage.OutputTokens)
	}
}

func TestATranscriptWithNoAssistantWorkHasNoTurns(t *testing.T) {
	turns := parse(t, prompt("2026-08-05T10:00:00.000Z", "hello"))
	if len(turns) != 0 {
		t.Errorf("expected no turns, got %d", len(turns))
	}
}

func TestCacheTokensAreKeptSeparate(t *testing.T) {
	turns := parse(t,
		prompt("2026-08-05T10:00:00.000Z", "go"),
		assistant("2026-08-05T10:00:05.000Z", "msg_1", 10),
	)

	usage := turns[0].Usage
	if usage.CacheCreationTokens != 5 || usage.CacheReadTokens != 7 {
		t.Errorf("cache tokens folded away: creation=%d read=%d", usage.CacheCreationTokens, usage.CacheReadTokens)
	}
	if usage.InputTokens != 10 {
		t.Errorf("input tokens absorbed the cache counts: %d", usage.InputTokens)
	}
	if usage.Model != "claude-opus-5" {
		t.Errorf("model not carried: %q", usage.Model)
	}
}

// A line longer than bufio.Scanner's default 64KB token is ordinary in a
// transcript that embedded a large tool result, and a scanner-based reader
// would silently truncate the session at that point.
func TestAVeryLongRecordIsRead(t *testing.T) {
	huge := strings.Repeat("x", 200_000)
	turns := parse(t,
		`{"type":"user","timestamp":"2026-08-05T10:00:00.000Z","message":{"content":[{"type":"tool_result","content":"`+huge+`"}]}}`,
		assistant("2026-08-05T10:00:05.000Z", "msg_1", 10),
	)

	if len(turns) != 1 || turns[0].Usage.OutputTokens != 10 {
		t.Fatalf("a long line broke the parse: %d turns", len(turns))
	}
}
