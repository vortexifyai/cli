package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"vortexify-cli/internal/projectfiles"
	workspacefs "vortexify-cli/internal/workspace"
)

func newContextCommand(opts *options) *cobra.Command {
	command := &cobra.Command{Use: "context", Short: "Synchronize project context and business knowledge files"}
	for _, action := range []string{"init", "pull", "push", "status"} {
		action := action
		command.AddCommand(&cobra.Command{Use: action, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, c, err := workspace(opts)
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
			if action == "status" {
				statuses, err := projectfiles.States(cmd.Context(), root, cfg, c)
				if err != nil {
					return err
				}
				for _, s := range statuses {
					fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (version %d)\n", s.Collection, s.State, s.Version)
				}
				return nil
			}
			if action == "init" {
				for _, name := range projectfiles.Names {
					if err = c.InitializeCollection(cmd.Context(), cfg.ProjectID, name); err != nil {
						return err
					}
				}
			}
			if _, err = projectfiles.Sync(cmd.Context(), root, cfg, c, action == "push", cmd.OutOrStdout()); err != nil {
				return err
			}
			return projectfiles.WriteInstructions(root)
		}})
	}
	for _, action := range []string{"read", "versions"} {
		action := action
		argc := 1
		use := action + " <collection>"
		if action == "read" {
			argc = 2
			use += " <path>"
		}
		command.AddCommand(&cobra.Command{Use: use, Args: cobra.ExactArgs(argc), RunE: func(cmd *cobra.Command, args []string) error {
			_, cfg, c, err := workspace(opts)
			if err != nil {
				return err
			}
			if err = c.ProjectOrg(cmd.Context(), cfg.ProjectID); err != nil {
				return err
			}
			values := map[string]any{"collection": args[0]}
			endpoint := "versions"
			if action == "read" {
				endpoint = "read_file"
				values["path"] = args[1]
			}
			response, err := c.Service(cmd.Context(), "documents/files/"+endpoint, cfg.ProjectID, values)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			var result any
			if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}})
	}
	return command
}
