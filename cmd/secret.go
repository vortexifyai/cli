package cmd

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/api"
)

func newSecretCommand(opts *options) *cobra.Command {
	command := &cobra.Command{Use: "secret", Short: "Manage project secrets"}
	command.AddCommand(newSetSecretCommand(opts, configurationReader))
	return command
}

func newSetSecretCommand(opts *options, reader func(*cobra.Command) func(bool) (string, error)) *cobra.Command {
	return &cobra.Command{Use: "set <name>", Short: "Create or replace a project secret; value input is hidden", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`).MatchString(args[0]) {
			return fmt.Errorf("secret name must start with a letter or underscore and contain only letters, digits, and underscores")
		}
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), "Secret value (hidden): ")
		value, err := reader(cmd)(true)
		if err != nil {
			return err
		}
		if value == "" {
			return fmt.Errorf("secret value must not be empty")
		}
		if err = client.SetProjectSecret(cmd.Context(), cfg.ProjectID, args[0], value); err != nil {
			// Never echo a server error body that might contain the submitted value.
			var response *api.APIError
			if errors.As(err, &response) {
				return fmt.Errorf("secret was not saved (HTTP %d); check project access and retry", response.StatusCode)
			}
			return fmt.Errorf("secret was not saved; check authentication and the server connection")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Saved project secret %s.\n", args[0])
		return nil
	}}
}
