package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/mcpinstall"
	"github.com/headwayio/fulcrum-cli/internal/projectctx"
)

// Harnesses this can launch. The command is the binary's usual name; a
// harness that is not installed is reported rather than guessed at.
var harnessCommands = map[string]string{
	mcpinstall.TargetClaude: "claude",
	mcpinstall.TargetCodex:  "codex",
	mcpinstall.TargetKimi:   "kimi",
}

func (a *App) workCmd() *cobra.Command {
	var feature, role, harness, dir string
	var noLaunch bool

	cmd := &cobra.Command{
		Use:   "work [card]",
		Short: "Pick a card and start working it in your harness",
		Long: "Pins a card to this checkout and opens your coding harness on it.\n\n" +
			"The pin is the point: it is how `where_am_i` — and the telemetry hooks,\n" +
			"which cannot ask anybody anything — know which card a session is about.\n" +
			"Without it every tool call has to name the card again.\n\n" +
			"Registers the MCP server if this checkout has not got one yet, so the\n" +
			"first run in a new repository is the only setup there is.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				feature = args[0]
			}
			return a.runWork(feature, role, harness, dir, noLaunch)
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "card to work, e.g. FUL-17")
	cmd.Flags().StringVar(&role, "role", "", "role you are working as; defaults to the card's dominant estimate")
	cmd.Flags().StringVar(&harness, "harness", mcpinstall.TargetClaude,
		"harness to launch (claude, codex, kimi)")
	cmd.Flags().StringVar(&dir, "dir", ".", "checkout to work in")
	cmd.Flags().BoolVar(&noLaunch, "no-launch", false, "pin the card and print the prompt, but launch nothing")

	cmd.AddCommand(a.workClearCmd())
	return cmd
}

func (a *App) workClearCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Unpin the card this checkout was working",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := a.checkoutRoot(dir)
			if err != nil {
				return err
			}
			if err := projectctx.ClearCurrentWork(root); err != nil {
				return exitf(ExitError, "could not clear the pin: %v", err)
			}
			fmt.Fprintln(a.Stdout, "unpinned")
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "checkout to clear")
	return cmd
}

func (a *App) runWork(feature, role, harness, dir string, noLaunch bool) error {
	local, root, err := a.resolveCheckout(dir)
	if err != nil {
		return err
	}

	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	client, err := a.client(resolved)
	if err != nil {
		return err
	}

	if strings.TrimSpace(feature) == "" {
		return exitf(ExitError,
			"name a card to work: `fulcrum work FUL-17`.\n"+
				"To see what is open, ask your harness for find_features, or run\n"+
				"`fulcrum work --help`.")
	}

	// Read the brief before pinning: a card that does not exist should fail
	// here, not after the harness has already started.
	brief, err := client.McpCall(background(), "get_feature", map[string]any{"feature": feature})
	if err != nil {
		return wrapAPIError(err)
	}
	if brief.IsError {
		return exitf(ExitError, "%s", brief.Text())
	}

	name := featureName(brief.Text())
	work := &projectctx.CurrentWork{
		Feature: feature,
		Name:    name,
		// The NUMERIC ids, lifted from the brief we already have. The
		// telemetry hook needs them and cannot go and ask: it fires on its own
		// and has no way to turn "FUL-17" into a row. Taking them here costs
		// nothing, because the brief has just been fetched.
		FeatureID: briefID(brief.Text(), "feature_id"),
		ProjectID: local.ProjectID,
		Role:      role,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if projectID := briefID(brief.Text(), "project_id"); projectID != 0 {
		work.ProjectID = projectID
	}
	if err := projectctx.WriteCurrentWork(root, work); err != nil {
		return exitf(ExitError, "could not pin the card: %v", err)
	}
	fmt.Fprintf(a.Stdout, "pinned %s%s in %s\n", feature, suffix(name), root)

	// First run in a new checkout is the only setup there is.
	if err := a.ensureRegistered(root, harness); err != nil {
		return err
	}

	prompt := starterPrompt(feature, name, role)
	if noLaunch {
		fmt.Fprintf(a.Stdout, "\n%s\n", prompt)
		return nil
	}

	return a.launch(harness, root, prompt)
}

// ensureRegistered installs the MCP server for the chosen harness if it is
// missing, and says nothing when it was already there.
func (a *App) ensureRegistered(root, harness string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return exitf(ExitError, "cannot find your home directory: %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return exitf(ExitError, "cannot determine this binary's path: %v", err)
	}
	if link, linkErr := filepath.EvalSymlinks(executable); linkErr == nil {
		executable = link
	}

	results, err := mcpinstall.Install([]string{harness}, mcpinstall.Options{
		ProjectDir: root, HomeDir: home, Command: executable, Args: []string{"mcp"},
	})
	if err != nil {
		return exitf(ExitError, "%v", err)
	}
	for _, result := range results {
		if result.Changed {
			fmt.Fprintf(a.Stdout, "registered the Fulcrum MCP server with %s\n", result.Target)
		}
	}

	hookResults, err := mcpinstall.InstallHooks([]string{harness}, mcpinstall.Options{
		ProjectDir: root, HomeDir: home, Command: executable,
	})
	if err != nil {
		return exitf(ExitError, "%v", err)
	}
	for _, result := range hookResults {
		if result.Changed {
			fmt.Fprintf(a.Stdout, "now recording agent time and tokens from %s\n", result.Target)
		}
	}
	return nil
}

func (a *App) launch(harness, root, prompt string) error {
	command, known := harnessCommands[harness]
	if !known {
		return exitf(ExitError, "unknown harness %q — known: claude, codex, kimi", harness)
	}
	path, err := exec.LookPath(command)
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s is not on your PATH, so nothing was launched.\n", command)
		fmt.Fprintf(a.Stdout, "\nThe card is pinned. Start it yourself with:\n\n  %s\n\n%s\n",
			command, prompt)
		return nil
	}

	fmt.Fprintf(a.Stdout, "launching %s…\n\n", command)

	// Hand the terminal over wholesale: the harness is interactive and owns
	// stdin from here.
	child := exec.Command(path, prompt)
	child.Dir = root
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return silentExit(exitErr.ExitCode())
		}
		return exitf(ExitError, "could not launch %s: %v", command, err)
	}
	return nil
}

func (a *App) resolveCheckout(dir string) (*projectctx.Local, string, error) {
	root, err := a.checkoutRoot(dir)
	if err != nil {
		return nil, "", err
	}
	local, _ := projectctx.Resolve(root)
	if local == nil {
		return nil, "", exitf(ExitError,
			"this checkout has no Fulcrum project linked.\n"+
				"Run `fulcrum context --project <name>` in it first.")
	}
	return local, local.Root, nil
}

func (a *App) checkoutRoot(dir string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", exitf(ExitError, "%v", err)
	}
	if local, _ := projectctx.Resolve(absolute); local != nil {
		return local.Root, nil
	}
	return absolute, nil
}

// featureName lifts the card's name out of the brief's heading, which reads
// "# FUL-17 — Dynamic field mapping". Best effort: the pin is still useful
// with only the id.
func featureName(brief string) string {
	for _, line := range strings.Split(brief, "\n") {
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		heading := strings.TrimPrefix(line, "# ")
		if _, after, found := strings.Cut(heading, "—"); found {
			return strings.TrimSpace(after)
		}
		return strings.TrimSpace(heading)
	}
	return ""
}

// briefID reads a numeric field out of the brief's YAML frontmatter. Best
// effort by design: a brief that has not got the field yet leaves the pin
// without it, and the hook says so rather than posting against a guess.
func briefID(brief, key string) int64 {
	for _, line := range strings.Split(brief, "\n") {
		if line == "---" && strings.HasPrefix(brief, "---") {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0
		}
		return id
	}
	return 0
}

func suffix(name string) string {
	if name == "" {
		return ""
	}
	return " (" + name + ")"
}

// The starter prompt names the tools rather than pasting the brief: the point
// of the server is that the agent fetches what it needs when it needs it, and
// a prompt that front-loads the card is the snapshot this replaces.
func starterPrompt(feature, name, role string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "I'm working Fulcrum card %s", feature)
	if name != "" {
		fmt.Fprintf(&b, " (%s)", name)
	}
	b.WriteString(" in this repository.\n\n")
	b.WriteString("Start by calling where_am_i and get_feature to read the brief. ")
	b.WriteString("Call start_work")
	if role != "" {
		fmt.Fprintf(&b, " with role %q", role)
	}
	b.WriteString(" before you begin, and finish_work when you stop. ")
	b.WriteString("Keep the board honest as you go: move the card with update_feature, ")
	b.WriteString("tick off tasks with update_tasks, link the branch once you have one, ")
	b.WriteString("and comment if you hit something a person needs to decide.")
	return b.String()
}
