package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"vortexify-cli/internal/dev"
	"vortexify-cli/internal/state"
)

func newDevCommand(opts *options) *cobra.Command {
	var port int
	command := &cobra.Command{Use: "dev [app]", Short: "Start a Vite app with an authenticated local API proxy", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		root, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		selection := ""
		if len(args) == 1 {
			selection = args[0]
		}
		appPath, err := dev.FindApp(root, cfg, selection, cwd)
		if err != nil {
			return err
		}
		st, err := state.Load(root)
		if err != nil {
			return err
		}
		// Resolve ownership from the project, independently of any global org selection.
		client.OrgID = ""
		project, err := client.GetProject(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		if project.ID != cfg.ProjectID || project.OrgID == "" {
			return fmt.Errorf("project lookup returned missing or inconsistent organization context")
		}
		appID := st.Resources[state.ResourceKey("app", filepath.Base(appPath))].ResourceID
		return dev.Run(cmd.Context(), dev.Options{AppPath: appPath, ProjectID: cfg.ProjectID, OrgID: project.OrgID, AppID: appID, APIURL: opts.apiURL, Port: port, TryNextPort: !cmd.Flags().Changed("port"), Output: cmd.OutOrStdout(), Errors: cmd.ErrOrStderr()})
	}}
	command.Flags().IntVar(&port, "port", 5173, "Local app port (default tries 5173 upward; explicit ports are fixed; 0 chooses a free port)")
	return command
}
