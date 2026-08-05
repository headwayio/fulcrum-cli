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
// discovering the limit as a 422 keeps a long session working.
const maxTurnsPerRequest = 200

// hookPayload is the JSON a harness writes to the hook's stdin. Only the
// fields telemetry needs are decoded; the rest is ignored so a harness can
// extend the shape without breaking us.
type hookPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
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
	cmd.AddCommand(a.hookStopCmd())
	return cmd
}

func (a *App) hookStopCmd() *cobra.Command {
	var transcript, cwd, session string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Record the turns an agent has just finished",
		Long: "Reads a harness Stop payload on stdin and records the session's turns\n" +
			"against the card this checkout is pinned to.\n\n" +
			"Does nothing at all when the checkout has no pin: a hook fires on its\n" +
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

// runHookStop NEVER FAILS THE SESSION.
//
// It returns nil on every path, including every error path, and says what
// went wrong on stderr instead. A hook that exits non-zero interrupts the
// developer — and in a Stop hook it can be fed back to the model as something
// to fix. Telemetry is not worth either. The one thing it must not do is
// silently look successful, hence the diagnostics.
func (a *App) runHookStop(transcriptFlag, cwdFlag, sessionFlag string, dryRun bool) error {
	payload := a.readHookPayload()
	transcriptPath := firstNonBlank(transcriptFlag, payload.TranscriptPath)
	sessionRef := firstNonBlank(sessionFlag, payload.SessionID)
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

	// The pin is the only thing that knows which card this is about.
	root, err := a.checkoutRoot(workingDir)
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: %v\n", err)
		return nil
	}
	work := projectctx.ReadCurrentWork(root)
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

	turns, err := agenthook.ParseClaudeTranscript(file)
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: cannot parse the transcript: %v\n", err)
		return nil
	}

	stateDir, err := a.configDir()
	if err != nil {
		fmt.Fprintf(a.Stderr, "fulcrum: %v\n", err)
		return nil
	}
	state := agenthook.LoadState(stateDir)
	through := state.PostedThrough(sessionRef)

	pending := make([]api.TelemetryTurn, 0, len(turns))
	for _, turn := range turns {
		if turn.Index <= through {
			continue
		}
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

	if dryRun {
		a.reportDryRun(work, sessionRef, turns, pending)
		return nil
	}
	if len(pending) == 0 {
		return nil
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
	posted := through
	for start := 0; start < len(pending); start += maxTurnsPerRequest {
		end := min(start+maxTurnsPerRequest, len(pending))
		batch := pending[start:end]

		if _, err := client.PostAgentTelemetry(
			background(), work.ProjectID, work.FeatureID, sessionRef, work.Role, batch,
		); err != nil {
			fmt.Fprintf(a.Stderr, "fulcrum: could not record agent time: %v\n", wrapAPIError(err))
			break
		}
		posted = batch[len(batch)-1].TurnIndex
		state.Record(sessionRef, work.Feature, posted, time.Now())
		if err := state.Save(stateDir, time.Now()); err != nil {
			// Losing the watermark costs a re-send and nothing else, because
			// the server dedupes on (session_ref, turn_index).
			fmt.Fprintf(a.Stderr, "fulcrum: could not save the telemetry watermark: %v\n", err)
		}
	}
	return nil
}

func (a *App) reportDryRun(work *projectctx.CurrentWork, sessionRef string, turns []agenthook.Turn, pending []api.TelemetryTurn) {
	fmt.Fprintf(a.Stdout, "card:     %s (feature %d, project %d)\n", work.Feature, work.FeatureID, work.ProjectID)
	fmt.Fprintf(a.Stdout, "session:  %s\n", sessionRef)
	fmt.Fprintf(a.Stdout, "turns:    %d in the transcript, %d not yet recorded\n", len(turns), len(pending))

	var active time.Duration
	var input, output, cacheCreation, cacheRead int64
	for _, turn := range turns {
		active += turn.Duration()
		input += turn.Usage.InputTokens
		output += turn.Usage.OutputTokens
		cacheCreation += turn.Usage.CacheCreationTokens
		cacheRead += turn.Usage.CacheReadTokens
	}
	fmt.Fprintf(a.Stdout, "active:   %s\n", active.Round(time.Second))
	fmt.Fprintf(a.Stdout, "tokens:   in %d, out %d, cache write %d, cache read %d\n",
		input, output, cacheCreation, cacheRead)
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
