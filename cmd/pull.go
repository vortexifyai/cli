package cmd

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
	workspacefs "vortexify-cli/internal/workspace"

	"vortexify-cli/internal/pull"
)

func newPullCommand(opts *options) *cobra.Command {
	filter := pull.Filter{}
	var projectID string
	command := &cobra.Command{Use: "pull", Short: "Download registered resources without overwriting local changes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := filter.Validate(); err != nil {
			return err
		}
		root, err := config.FindProjectRoot()
		var cfg *config.ProjectConfig
		client := api.NewClient(opts.apiURL)
		client.OrgID = opts.orgID
		if errors.Is(err, config.ErrProjectNotFound) {
			if strings.TrimSpace(projectID) == "" {
				return fmt.Errorf("no %s found; use --project-id for the first pull", config.ConfigFileName)
			}
			root, err = os.Getwd()
			if err != nil {
				return err
			}
			project, err := client.GetProject(cmd.Context(), projectID)
			if err != nil {
				return err
			}
			if project.ID != projectID {
				return fmt.Errorf("server returned a different project ID")
			}
			lock, err := workspacefs.Acquire(root)
			if err != nil {
				return err
			}
			err = func() error {
				if _, err := os.Lstat(filepath.Join(root, config.ConfigFileName)); err == nil {
					cfg, err = config.Load(root)
					return err
				} else if !os.IsNotExist(err) {
					return err
				}
				cfg = config.Default(projectID, project.Name, project.Description)
				return config.Save(root, cfg)
			}()
			lock.Close()
			if err != nil {
				return err
			}
		} else {
			if err != nil {
				return err
			}
			cfg, err = config.Load(root)
			if err != nil {
				return err
			}
		}
		if projectID != "" && cfg.ProjectID != projectID {
			return fmt.Errorf("--project-id does not match %s; use another directory", config.ConfigFileName)
		}
		service := pull.Service{Client: client, Output: cmd.OutOrStdout()}
		return service.Run(cmd.Context(), root, cfg, filter)
	}}
	command.Flags().StringVar(&projectID, "project-id", "", "Project to pull; required when no project YAML exists")
	command.Flags().StringVar(&filter.Type, "type", "", "Filter by resource type")
	command.Flags().StringVar(&filter.Resource, "resource", "", "Filter by resource name or ID")
	command.Flags().BoolVar(&filter.IncludeDependencies, "include-dependencies", false, "Include transitive dependencies of --resource")
	command.Flags().BoolVar(&filter.IncludeDependents, "include-dependents", false, "Include transitive dependents of --resource")
	return command
}
