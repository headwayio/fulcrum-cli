package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/estimate"
)

func (a *App) estimateCmd() *cobra.Command {
	var asJSON bool
	var dir string
	cmd := &cobra.Command{
		Use:   "estimate [draft.json]",
		Short: "Derive committed values for a locally-produced estimate",
		Long: "Reads a features-json draft (from the estimation skill, or stdin) and\n" +
			"derives what Fulcrum would derive: the committed value per role as\n" +
			"(low + 4*likely + high) / 6, snapped to this project's complexity scale.\n\n" +
			"The model supplies judgment — the range and the reasoning. It never\n" +
			"supplies the committed value, and it does not do this arithmetic:\n" +
			"a language model doing sums is the weakest link in an otherwise\n" +
			"reproducible chain.\n\n" +
			"Run `fulcrum context --project <ref>` first.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			return a.runEstimate(path, dir, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	cmd.Flags().StringVar(&dir, "dir", ".", "project directory holding "+ContextDir)
	return cmd
}

func (a *App) runEstimate(path, dir string, asJSON bool) error {
	raw, err := readDraft(a.Stdin, path)
	if err != nil {
		return err
	}
	payload, err := estimate.Parse(raw)
	if err != nil {
		return exitf(ExitError, "%v", err)
	}

	if problems := payload.Validate(); len(problems) > 0 {
		fmt.Fprintln(a.Stderr, "This draft does not satisfy the estimate contract:")
		for _, problem := range problems {
			fmt.Fprintf(a.Stderr, "  - %s\n", problem)
		}
		return silentExit(ExitError)
	}

	snapping, err := loadSnapping(dir)
	if err != nil {
		return err
	}

	results := payload.Compute(snapping.Scale)
	if asJSON {
		encoded, _ := json.MarshalIndent(results, "", "  ")
		fmt.Fprintln(a.Stdout, string(encoded))
		return nil
	}
	renderEstimates(a.Stdout, results)
	return nil
}

func readDraft(stdin io.Reader, path string) ([]byte, error) {
	if path == "" || path == "-" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return nil, exitf(ExitError, "read stdin: %v", err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			return nil, exitf(ExitError, "no draft given: pass a file or pipe features-json on stdin")
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, exitf(ExitError, "read %s: %v", path, err)
	}
	return raw, nil
}

// loadSnapping reads the scale the local estimate must snap against. Its
// absence is a real error rather than a silent default: guessing a scale
// would produce committed values that look authoritative and are not.
func loadSnapping(projectDir string) (*api.Snapping, error) {
	path := filepath.Join(projectDir, ContextDir, snappingFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, exitf(ExitError,
			"no %s — run `fulcrum context --project <ref>` first", path)
	}
	snapping := &api.Snapping{}
	if err := json.Unmarshal(raw, snapping); err != nil {
		return nil, exitf(ExitError, "corrupt %s: %v", path, err)
	}
	if len(snapping.Scale) == 0 {
		return nil, exitf(ExitError, "%s carries no complexity scale", path)
	}
	return snapping, nil
}

func renderEstimates(out io.Writer, results []estimate.FeatureResult) {
	for i, result := range results {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s\n", result.Feature.Name)
		if result.Feature.Release != "" {
			fmt.Fprintf(out, "  release: %s · %s\n", result.Feature.Release, result.Feature.MoscowPriority)
		} else {
			fmt.Fprintf(out, "  backlog · %s\n", result.Feature.MoscowPriority)
		}
		fmt.Fprintln(out)

		width := 0
		for _, role := range result.Roles {
			if len(role.Role) > width {
				width = len(role.Role)
			}
		}
		for _, role := range result.Roles {
			fmt.Fprintf(out, "  %-*s  %6s  (%g–%g, %s)\n",
				width, role.Role, role.Committed, role.Low, role.High, role.Confidence)
		}
		fmt.Fprintf(out, "  %-*s  %s\n", width, "", strings.Repeat("─", 6))
		fmt.Fprintf(out, "  %-*s  expected %.1fh · σ %.1f\n", width, "", result.Expected, result.Sigma)
	}
}
