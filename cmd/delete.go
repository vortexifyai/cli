package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/resources"
)

func newDeleteCommand(opts *options) *cobra.Command {
	var yes, asJSON bool
	command := &cobra.Command{
		Use:   "delete <type> <name-or-id>",
		Short: "Soft-delete a registered resource or uploaded table",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, reference := args[0], args[1]
			if kind != "table" {
				if err := (resources.Filter{Type: kind}).Validate(); err != nil {
					return err
				}
			}
			if strings.TrimSpace(kind) == "" || strings.TrimSpace(reference) == "" {
				return fmt.Errorf("resource type and name, ID, or reference are required")
			}
			_, cfg, client, err := workspace(opts)
			if err != nil {
				return err
			}
			id, name := "", ""
			if kind == "table" {
				tables, err := client.ListTables(cmd.Context(), cfg.ProjectID)
				if err != nil {
					return err
				}
				table, err := resolveTable(tables, reference)
				if err != nil {
					return err
				}
				if table.Type != "upload" {
					return fmt.Errorf("only uploaded tables can be deleted directly with this command")
				}
				id, name = table.ID, tableLabel(*table)
			} else {
				rows, err := client.ListRegisteredResources(cmd.Context(), cfg.ProjectID)
				if err != nil {
					return err
				}
				selection := resources.Filter{Type: kind, Resource: reference}
				entries := make([]resources.RegistryEntry, 0, len(rows))
				for _, row := range rows {
					entries = append(entries, resources.RegistryEntry{ID: row.ID, Type: row.Type, Name: row.Name})
				}
				if err := selection.CheckSelection(entries); err != nil {
					return err
				}
				for _, row := range rows {
					matches := selection.Match(row.Type, row.Name, row.ID)
					if strings.HasPrefix(reference, "vortexify://") {
						matches = row.Type == kind && row.ResourceReference == reference
					}
					if matches {
						if id != "" {
							return fmt.Errorf("ambiguous resource %q; use its ID or canonical reference", reference)
						}
						id, name = row.ID, row.Name
					}
				}
				if id == "" {
					return fmt.Errorf("%s %q is not registered in this project", kind, reference)
				}
			}
			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(), "Delete %s %s (%s) from project %s? [y/N]: ", kind, name, id, cfg.ProjectID)
				scanner := bufio.NewScanner(cmd.InOrStdin())
				if !scanner.Scan() {
					if err := scanner.Err(); err != nil {
						return err
					}
					return fmt.Errorf("confirmation required; use --yes for noninteractive deletion")
				}
				answer := strings.TrimSpace(scanner.Text())
				if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
					return fmt.Errorf("deletion canceled")
				}
			}
			result, err := client.DeleteResource(cmd.Context(), cfg.ProjectID, kind, id)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s %s.\n", kind, name)
			return err
		},
	}
	command.Long = "Soft-delete an API, transform, app, agent, connector, sync, database, or uploaded table from the current project. Select by name, ID, or canonical resource reference; qualify sync names as connector/sync.\n\nDeployed resources must be undeployed first. This command does not undeploy resources or remove local source files."
	command.Example = "  vortexify delete api orders\n  vortexify delete sync shop/orders\n  vortexify delete database crm --yes\n  vortexify delete table customers --yes --json"
	command.Flags().BoolVar(&yes, "yes", false, "Skip the deletion confirmation")
	command.Flags().BoolVar(&asJSON, "json", false, "Print the server's deletion result as JSON")
	return command
}
