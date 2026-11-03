package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/projectfiles"
	workspacefs "vortexify-cli/internal/workspace"
)

func newInitCommand(opts *options) *cobra.Command {
	var id, name, description string
	command := &cobra.Command{Use: "init <project-name>", Short: "Create a named project folder and select its organization", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			if cmd.Flags().Changed("name") {
				return fmt.Errorf("provide the project name as an argument or --name, not both")
			}
			name = args[0]
		}
		if name == "" {
			return fmt.Errorf("project name is required: use vortexify init <project-name>")
		}
		if strings.TrimSpace(name) != name || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\:\x00\r\n\t") || filepath.IsAbs(name) {
			return fmt.Errorf("project name must be a single non-hidden directory name, not a path")
		}
		parent, err := os.Getwd()
		if err != nil {
			return err
		}
		root := filepath.Join(parent, name)
		if _, err = os.Lstat(root); err == nil {
			return fmt.Errorf("project directory %s already exists; choose a new project name", root)
		} else if !os.IsNotExist(err) {
			return err
		}
		if ancestor, err := config.FindProjectRootFrom(parent); err == nil {
			return fmt.Errorf("already inside project %s; nested projects are unsupported", ancestor)
		}
		client := api.NewClient(opts.apiURL)
		if err := selectOrganization(cmd, client, opts.orgID); err != nil {
			return err
		}
		// Reserve the destination before creating a remote project. Never reuse an
		// existing directory, even an empty one or a symlink.
		if err := os.Mkdir(root, 0755); err != nil {
			return err
		}
		defer func() { _ = os.Remove(root) }() // Removes only an empty folder on failure.
		var project *api.Project
		if id != "" {
			project, err = client.GetProject(cmd.Context(), id)
		} else {
			project, err = client.CreateProject(cmd.Context(), name, description)
		}
		if err != nil {
			return err
		}
		if project.ID == "" {
			return fmt.Errorf("server returned an empty project ID")
		}
		if id != "" && project.ID != id {
			return fmt.Errorf("server returned a different project ID")
		}
		if project.OrgID != "" && project.OrgID != client.OrgID {
			return fmt.Errorf("project does not belong to the selected organization")
		}
		cfg := project.Config
		if cfg == nil {
			cfg = config.Default(project.ID, project.Name, project.Description)
		} else {
			cfg.ProjectID = project.ID
			cfg.Name = project.Name
			cfg.Description = project.Description
		}
		if err = config.Save(root, cfg); err != nil {
			return err
		}
		lock, err := workspacefs.Acquire(root)
		if err != nil {
			return err
		}
		defer lock.Close()
		if project.OrgID != "" {
			client.OrgID = project.OrgID
		}
		for _, name := range projectfiles.Names {
			if err = client.InitializeCollection(cmd.Context(), cfg.ProjectID, name); err != nil {
				return err
			}
		}
		if _, err = projectfiles.Sync(cmd.Context(), root, cfg, client, false, cmd.OutOrStdout()); err != nil {
			return err
		}
		if err = projectfiles.WriteInstructions(root); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s (%s) in %s.\n", project.Name, project.ID, root)
		fmt.Fprintf(cmd.OutOrStdout(), "Next: cd '%s'\n", strings.ReplaceAll(name, "'", "'\\''"))
		return nil
	}}
	command.Flags().StringVar(&id, "project-id", "", "Attach an existing project in the new named folder")
	command.Flags().StringVar(&name, "name", "", "Explicit project/folder name (alternative to the positional argument)")
	command.Flags().StringVar(&description, "description", "", "New project description")
	return command
}
