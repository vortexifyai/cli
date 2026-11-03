package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"vortexify-cli/internal/config"
	workspacefs "vortexify-cli/internal/workspace"
)

func newRecoverCommand(opts *options) *cobra.Command {
	return &cobra.Command{Use: "recover", Short: "Recover an interrupted local pull without network access", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := config.FindProjectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		lock, err := workspacefs.Acquire(root)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err = workspacefs.Recover(root, cfg, cmd.OutOrStdout()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Workspace recovery complete.")
		return nil
	}}
}
