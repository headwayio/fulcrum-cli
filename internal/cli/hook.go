package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/agenthook"
	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/projectctx"
)

// maxTurnsPerRequest mirrors the server's own cap. Batching here rather than
// discovering the limit as a 422 keeps a long session working. A variable
// only so tests can make a short transcript span several batches.
var maxTurnsPerRequest = 200

// hookPayload is the JSON a harness writes to the hook's stdin. Only the
// fields telemetry needs are decoded; the rest is ignored so a harness can
// extend the shape without breaking us.
//
// Claude Code's SubagentStop carries the PARENT's session_id and
// transcript_path, and names the subagent's own work in agent_id and
// agent_transcript_path; PreToolUse inside a subagent carries agent_id too.
// Both are what let a subagent's work be kept apart from its parent's.
type hookPayload struct {
	SessionID           string         `json:"session_id"`
	TranscriptPath      string         `json:"transcript_path"`
	CWD                 string         `json:"cwd"`
	HookEventName       string         `json:"hook_event_name"`
	AgentID             string         `json:"agent_id"`
	AgentTranscriptPath string         `json:"agent_transcript_path"`
	ToolName            string         `json:"tool_name"`
	ToolInput           map[string]any `json:"tool_input"`
}

// sessionRef names the work the payload is about: the session, or the
// subagent within it.
func (p hookPayload) sessionRef() string { return agenthook.SessionRef(p.SessionID, p.AgentID) }

// transcript is the file holding that work's turns.
func (p hookPayload) transcript() string {
	return firstNonBlank(p.AgentTranscriptPath, p.TranscriptPath)
}

func (a *App) hookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Endpoints your coding harness calls; you do not run these yourself",
		Long: "Harness hooks that record what an agent actually spent on a card.\n\n" +
			"`fulcrum mcp install` writes these into your harness configuration. They\n" +
			"read the harness's own transcript, because THE MODEL CANNOT REPORT ITS\n" +
			"OWN USAGE — no harness exposes token counts to the model it is running,\n" +
			"so no MCP tool could ever carry them.",
	}
	cmd.AddCommand(a.hookStopCmd(), a.hookToolUseCmd())
	return cmd
}

func (a *App) hookStopCmd() *cobra.Command {
	var transcript, cwd, session string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Record the turns an agent has just finished",
		Long: "Reads a harness Stop payload on stdin and records the session's turns\n" +
			"against the card the session is pinned to, or else the checkout's.\n\n" +
			"A session pin is written when start_work names the session, and is a\n" +
			"window: only turns that overlap it — from the turn that called\n" +
			"start_work to the turn that called finish_work — are recorded. Turns\n" +
			"outside it are passed over for good. The checkout pin has no window.\n\n" +
			"Does nothing at all when there is no pin: a hook fires on its\n" +
			"own and cannot ask anybody anything, so with no pin there is no honest\n" +
			"answer to which card the work belongs to, and guessing would put one\n" +
			"project's cost on another's card.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runHookStop(transcript, cwd, session, dryRun)
		},
	}
	cmd.Flags().StringVar(&transcript, "transcript", "", "transcript to read (default: from the payload)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "checkout the session ran in (default: from the payload)")
	cmd.Flags().StringVar(&session, "session", "", "harness session reference (default: from the payload)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be recorded, and post nothing")
	return cmd
}

// hookToolUseCmd is Claude Code's PreToolUse hook. It exists for one reason:
// the model does not know its own session id, and should not have to. When
// a start_work or finish_work call goes by without a session_ref, the hook
// fills it in from the payload, so the MCP bridge can pin the session to the
// card — which is what keeps parallel subagents on their own cards.
func (a *App) hookToolUseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tool-use",
		Short: "Name the session on the start_work and finish_work calls an agent makes",
		Long: "Reads a harness PreToolUse payload on stdin. When the tool is Fulcrum's\n" +
			"start_work or finish_work and the call does not name a session_ref, it\n" +
			"answers with the same call plus the session's own reference, so the work\n" +
			"episode and the telemetry that follows are keyed by the session rather\n" +
			"than by whichever card the checkout happens to be pinned to.\n\n" +
			"Every other call, and every call that already names a session, is left\n" +
			"exactly as it is: the hook prints nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runHookToolUse()
		},
	}
	return cmd
}

// runHookToolUse NEVER FAILS THE SESSION, for the same reason runHookStop
// does not: a PreToolUse hook that exits non-zero blocks the tool call.
func (a *App) runHookToolUse() error {
	payload := a.readHookPayload()
	amended, ok := agenthook.InjectSessionRef(payload.ToolName, payload.ToolInput, payload.sessionRef())
	if !ok {
		return nil
	}
	// Claude Code's contract for changing a call: hookSpecificOutput with
	// the amended input. The decision must be stated for the input to apply;
	// "allow" is honest here, because these two tools only open and close a
	// work episode.
	answer := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "allow",
			"updatedInput":       amended,
		},
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: could not encode the tool-use answer: %v\n", err)
		return nil
	}
	fmt.Fprintln(a.Stdout, string(encoded))
	return nil
}

// runHookStop NEVER FAILS THE SESSION.
//
// It returns nil on every path, including every error path, and says what
// went wrong on stderr instead. A hook that exits non-zero interrupts the
// developer — and in a Stop hook it can be fed back to the model as something
// to fix. Telemetry is not worth either. The one thing it must not do is
// silently look successful, hence the diagnostics.
func (a *App) runHookStop(transcriptFlag, cwdFlag, sessionFlag string, dryRun bool) error {
	payload := a.readHookPayload()
	transcriptPath := firstNonBlank(transcriptFlag, payload.transcript())
	sessionRef := firstNonBlank(sessionFlag, payload.sessionRef())
	workingDir := firstNonBlank(cwdFlag, payload.CWD)
	if workingDir == "" {
		workingDir, _ = os.Getwd()
	}

	if transcriptPath == "" {
		fmt.Fprintln(a.Stderr, "fulcrum: no transcript in the hook payload; nothing recorded")
		return nil
	}
	if sessionRef == "" {
		fmt.Fprintln(a.Stderr, "fulcrum: no session id in the hook payload; nothing recorded")
		return nil
	}

	stateDir, err := a.configDir()
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: %v\n", err)
		return nil
	}

	// Which card: the session's own pin first — written by the MCP bridge
	// when this session called start_work with its reference, which is what
	// keeps parallel subagents on their own cards — then the checkout's. Only
	// a session pin carries a window; the checkout pin claims every turn.
	var work *projectctx.CurrentWork
	var window *agenthook.SessionPin
	pinSource := "checkout"
	if pin := agenthook.ReadSessionPin(stateDir, sessionRef); pin != nil {
		pinSource = "session"
		window = pin
		work = &projectctx.CurrentWork{
			Feature: pin.Feature, FeatureID: pin.FeatureID, ProjectID: pin.ProjectID, Role: pin.Role,
		}
	} else {
		root, err := a.checkoutRoot(workingDir)
		if err != nil {
			fmt.Fprintf(a.Stderr, "fulcrum: %v\n", err)
			return nil
		}
		work = projectctx.ReadCurrentWork(root)
	}
	if work == nil {
		// An ordinary state, and the acceptance criterion: record nothing
		// rather than guess. Silent, because most checkouts are not pinned and
		// a warning on every turn would be noise.
		return nil
	}
	if work.FeatureID == 0 {
		fmt.Fprintf(a.Stderr,
			"fulcrum: the pin for %s predates telemetry and has no feature id — "+
				"re-run `fulcrum work %s` to refresh it\n", work.Feature, work.Feature)
		return nil
	}

	file, err := os.Open(transcriptPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: cannot read the transcript: %v\n", err)
		return nil
	}
	defer file.Close()

	turns, err := agenthook.ParseTranscript(file)
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: cannot parse the transcript: %v\n", err)
		return nil
	}

	state := agenthook.LoadState(stateDir)
	agenthook.PruneSessionPins(stateDir, time.Now())
	through := state.PostedThrough(sessionRef)
	plan := planTurns(turns, through, window)

	if dryRun {
		a.reportDryRun(work, pinSource, sessionRef, window, turns, plan)
		return nil
	}
	// Turns the window excludes are settled before anything is posted, so
	// they are never considered again — and never land on a later pin —
	// even if the post below fails.
	a.advanceWatermark(state, stateDir, sessionRef, work.Feature, plan.through(0))
	if len(plan.post) == 0 {
		return nil
	}
	pending := make([]api.TelemetryTurn, 0, len(plan.post))
	for _, turn := range plan.post {
		pending = append(pending, api.TelemetryTurn{
			TurnIndex: turn.Index,
			// Nano, not RFC3339: the second-precision format truncates both
			// ends of every turn, which moved a real session's active time by
			// three seconds and would matter far more on short turns.
			StartedAt:           turn.StartedAt.Format(time.RFC3339Nano),
			EndedAt:             turn.EndedAt.Format(time.RFC3339Nano),
			InputTokens:         turn.Usage.InputTokens,
			OutputTokens:        turn.Usage.OutputTokens,
			CacheCreationTokens: turn.Usage.CacheCreationTokens,
			CacheReadTokens:     turn.Usage.CacheReadTokens,
			Model:               turn.Usage.Model,
		})
	}

	resolved, err := a.resolveConfig()
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: %v\n", err)
		return nil
	}
	client, err := a.client(resolved)
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: %v\n", err)
		return nil
	}

	// Batch to the server's cap. Each batch's watermark is saved as it lands,
	// so a failure halfway does not re-send what already succeeded.
	for start := 0; start < len(pending); start += maxTurnsPerRequest {
		end := min(start+maxTurnsPerRequest, len(pending))
		batch := pending[start:end]

		if _, err := client.PostAgentTelemetry(
			background(), work.ProjectID, work.FeatureID, sessionRef, work.Role, batch,
		); err != nil {
			fmt.Fprintf(a.Stderr, "fulcrum: could not record agent time: %v\n", wrapAPIError(err))
			break
		}
		a.advanceWatermark(state, stateDir, sessionRef, work.Feature, plan.through(end))
	}
	return nil
}

// turnPlan splits the turns above the watermark into those the pin claims and
// those its window excludes.
type turnPlan struct {
	post []agenthook.Turn
	// skipped counts the turns above the watermark that fall outside the
	// window. They are never posted, by this pin or any later one.
	skipped int
	// considered is the highest turn index above the watermark, posted or
	// not; once everything to post has landed, the watermark moves here.
	considered int
}

func planTurns(turns []agenthook.Turn, through int, window *agenthook.SessionPin) turnPlan {
	plan := turnPlan{considered: through}
	for _, turn := range turns {
		if turn.Index <= through {
			continue
		}
		plan.considered = max(plan.considered, turn.Index)
		if window != nil && !window.Covers(turn) {
			plan.skipped++
			continue
		}
		plan.post = append(plan.post, turn)
	}
	return plan
}

// through is how far the watermark may safely move once the first `landed`
// turns to post have been recorded: up to just below the next turn still to
// post, so excluded turns between posted ones are settled with them, or to
// the last turn considered once nothing remains to post. Excluded 1–25,
// posted 26–30, excluded 31–40 gives 25 before posting and 40 after.
func (p turnPlan) through(landed int) int {
	if landed < len(p.post) {
		return p.post[landed].Index - 1
	}
	return p.considered
}

// advanceWatermark records and saves the watermark when it moves forward.
func (a *App) advanceWatermark(state *agenthook.State, stateDir, sessionRef, feature string, to int) {
	if to <= state.PostedThrough(sessionRef) {
		return
	}
	state.Record(sessionRef, feature, to, time.Now())
	if err := state.Save(stateDir, time.Now()); err != nil {
		// Losing the watermark costs a re-send and nothing else, because
		// the server dedupes on (session_ref, turn_index).
		fmt.Fprintf(a.Stderr, "fulcrum: could not save the telemetry watermark: %v\n", err)
	}
}

func (a *App) reportDryRun(work *projectctx.CurrentWork, pinSource, sessionRef string, window *agenthook.SessionPin, turns []agenthook.Turn, plan turnPlan) {
	fmt.Fprintf(a.Stdout, "card:     %s (feature %d, project %d) via the %s pin\n", work.Feature, work.FeatureID, work.ProjectID, pinSource)
	fmt.Fprintf(a.Stdout, "session:  %s\n", sessionRef)
	fmt.Fprintf(a.Stdout, "window:   %s\n", describeWindow(window))
	fmt.Fprintf(a.Stdout, "turns:    %d in the transcript, %d not yet recorded: %d to post, %d outside the window\n",
		len(turns), len(plan.post)+plan.skipped, len(plan.post), plan.skipped)

	var active time.Duration
	var input, output, cacheCreation, cacheRead int64
	for _, turn := range plan.post {
		active += turn.Duration()
		input += turn.Usage.InputTokens
		output += turn.Usage.OutputTokens
		cacheCreation += turn.Usage.CacheCreationTokens
		cacheRead += turn.Usage.CacheReadTokens
	}
	fmt.Fprintf(a.Stdout, "active:   %s in the turns to post\n", active.Round(time.Second))
	fmt.Fprintf(a.Stdout, "tokens:   in %d, out %d, cache write %d, cache read %d\n",
		input, output, cacheCreation, cacheRead)
}

// describeWindow names the span a pin claims, for the operator reading a dry
// run.
func describeWindow(window *agenthook.SessionPin) string {
	if window == nil || !window.Windowed() {
		return "none (every turn not yet recorded posts)"
	}
	from, to := "the start of the session", "open (no finish_work yet)"
	if !window.StartedAt.IsZero() {
		from = window.StartedAt.UTC().Format(time.RFC3339)
	}
	if !window.FinishedAt.IsZero() {
		to = window.FinishedAt.UTC().Format(time.RFC3339)
	}
	return from + " → " + to
}

// readHookPayload decodes the harness's JSON from stdin. An empty or
// unparseable stdin yields a zero payload rather than an error: the flags
// exist so this can be driven by hand, and a hook run with no stdin should
// explain itself rather than crash.
func (a *App) readHookPayload() hookPayload {
	var payload hookPayload
	if a.Stdin == nil {
		return payload
	}
	content, err := io.ReadAll(io.LimitReader(a.Stdin, 1<<20))
	if err != nil || len(content) == 0 {
		return payload
	}
	_ = json.Unmarshal(content, &payload)
	return payload
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
