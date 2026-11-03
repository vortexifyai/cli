package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"vortexify-cli/internal/update"
)

type cliUpdater interface {
	Latest(context.Context) (update.Release, error)
	Install(context.Context, update.Release, string, io.Writer, io.Writer) (bool, error)
}

func newUpdateCommand() *cobra.Command {
	return updateCommand(version, update.NewClient(), os.Executable)
}

func updateCommand(installed string, updater cliUpdater, executable func() (string, error)) *cobra.Command {
	var check bool
	command := &cobra.Command{
		Use: "update", Short: "Check for or install the latest CLI release", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			release, err := updater.Latest(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed: %s\nLatest: %s\n", installed, release.Tag)
			comparison, err := update.Compare(installed, release.Tag)
			if err != nil {
				if check {
					fmt.Fprintln(cmd.OutOrStdout(), "This is a development build; its version cannot be compared with a published release.")
					return nil
				}
				return fmt.Errorf("this is a development build; install a published CLI release before using vortexify update")
			}
			if comparison >= 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No newer CLI release is available.")
				return nil
			}
			if check {
				fmt.Fprintln(cmd.OutOrStdout(), "Run vortexify update to install the new release.")
				return nil
			}
			path, err := executable()
			if err != nil {
				return err
			}
			pending, err := updater.Install(cmd.Context(), release, path, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if pending {
				fmt.Fprintln(cmd.OutOrStdout(), "The update will finish in a separate PowerShell window after this CLI exits.")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Updated Vortexify from %s to %s.\n", installed, release.Tag)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&check, "check", false, "Check for a newer release without installing it")
	return command
}
