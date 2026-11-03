package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/auth"
)

const devOAuthClientID = "sIuJc9gIdE8ihEpD"
const devOAuthTokenURL = "https://gentle-mackerel-92.clerk.accounts.dev/oauth/token"
const prodOAuthClientID = "yMhbWleHiewpXd0E"
const prodOAuthTokenURL = "https://clerk.vortexify.app/oauth/token"
const prodOAuthLoginURL = "https://dashboard.vortexify.ai/login/oauth"

func newLoginCommand() *cobra.Command {
	var dev, devLocal bool
	command := &cobra.Command{Use: "login", Short: "Log in through your browser using PKCE", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		options := loginOptions(cmd, dev, devLocal)
		if options.ClientID == "" || options.TokenURL == "" {
			return fmt.Errorf("OAuth client ID and token URL are required; supply --client-id and --token-url")
		}
		credentials, err := auth.Login(cmd.Context(), options)
		if err != nil {
			return err
		}
		if err = auth.SaveCredentials(credentials); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Logged in to Vortexify.")
		return nil
	}}
	command.Flags().BoolVar(&dev, "dev", false, "Log in at https://vortexify.dev/login/oauth using development OAuth")
	command.Flags().BoolVar(&devLocal, "dev-local", false, "Log in at http://localhost:5173/login/oauth using development OAuth")
	command.MarkFlagsMutuallyExclusive("dev", "dev-local")
	command.Flags().String("login-url", os.Getenv("VORTEXIFY_LOGIN_URL"), "Override the browser authorization URL")
	command.Flags().String("token-url", os.Getenv("VORTEXIFY_TOKEN_URL"), "Override the OAuth token exchange URL")
	command.Flags().String("client-id", os.Getenv("VORTEXIFY_CLIENT_ID"), "Override the OAuth public client ID")
	command.Flags().String("scope", "profile email offline_access", "Space-separated OAuth scopes")
	return command
}

func loginOptions(cmd *cobra.Command, dev, devLocal bool) auth.LoginOptions {
	options := auth.LoginOptions{LoginURL: prodOAuthLoginURL, ClientID: prodOAuthClientID, TokenURL: prodOAuthTokenURL, Output: cmd.OutOrStdout()}
	if dev || devLocal {
		options.LoginURL = "https://vortexify.dev/login/oauth"
		options.ClientID = devOAuthClientID
		options.TokenURL = devOAuthTokenURL
	}
	if devLocal {
		options.LoginURL = "http://localhost:5173/login/oauth"
	}
	// Explicit flags override presets. Legacy environment defaults apply to plain login
	// only, so a development preset cannot accidentally inherit production OAuth values.
	for name, target := range map[string]*string{"login-url": &options.LoginURL, "token-url": &options.TokenURL, "client-id": &options.ClientID} {
		value, _ := cmd.Flags().GetString(name)
		if cmd.Flags().Changed(name) || (!(dev || devLocal) && value != "") {
			*target = value
		}
	}
	options.Scopes, _ = cmd.Flags().GetString("scope")
	return options
}
