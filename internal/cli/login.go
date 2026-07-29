package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/config"
)

func (a *App) loginCmd() *cobra.Command {
	var noKeychain bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Connect to a Fulcrum server and store your token",
		Long: "Prompts for the server URL and your API token, validates them with a\n" +
			"live manifest fetch (echoing the organization it resolves to), then\n" +
			"stores the URL in config.json and the token in your OS keychain\n" +
			"(--no-keychain: a 0600 file instead). The token is minted at\n" +
			"<server>/settings/developer — any signed-in member can.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLogin(noKeychain)
		},
	}
	cmd.Flags().BoolVar(&noKeychain, "no-keychain", false, "store the token in a 0600 file instead of the OS keychain")
	return cmd
}

func (a *App) runLogin(noKeychain bool) error {
	dir, err := a.configDir()
	if err != nil {
		return exitf(ExitError, "no config dir: %v", err)
	}
	file, err := config.LoadFile(dir)
	if err != nil {
		return exitf(ExitError, "%v", err)
	}

	fmt.Fprintf(a.Stdout, "Server URL%s: ", defaultHint(file.URL))
	url := strings.TrimSpace(a.readLine())
	if url == "" {
		url = file.URL
	}
	if url == "" {
		return exitf(ExitError, "a server URL is required (e.g. https://usefulcrum.ai)")
	}
	url = strings.TrimSuffix(url, "/")

	fmt.Fprintf(a.Stdout, "Mint a personal token at %s/settings/developer (shown once).\n", url)
	fmt.Fprint(a.Stdout, "API token: ")
	token, err := a.readSecret()
	if err != nil || token == "" {
		return exitf(ExitError, "an API token is required")
	}

	client := &api.Client{BaseURL: url, Token: token, Version: a.Version}
	manifest, _, err := client.Manifest(background(), "")
	if apiErr, ok := api.AsError(err); ok && apiErr.Code == "organization_required" {
		// Multi-org token: pick explicitly, then re-validate.
		fmt.Fprintf(a.Stdout, "%s\n", apiErr.ServerMessage)
		fmt.Fprint(a.Stdout, "Organization id: ")
		file.OrganizationID = strings.TrimSpace(a.readLine())
		client.OrganizationID = file.OrganizationID
		manifest, _, err = client.Manifest(background(), "")
	}
	if err != nil {
		return wrapAPIError(err)
	}

	file.URL = url
	if err := file.SaveFile(dir); err != nil {
		return exitf(ExitError, "save config: %v", err)
	}

	ring := a.keyring()
	if noKeychain {
		ring = nil
	}
	where, err := config.StoreToken(dir, ring, token)
	if err != nil {
		return exitf(ExitError, "store token: %v", err)
	}

	who := ""
	if manifest.User != nil {
		who = " as " + manifest.User.Email
	}
	fmt.Fprintf(a.Stdout, "Connected to %s%s (token in %s).\n", manifest.Organization.Name, who, where)
	return nil
}

func (a *App) readSecret() (string, error) {
	if f, ok := a.Stdin.(*os.File); ok && isatty.IsTerminal(f.Fd()) && a.getenv("FULCRUM_FORCE_TTY") != "1" {
		secret, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.Stdout)
		return strings.TrimSpace(string(secret)), err
	}
	return strings.TrimSpace(a.readLine()), nil
}

func defaultHint(current string) string {
	if current == "" {
		return ""
	}
	return " [" + current + "]"
}
