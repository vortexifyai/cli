package cmd

import (
	"github.com/spf13/cobra"

	"vortexify-cli/internal/commit"
)

func newCommitCommand(opts *options) *cobra.Command {
	filter := commit.Filter{}
	command := &cobra.Command{Use: "commit", Short: "Commit ready local resources and report blocked changes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		service := commit.Service{Client: client, Output: cmd.OutOrStdout()}
		return service.Run(cmd.Context(), root, cfg, filter)
	}}
	command.Flags().StringVar(&filter.Type, "type", "", "Filter by resource type")
	command.Flags().StringVar(&filter.Resource, "resource", "", "Filter by resource name or ID")
	return command
}
