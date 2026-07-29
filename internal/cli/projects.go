package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

func (a *App) projectsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "List the organization's active projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := a.resolveConfig()
			if err != nil {
				return err
			}
			client, err := a.client(resolved)
			if err != nil {
				return err
			}
			projects, err := client.Projects(background())
			if err != nil {
				return wrapAPIError(err)
			}

			if asJSON {
				encoded, _ := json.MarshalIndent(map[string]any{"projects": projects}, "", "  ")
				fmt.Fprintln(a.Stdout, string(encoded))
				return nil
			}
			fmt.Fprintf(a.Stdout, "%-6s %-32s %-24s %s\n", "ID", "PROJECT", "CLIENT", "ARCHITECTURE")
			for _, p := range projects {
				arch := "—"
				if p.HasArchitectureProfile {
					arch = "profiled"
					if p.ArchitectureCollectedAt != nil {
						arch = "profiled " + *p.ArchitectureCollectedAt
					}
				}
				fmt.Fprintf(a.Stdout, "%-6d %-32s %-24s %s\n", p.ID, p.Name, p.ClientName, arch)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

// resolveProject accepts a numeric id or an unambiguous name. Unknown or
// ambiguous names exit 2 listing candidates — never a guess.
func resolveProject(client *api.Client, ref string) (*api.Project, error) {
	projects, err := client.Projects(background())
	if err != nil {
		return nil, wrapAPIError(err)
	}

	if id, numErr := strconv.ParseInt(ref, 10, 64); numErr == nil {
		for i := range projects {
			if projects[i].ID == id {
				return &projects[i], nil
			}
		}
		return nil, exitf(ExitError, "no project with id %d — run `fulcrum projects`", id)
	}

	var exact []*api.Project
	var partial []*api.Project
	needle := strings.ToLower(ref)
	for i := range projects {
		name := strings.ToLower(projects[i].Name)
		switch {
		case name == needle:
			exact = append(exact, &projects[i])
		case strings.Contains(name, needle):
			partial = append(partial, &projects[i])
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, exitf(ExitError, "no project matches %q — run `fulcrum projects`", ref)
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = fmt.Sprintf("%d=%s", m.ID, m.Name)
		}
		return nil, exitf(ExitError, "%q is ambiguous: %s", ref, strings.Join(names, ", "))
	}
}
