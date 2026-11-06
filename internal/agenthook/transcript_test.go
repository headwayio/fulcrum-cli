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
	turns, err := agenthook.ParseTranscript(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return turns
}

// OMP writes one record per model response, keyed by the record id, with the
// speaker in message.role and usage under its own names.
func ompAssistant(stamp, id string, output int64) string {
	return `{"type":"message","id":"` + id + `","timestamp":"` + stamp +
		`","message":{"role":"assistant","model":"claude-opus-5","usage":{"input":10,"output":` +
		itoa(output) + `,"cacheRead":7,"cacheWrite":5,"totalTokens":0}}}`
}

func ompPrompt(stamp, id string) string {
	return `{"type":"message","id":"` + id + `","timestamp":"` + stamp +
		`","message":{"role":"user","content":[{"type":"text","text":"go"}]}}`
}

func ompToolResult(stamp, id string) string {
	return `{"type":"message","id":"` + id + `","timestamp":"` + stamp +
		`","message":{"role":"toolResult","toolName":"bash","content":[{"type":"text","text":"ok"}]}}`
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

// OMP's transcript carries the speaker in message.role rather than the record
// type, gives tool output its own role, and surrounds the exchange with
// session metadata. None of that may change the turn arithmetic.
func TestOMPTranscriptIsReadByRole(t *testing.T) {
	turns := parse(t,
		`{"type":"session","version":3,"id":"01a1","timestamp":"2026-10-09T15:45:12.096Z","cwd":"/repo"}`,
		`{"type":"model_change","id":"aa","timestamp":"2026-10-09T15:45:12.100Z"}`,
		ompPrompt("2026-10-09T15:45:13.000Z", "p1"),
		ompAssistant("2026-10-09T15:45:16.000Z", "r1", 100),
		ompToolResult("2026-10-09T15:45:20.000Z", "t1"),
		`{"type":"custom","id":"c1","timestamp":"2026-10-09T15:45:21.000Z","customType":"x"}`,
		ompAssistant("2026-10-09T15:45:30.000Z", "r2", 40),
		ompPrompt("2026-10-09T15:50:00.000Z", "p2"),
		ompAssistant("2026-10-09T15:50:05.000Z", "r3", 1),
	)

	if len(turns) != 2 {
		t.Fatalf("expected two turns (tool output and metadata do not split one), got %d", len(turns))
	}
	first := turns[0]
	if first.Calls != 2 || first.Usage.OutputTokens != 140 {
		t.Errorf("first turn should sum both responses: calls=%d output=%d", first.Calls, first.Usage.OutputTokens)
	}
	if got := first.Duration(); got != 17*time.Second {
		t.Errorf("turn should run from the prompt to the last response: got %s, want 17s", got)
	}
	if first.Usage.InputTokens != 20 || first.Usage.CacheReadTokens != 14 || first.Usage.CacheCreationTokens != 10 {
		t.Errorf("OMP usage names not mapped: input=%d cacheRead=%d cacheWrite=%d",
			first.Usage.InputTokens, first.Usage.CacheReadTokens, first.Usage.CacheCreationTokens)
	}
	if first.Usage.Model != "claude-opus-5" {
		t.Errorf("model not carried from an OMP record: %q", first.Usage.Model)
	}
	if turns[1].Index != 2 || turns[1].Usage.OutputTokens != 1 {
		t.Errorf("second turn wrong: index=%d output=%d", turns[1].Index, turns[1].Usage.OutputTokens)
	}
}

// An OMP record that is re-read (a hook firing twice over an append-only file)
// is the same response, so its id must dedupe it exactly as Claude Code's
// message id does.
func TestOMPResponsesAreCountedOncePerRecordID(t *testing.T) {
	turns := parse(t,
		ompPrompt("2026-10-09T15:45:13.000Z", "p1"),
		ompAssistant("2026-10-09T15:45:16.000Z", "r1", 100),
		ompAssistant("2026-10-09T15:45:16.000Z", "r1", 100),
	)
	if len(turns) != 1 || turns[0].Usage.OutputTokens != 100 || turns[0].Calls != 1 {
		t.Fatalf("duplicate OMP record was counted twice: %+v", turns)
	}
}

// OMP wakes an idle agent with a custom_message — an IRC message, a
// background job's result — rather than a user record. The round it starts is
// new work and must be a new turn: folded into the previous one, it lands on
// an index already posted, which the server ignores. The same messages
// arriving mid-run are steering and stay in the turn. The records are real
// ones from OMP subagent transcripts, trimmed to the fields the parser reads.
func TestOMPInjectedMessagesStartATurnOnlyAfterTheAgentHandedBack(t *testing.T) {
	type want struct {
		start, end string
		calls      int
		output     int64
	}
	for _, tc := range []struct {
		name  string
		lines []string
		want  []want
	}{
		{
			name: "a background job's result wakes the agent after it handed back",
			lines: []string{
				`{"type":"message","id":"d1a437dd","timestamp":"2026-11-06T04:46:47.114Z","message":{"role":"user"}}`,
				`{"type":"message","id":"98843628","timestamp":"2026-11-06T04:46:52.062Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":4,"output":483,"cacheRead":409305,"cacheWrite":886}}}`,
				`{"type":"custom","id":"5f4cab3d","timestamp":"2026-11-06T04:46:52.063Z","customType":"tool_execution_start"}`,
				`{"type":"message","id":"d92581dd","timestamp":"2026-11-06T04:47:13.254Z","message":{"role":"toolResult"}}`,
				`{"type":"custom_message","id":"e44f5f05","timestamp":"2026-11-06T04:47:13.254Z","customType":"async-result","display":true}`,
				`{"type":"message","id":"c2d67f3e","timestamp":"2026-11-06T04:47:18.150Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":2,"output":435,"cacheRead":410191,"cacheWrite":1150}}}`,
				`{"type":"message","id":"b56dcee9","timestamp":"2026-11-06T04:47:19.208Z","message":{"role":"toolResult"}}`,
				`{"type":"message","id":"f6bec625","timestamp":"2026-11-06T04:47:49.216Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":2,"output":2226,"cacheRead":411832,"cacheWrite":943}}}`,
				`{"type":"custom_message","id":"bf7a05db","timestamp":"2026-11-06T05:02:13.959Z","customType":"async-result","display":true}`,
				`{"type":"message","id":"21b2ed72","timestamp":"2026-11-06T05:02:17.061Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":4,"output":199,"cacheRead":412775,"cacheWrite":2425}}}`,
				`{"type":"message","id":"040013f0","timestamp":"2026-11-06T05:02:17.125Z","message":{"role":"toolResult"}}`,
				`{"type":"message","id":"a0274959","timestamp":"2026-11-06T05:02:19.167Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":2,"output":159,"cacheRead":415200,"cacheWrite":227}}}`,
			},
			want: []want{
				{"2026-11-06T04:46:47.114Z", "2026-11-06T04:47:49.216Z", 3, 483 + 435 + 2226},
				{"2026-11-06T05:02:13.959Z", "2026-11-06T05:02:19.167Z", 2, 199 + 159},
			},
		},
		{
			// Sent at 04:55 while the agent slept, delivered after the next
			// round handed back at 05:02:19. The turn starts on delivery, not
			// seven minutes inside the turn before it.
			name: "an IRC message starts its turn when it was delivered, not when it was sent",
			lines: []string{
				`{"type":"custom_message","id":"bf7a05db","timestamp":"2026-11-06T05:02:13.959Z","customType":"async-result","display":true}`,
				`{"type":"message","id":"21b2ed72","timestamp":"2026-11-06T05:02:17.061Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":4,"output":199,"cacheRead":412775,"cacheWrite":2425}}}`,
				`{"type":"message","id":"040013f0","timestamp":"2026-11-06T05:02:17.125Z","message":{"role":"toolResult"}}`,
				`{"type":"message","id":"a0274959","timestamp":"2026-11-06T05:02:19.167Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":2,"output":159,"cacheRead":415200,"cacheWrite":227}}}`,
				`{"type":"custom_message","id":"25d65251","timestamp":"2026-11-06T04:55:13.273Z","customType":"irc:incoming","display":true}`,
				`{"type":"message","id":"060a9f4d","timestamp":"2026-11-06T05:02:22.333Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":4,"output":367,"cacheRead":415427,"cacheWrite":584}}}`,
				`{"type":"message","id":"4d0289c7","timestamp":"2026-11-06T05:02:25.283Z","message":{"role":"toolResult"}}`,
				`{"type":"message","id":"329da953","timestamp":"2026-11-06T05:02:27.514Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":2,"output":224,"cacheRead":416011,"cacheWrite":488}}}`,
				`{"type":"message","id":"432a52ee","timestamp":"2026-11-06T05:02:27.516Z","message":{"role":"toolResult"}}`,
				`{"type":"message","id":"7f0a89eb","timestamp":"2026-11-06T05:02:30.698Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":2,"output":258,"cacheRead":416499,"cacheWrite":237}}}`,
			},
			want: []want{
				{"2026-11-06T05:02:13.959Z", "2026-11-06T05:02:19.167Z", 2, 199 + 159},
				{"2026-11-06T05:02:19.167Z", "2026-11-06T05:02:30.698Z", 3, 367 + 224 + 258},
			},
		},
		{
			name: "IRC and job results that arrive while tools are pending stay in the turn",
			lines: []string{
				`{"type":"message","id":"c6b1eeff","timestamp":"2026-11-06T04:39:25.326Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":2,"output":4090,"cacheRead":333223,"cacheWrite":3640}}}`,
				`{"type":"custom","id":"c24d7fd7","timestamp":"2026-11-06T04:39:25.326Z","customType":"tool_execution_start"}`,
				`{"type":"message","id":"15ab13a0","timestamp":"2026-11-06T04:39:25.328Z","message":{"role":"toolResult"}}`,
				`{"type":"custom_message","id":"c1899634","timestamp":"2026-11-06T04:38:59.403Z","customType":"irc:incoming","display":true}`,
				`{"type":"message","id":"c8368778","timestamp":"2026-11-06T04:39:31.103Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":2,"output":633,"cacheRead":336863,"cacheWrite":4765}}}`,
				`{"type":"message","id":"2c6e36c1","timestamp":"2026-11-06T04:39:31.252Z","message":{"role":"toolResult"}}`,
				`{"type":"custom_message","id":"7f612934","timestamp":"2026-11-06T04:39:31.253Z","customType":"async-result","display":true}`,
				`{"type":"message","id":"fb533109","timestamp":"2026-11-06T04:39:34.155Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":2,"output":227,"cacheRead":341628,"cacheWrite":832}}}`,
			},
			want: []want{
				{"2026-11-06T04:39:25.326Z", "2026-11-06T04:39:34.155Z", 3, 4090 + 633 + 227},
			},
		},
		{
			name: "IRC wakes a session that exited",
			lines: []string{
				`{"type":"message","id":"2110ad28","timestamp":"2026-11-06T05:23:43.010Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":2,"output":1440,"cacheRead":465825,"cacheWrite":998}}}`,
				`{"type":"custom","id":"54dbcd51","timestamp":"2026-11-06T05:30:43.015Z","customType":"session_exit"}`,
				`{"type":"custom_message","id":"fe8ad783","timestamp":"2026-11-06T05:51:31.929Z","customType":"eval-state-context","display":false}`,
				`{"type":"custom_message","id":"7e0a0ee1","timestamp":"2026-11-06T05:51:31.898Z","customType":"irc:incoming","display":true}`,
				`{"type":"message","id":"57ec6c24","timestamp":"2026-11-06T05:51:57.829Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":2,"output":2785,"cacheRead":466823,"cacheWrite":2803}}}`,
				`{"type":"message","id":"a3e90397","timestamp":"2026-11-06T05:51:57.831Z","message":{"role":"toolResult"}}`,
				`{"type":"message","id":"354621c6","timestamp":"2026-11-06T05:52:01.699Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":2,"output":377,"cacheRead":469626,"cacheWrite":2890}}}`,
				`{"type":"custom","id":"6dc8cef4","timestamp":"2026-11-06T05:59:01.703Z","customType":"session_exit"}`,
			},
			want: []want{
				{"2026-11-06T05:23:43.010Z", "2026-11-06T05:23:43.010Z", 1, 1440},
				{"2026-11-06T05:51:31.929Z", "2026-11-06T05:52:01.699Z", 2, 2785 + 377},
			},
		},
		{
			// The session exited with a tool still running. On waking, OMP
			// first resolves that call as an aborted, empty response; it is
			// its own instant, and neither it nor the new round stretches the
			// old turn across the 25 idle minutes.
			name: "a tool left pending at exit does not stretch the turn across the idle time",
			lines: []string{
				`{"type":"message","id":"9e441608","timestamp":"2026-11-01T01:10:24.131Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":2,"output":55,"cacheRead":831182,"cacheWrite":834}}}`,
				`{"type":"custom","id":"72422e2f","timestamp":"2026-11-01T01:10:24.132Z","customType":"tool_execution_start"}`,
				`{"type":"custom","id":"cc8aa7ae","timestamp":"2026-11-01T01:33:50.580Z","customType":"session_exit"}`,
				`{"type":"message","id":"9a146eb4","timestamp":"2026-11-01T01:35:28.154Z","message":{"role":"toolResult"}}`,
				`{"type":"message","id":"bdce3c8c","timestamp":"2026-11-01T01:35:28.155Z","message":{"role":"assistant","stopReason":"aborted","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}}`,
				`{"type":"custom_message","id":"f65a7494","timestamp":"2026-11-01T01:35:28.213Z","customType":"eval-state-context","display":false}`,
				`{"type":"custom_message","id":"8902cfb9","timestamp":"2026-11-01T01:35:28.005Z","customType":"irc:incoming","display":true}`,
				`{"type":"message","id":"b1b4c8f2","timestamp":"2026-11-01T01:36:12.965Z","message":{"role":"assistant","stopReason":"toolUse","usage":{"input":10610,"output":156,"cacheRead":0,"cacheWrite":811753}}}`,
				`{"type":"custom","id":"7b6ebff6","timestamp":"2026-11-01T01:36:12.966Z","customType":"tool_execution_start"}`,
			},
			want: []want{
				{"2026-11-01T01:10:24.131Z", "2026-11-01T01:10:24.131Z", 1, 55},
				{"2026-11-01T01:35:28.154Z", "2026-11-01T01:35:28.155Z", 1, 0},
				{"2026-11-01T01:35:28.213Z", "2026-11-01T01:36:12.965Z", 1, 156},
			},
		},
		{
			// Re-read after a revival (a hook firing over an append-only
			// file), a response is still the same response.
			name: "a response is counted once on either side of a revival",
			lines: []string{
				`{"type":"message","id":"f6bec625","timestamp":"2026-11-06T04:47:49.216Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":2,"output":2226,"cacheRead":411832,"cacheWrite":943}}}`,
				`{"type":"custom_message","id":"bf7a05db","timestamp":"2026-11-06T05:02:13.959Z","customType":"async-result","display":true}`,
				`{"type":"message","id":"f6bec625","timestamp":"2026-11-06T04:47:49.216Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":2,"output":2226,"cacheRead":411832,"cacheWrite":943}}}`,
				`{"type":"message","id":"21b2ed72","timestamp":"2026-11-06T05:02:17.061Z","message":{"role":"assistant","stopReason":"stop","usage":{"input":4,"output":199,"cacheRead":412775,"cacheWrite":2425}}}`,
			},
			want: []want{
				{"2026-11-06T04:47:49.216Z", "2026-11-06T04:47:49.216Z", 1, 2226},
				{"2026-11-06T05:02:13.959Z", "2026-11-06T05:02:17.061Z", 1, 199},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turns := parse(t, tc.lines...)
			if len(turns) != len(tc.want) {
				t.Fatalf("got %d turns, want %d: %+v", len(turns), len(tc.want), turns)
			}
			for i, w := range tc.want {
				turn := turns[i]
				start, _ := time.Parse(time.RFC3339Nano, w.start)
				end, _ := time.Parse(time.RFC3339Nano, w.end)
				if turn.Index != i+1 || !turn.StartedAt.Equal(start) || !turn.EndedAt.Equal(end) {
					t.Errorf("turn %d: #%d %s → %s, want #%d %s → %s", i+1, turn.Index,
						turn.StartedAt.Format(time.RFC3339Nano), turn.EndedAt.Format(time.RFC3339Nano), i+1, w.start, w.end)
				}
				if turn.Calls != w.calls || turn.Usage.OutputTokens != w.output {
					t.Errorf("turn %d: %d calls, %d output tokens; want %d, %d", i+1, turn.Calls, turn.Usage.OutputTokens, w.calls, w.output)
				}
			}
		})
	}
}

// Claude Code reports why a response ended as stop_reason; a turn there still
// ends only on a prompt, since it has no injected messages to tell apart.
func TestClaudeCodeTurnsAreUnchangedByStopReasons(t *testing.T) {
	turns := parse(t,
		prompt("2026-08-05T10:00:00.000Z", "go"),
		`{"type":"assistant","timestamp":"2026-08-05T10:00:05.000Z","message":{"id":"msg_1","stop_reason":"end_turn","usage":{"output_tokens":10}}}`,
		`{"type":"system","timestamp":"2026-08-05T10:03:00.000Z"}`,
		`{"type":"assistant","timestamp":"2026-08-05T10:03:05.000Z","message":{"id":"msg_2","stop_reason":"end_turn","usage":{"output_tokens":10}}}`,
	)
	if len(turns) != 1 || turns[0].Calls != 2 {
		t.Fatalf("only a prompt starts a Claude Code turn: %+v", turns)
	}
}
