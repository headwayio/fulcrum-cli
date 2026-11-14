package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Reading a card and what is waiting for review on it, and making the review
// decision, from a terminal.
//
// These are the server's own MCP tools — get_feature, get_feature_prd,
// get_prd_suggestions and review_pending — called directly, so a person at a
// shell and an agent in a harness read the same words and go through the same
// gate. Nothing is decided here: whether a token may accept or reject is its
// direct access on the card's project, which an admin grants and the server
// checks.

func (a *App) featureShowCmd() *cobra.Command {
	var prd, pending bool
	cmd := &cobra.Command{
		Use:   "show <card>",
		Short: "Read a card, including what is waiting for review on it",
		Long: "Prints a card's brief: description, requirements, estimates, tasks,\n" +
			"dependencies and comments — and, apart from those, what is WAITING\n" +
			"FOR REVIEW on it: a pending change and any PRD suggestions.\n\n" +
			"<card> is a short id such as FUL-17 or a numeric id. Your own Inbox\n" +
			"proposals are readable; with direct access on the project, anybody's.\n\n" +
			"--prd prints the whole PRD instead of the brief. --pending prints the\n" +
			"replacement PRD a pending change proposes (it is not on the card yet).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments := map[string]any{"feature": args[0]}
			switch {
			case pending:
				arguments["pending"] = true
				return a.printTool("get_feature_prd", arguments)
			case prd:
				return a.printTool("get_feature_prd", arguments)
			default:
				return a.printTool("get_feature", arguments)
			}
		},
	}
	cmd.Flags().BoolVar(&prd, "prd", false, "print the card's whole PRD instead of the brief")
	cmd.Flags().BoolVar(&pending, "pending", false, "print the replacement PRD waiting for review")
	return cmd
}

func (a *App) featureSuggestionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "suggestions <card> [suggestion-id]",
		Short: "Read the PRD suggestions on a card, drafts included",
		Long: "Lists a card's PRD suggestions. A pending one is shown with what it\n" +
			"says — the text it would add, replace or remove, or the task,\n" +
			"dependency or label it would change — so a draft the PRD chat left\n" +
			"waiting can be read before it is decided.\n\n" +
			"A long suggestion is cut at a stated length; name its id to read the\n" +
			"whole of it.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments := map[string]any{"feature": args[0]}
			if len(args) == 2 {
				arguments["suggestion"] = args[1]
			}
			return a.printTool("get_prd_suggestions", arguments)
		},
	}
	return cmd
}

func (a *App) featurePendingCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List what is waiting for review on a project",
		Long: "Lists a project's Inbox proposals, the cards with PRD suggestions\n" +
			"waiting, and the cards with a change waiting.\n\n" +
			"Needs direct access on the project: what waits for review there is\n" +
			"listed only to a token that may decide it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.printTool("review_pending", map[string]any{"project": project})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project id or unambiguous name (required)")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

// featureReviewCmd is `feature accept` or `feature reject`: the same call
// with a different decision.
func (a *App) featureReviewCmd(decision string) *cobra.Command {
	var suggestion, reason string
	outcome := map[string]string{
		"accept": "Accepting a proposal puts the card on the board and into the estimate;\n" +
			"accepting a suggestion writes it into the requirements, or applies the\n" +
			"task, dependency or label it proposes. A locked card is not accepted\n" +
			"onto — a person unlocks it in Fulcrum.",
		"reject": "Rejecting a proposal leaves it archived, off the board; rejecting a\n" +
			"suggestion leaves the requirements as they are.",
	}[decision]
	cmd := &cobra.Command{
		Use:   decision + " <card>",
		Short: strings.ToUpper(decision[:1]) + decision[1:] + " an Inbox proposal, or a PRD suggestion on a card",
		Long: "Makes the review decision on something waiting for it: the Inbox\n" +
			"proposal <card>, or with --suggestion a PRD suggestion on that card\n" +
			"(`fulcrum feature suggestions <card>` lists their ids).\n\n" +
			outcome + "\n\n" +
			"Needs direct access on the card's project. The decision and --reason\n" +
			"are recorded on the card as your agent's.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return exitf(ExitError,
					"say why with --reason: it is recorded on the card with the decision")
			}
			arguments := map[string]any{"feature": args[0], "decision": decision, "reason": reason}
			if suggestion != "" {
				arguments["suggestion"] = suggestion
			}
			return a.printTool("review_pending", arguments)
		},
	}
	cmd.Flags().StringVar(&suggestion, "suggestion", "", "a PRD suggestion's id, such as sug-3f9a1c2b7d4e; omit to decide the proposal itself")
	cmd.Flags().StringVar(&reason, "reason", "", "why, in a sentence or two (required)")
	return cmd
}

// printTool calls one MCP tool and prints what it said. A tool that refuses —
// no such card, no direct access, a locked card — answers with words meant to
// be read, so they go to stderr and the command exits 2.
func (a *App) printTool(name string, arguments map[string]any) error {
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	client, err := a.client(resolved)
	if err != nil {
		return err
	}
	result, err := client.McpCall(background(), name, arguments)
	if err != nil {
		return wrapAPIError(err)
	}
	if result.IsError {
		return exitf(ExitError, "%s", result.Text())
	}
	fmt.Fprintln(a.Stdout, result.Text())
	return nil
}
