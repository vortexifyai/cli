package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
)

var version = "dev"

type options struct {
	apiURL string
	orgID  string
}

func NewRootCommand() *cobra.Command {
	opts := &options{apiURL: os.Getenv("VORTEXIFY_API_URL")}
	if opts.apiURL == "" {
		opts.apiURL = "https://api.vortexify.ai"
	}
	root := &cobra.Command{Use: "vortexify", Short: "Synchronize Vortexify project resources", Version: version, SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&opts.apiURL, "api-url", opts.apiURL, "Vortexify API URL")
	root.PersistentFlags().StringVar(&opts.orgID, "org-id", "", "Explicit organization ID (skips organization selection)")
	root.AddCommand(newProjectsCommand(opts), newKnowledgeGraphCommand(opts))
	root.AddCommand(newCreateCommand(opts), newStatusCommand(opts), newLoginCommand(), newInitCommand(opts), newRegistryCommand(opts), newPullCommand(opts), newCommitCommand(opts), newDevCommand(opts), newRecoverCommand(opts))
	root.AddCommand(executionCommands(opts)...)
	root.AddCommand(newSkillsCommand(), newUpdateCommand())
	root.AddCommand(newContextCommand(opts), newBlueprintsCommand(opts), newAnalyzeCommand(opts), newSecretCommand(opts), newConnectorCommand(opts), newDatabaseCommand(opts), newUploadCommand(opts), newFilesCommand(opts), newTablesCommand(opts), newTableCommand(opts))
	root.AddGroup(
		&cobra.Group{ID: "projects", Title: "Project workflow:"},
		&cobra.Group{ID: "data", Title: "Data and connections:"},
		&cobra.Group{ID: "execution", Title: "Execution and apps:"},
		&cobra.Group{ID: "references", Title: "Blueprints and context:"},
		&cobra.Group{ID: "setup", Title: "Setup and help:"},
	)
	groups := map[string][]string{
		"projects":   {"commit", "create", "init", "knowledge-graph", "projects", "pull", "recover", "registry", "status"},
		"data":       {"analyze", "connector", "database", "files", "secret", "table", "tables", "upload"},
		"execution":  {"build", "deploy", "dev", "job", "logs", "run", "test", "undeploy", "upgrade"},
		"references": {"blueprints", "context"},
		"setup":      {"login", "skills", "update"},
	}
	for _, command := range root.Commands() {
		for group, names := range groups {
			for _, name := range names {
				if command.Name() == name {
					command.GroupID = group
				}
			}
		}
	}
	root.SetHelpCommandGroupID("setup")
	root.SetCompletionCommandGroupID("setup")
	return root
}
func Execute() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := NewRootCommand().ExecuteContext(ctx); err != nil {
		var reported *reportedError
		if !errors.As(err, &reported) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

// reportedError preserves failure status when the command has already shown the problem.
type reportedError struct{ error }

func workspace(opts *options) (string, *config.ProjectConfig, *api.Client, error) {
	root, err := config.FindProjectRoot()
	if err != nil {
		return "", nil, nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return "", nil, nil, err
	}
	client := api.NewClient(opts.apiURL)
	client.OrgID = opts.orgID
	return root, cfg, client, nil
}
