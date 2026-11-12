package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"strings"
)

func newDatabaseCommand(opts *options) *cobra.Command {
	var approve, reject, migrate int
	var environment string
	var asJSON bool
	command := &cobra.Command{Use: "database <name-or-id>", Short: "Inspect database migrations and backup jobs, or approve/reject a saved version", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		deciding := cmd.Flags().Changed("approve") || cmd.Flags().Changed("reject")
		if environment != "origin" && environment != "preview" {
			return fmt.Errorf("environment must be origin or preview")
		}
		if cmd.Flags().Changed("migrate") && migrate <= 0 {
			return fmt.Errorf("select a positive saved version")
		}
		if deciding && approve <= 0 && reject <= 0 {
			return fmt.Errorf("select a positive saved version")
		}
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		rows, err := client.GetResourceGraph(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		id := ""
		for _, row := range rows {
			if row.Type == "database" && (row.ID == args[0] || row.Name == args[0]) {
				if id != "" {
					return fmt.Errorf("ambiguous database; use its ID")
				}
				id = row.ID
			}
		}
		if id == "" {
			return fmt.Errorf("database is not registered; commit it first")
		}
		out := cmd.OutOrStdout()
		if cmd.Flags().Changed("migrate") {
			result, err := client.MigrateDatabase(cmd.Context(), cfg.ProjectID, id, migrate, environment)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(out).Encode(result)
			}
			if job, ok := result.(map[string]any); ok && job["id"] != nil {
				fmt.Fprintf(out, "Migration submitted for %s version %d (%s).\n", args[0], migrate, environment)
				fmt.Fprintf(out, "Migration job: %v\nStatus: %v\n", job["id"], job["status"])
			} else {
				fmt.Fprintln(out, "Schema version is already applied.")
			}
			return nil
		}
		if deciding {
			version := reject
			if approve > 0 {
				version = approve
			}
			result, err := client.DecideDatabaseMigration(cmd.Context(), cfg.ProjectID, id, version, environment, approve > 0)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(out).Encode(result)
			}
			decision := "Rejected"
			if result.Approved {
				decision = "Approved"
			}
			fmt.Fprintf(out, "%s %s version %d (%s).\n", decision, args[0], result.Version, result.Environment)
			if job, ok := result.MigrationResult.(map[string]any); ok && job["id"] != nil {
				fmt.Fprintf(out, "Migration job: %v\nStatus: %v\n", job["id"], job["status"])
			}
			return nil
		}
		status, err := client.GetDatabaseStatus(cmd.Context(), cfg.ProjectID, id)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(out).Encode(status)
		}
		versionLabel := "uncommitted"
		if status.Version > 0 {
			versionLabel = fmt.Sprint(status.Version)
		}
		fmt.Fprintf(out, "%s\n  ID: %s\n  Version: %s\n  Migration approval required: %t\n", status.Name, status.ID, versionLabel, status.ApprovalRequired)
		for _, env := range status.Environments {
			if env.Version > 0 {
				fmt.Fprintf(out, "  Environment: %s (schema version %d)\n", env.Environment, env.Version)
			} else {
				fmt.Fprintf(out, "  Environment: %s (schema not migrated)\n", env.Environment)
			}
		}
		if len(status.Pending) == 0 {
			fmt.Fprintln(out, "  Pending approvals: none")
		}
		for _, migration := range status.Pending {
			fmt.Fprintf(out, "  Pending approval: version %d (%s)\n    SQL: %s\n", migration.Version, migration.Environment, strings.Join(migration.MigrationPaths, ", "))
		}
		for _, job := range status.LatestMigrations {
			fmt.Fprintf(out, "  Latest migration: %s (version %d, %s)\n  Migration job: %s\n", job.Status, job.Version, job.Environment, job.ID)
			if job.Error != "" {
				fmt.Fprintf(out, "  Error: %s\n", job.Error)
			}
			if job.Status == "failed" || job.Status == "stopped" {
				fmt.Fprintf(out, "  Logs: vortexify logs %s\n", job.ID)
				if job.Environment != "" {
					fmt.Fprintf(out, "  Retry: vortexify database %s --migrate %d --environment %s\n", args[0], job.Version, job.Environment)
				}
			}
		}
		if len(status.LatestBackups) == 0 {
			fmt.Fprintln(out, "  Backups: no jobs yet")
		} else {
			fmt.Fprintln(out, "\nBackups (latest job per environment)")
			for _, job := range status.LatestBackups {
				fmt.Fprintf(out, "  %s  %s\n    Backup job: %s\n", job.Environment, job.Status, job.ID)
			}
		}
		return nil
	}}
	command.Flags().IntVar(&approve, "approve", 0, "Approve and apply a saved database version")
	command.Flags().IntVar(&reject, "reject", 0, "Reject a saved database version without applying it")
	command.Flags().IntVar(&migrate, "migrate", 0, "Apply a saved schema version; provisions preview when missing")
	command.MarkFlagsMutuallyExclusive("approve", "reject", "migrate")
	command.Flags().StringVar(&environment, "environment", "origin", "Target environment for an approval decision")
	command.Flags().BoolVar(&asJSON, "json", false, "Print structured database status or decision")
	return command
}
