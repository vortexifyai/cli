package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"strings"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/output"
	"vortexify-cli/internal/resources"
)

func newRegistryCommand(opts *options) *cobra.Command {
	var resourceType, name string
	var asJSON bool
	command := &cobra.Command{Use: "registry", Short: "List registered project resources", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		filter := resources.Filter{Type: resourceType, Resource: name}
		if err = filter.Validate(); err != nil {
			return err
		}
		registry, err := client.ListResources(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		entries := []resources.RegistryEntry{}
		for _, row := range registry {
			entries = append(entries, resources.RegistryEntry{ID: row.ID, Type: row.Type, Name: row.Name})
		}
		if err := filter.CheckSelection(entries); err != nil {
			return err
		}
		selected := []api.Resource{}
		for _, r := range registry {
			if filter.Match(r.Type, r.Name, r.ID) {
				if r.Type == "api" || r.Type == "transform" {
					environment, lookupErr := client.GetFunctionEnvironment(cmd.Context(), cfg.ProjectID, r.ID, r.Version)
					if lookupErr != nil {
						environment = &api.Environment{Status: "unavailable", Error: lookupErr.Error()}
						fmt.Fprintf(cmd.ErrOrStderr(), "%s:%s: environment check failed: %v\n", r.Type, r.Name, lookupErr)
					}
					r.Environment = environment
				}
				selected = append(selected, r)
			}
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(selected)
		}
		if err := output.PrintRegistry(cmd.OutOrStdout(), selected); err != nil {
			return err
		}
		if name != "" && len(selected) == 1 && selected[0].Version > 0 && selected[0].Environment != nil {
			environment := selected[0].Environment
			fmt.Fprintf(cmd.OutOrStdout(), "\nEnvironment for committed version %d\n", environment.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "  Image type: %s\n", environment.ImageKind)
			if environment.LambdaName != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  Lambda: %s\n", environment.LambdaName)
			}
			if len(environment.Compute) > 0 {
				compute, err := json.Marshal(environment.Compute)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  Compute: %s\n", compute)
			}
			if len(environment.Requirements) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "  Requirements: %s\n", strings.Join(environment.Requirements, ", "))
			}
		}
		return nil
	}}
	command.Flags().StringVar(&resourceType, "type", "", "Filter by resource type")
	command.Flags().StringVar(&name, "name", "", "Filter by name or resource ID")
	command.Flags().BoolVar(&asJSON, "json", false, "Print registry and environment details as JSON")
	return command
}
