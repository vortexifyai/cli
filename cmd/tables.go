package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/api"
)

func tableLabel(table api.TableSummary) string {
	if table.DisplayName != "" {
		return displayTableName(table.DisplayName, table.Type)
	}
	if table.Name != "" {
		label := displayTableName(table.Name, table.Type)
		if table.Type == "database" && table.Environment != "" {
			label += " [" + table.Environment + "]"
		}
		return label
	}
	return "unavailable table (" + table.ID + ")"
}

func displayTableName(name, kind string) string {
	if kind == "sync" || kind == "transform" || kind == "" {
		return strings.TrimSuffix(name, "/default")
	}
	return name
}

func newTablesCommand(opts *options) *cobra.Command {
	var name string
	var asJSON bool
	command := &cobra.Command{Use: "tables", Short: "List project tables and schema discovery status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		tables, err := client.ListTables(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		selected := []api.TableSummary{}
		for _, table := range tables {
			if name == "" || table.ID == name || tableMatches(table, name) {
				selected = append(selected, table)
			}
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(selected)
		}
		if len(selected) == 0 {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "No matching project tables.")
			return err
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tTYPE\tDATA\tSCHEMA\tKNOWLEDGE GRAPH\tPRIMARY KEYS\tRELATIONSHIPS\tID\tREFERENCE")
		for _, table := range selected {
			graph, keys, relationships := tableGraphStatuses(table.KnowledgeGraph)
			data, schema := tableDataSchemaStatuses(table)
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", tableLabel(table), table.Type, data, schema, graph, keys, relationships, table.ID, table.ResourceReference)
		}
		return writer.Flush()
	}}
	command.Long = "List published project tables with data readiness, schema discovery progress, and knowledge graph, primary-key and relationship analysis status.\n\nUse vortexify table <table-reference> to inspect discovered keys, row grain and relationships."
	command.Example = "  vortexify tables\n  vortexify tables --name postgres.mes_items\n  vortexify tables --json"
	command.Flags().StringVar(&name, "name", "", "Filter by table name or ID")
	command.Flags().BoolVar(&asJSON, "json", false, "Print structured JSON")
	command.AddCommand(&cobra.Command{Use: "delete <table-name>", Short: "Soft-delete an uploaded table from the project", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		tables, err := client.ListTables(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		table, err := resolveTable(tables, args[0])
		if err != nil {
			return err
		}
		if table.Type != "upload" {
			return fmt.Errorf("only uploaded tables can be deleted with this command")
		}
		if err = client.DeleteUploadedTable(cmd.Context(), cfg.ProjectID, table.ID); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted uploaded table %s.\n", tableLabel(*table))
		return err
	}})
	return command
}

func tableDataSchemaStatuses(table api.TableSummary) (string, string) {
	if table.Type == "database" {
		return table.Status, "not applicable"
	}
	status := table.Status
	data := "not ready"
	if status == "ready" {
		return "ready", "complete"
	}
	if strings.HasPrefix(status, "ready; ") {
		data, status = "ready", strings.TrimPrefix(status, "ready; ")
	}
	switch status {
	case "schema pending":
		return data, "pending"
	case "discovering schema":
		return data, "discovering"
	case "schema retrying":
		return data, "retrying"
	case "schema failed":
		return data, "failed"
	case "schema stopped":
		return data, "stopped"
	case "publishing":
		return data, "publishing"
	default:
		return table.Status, "unknown"
	}
}

func tableGraphStatuses(graph *api.TableKnowledgeGraph) (string, string, string) {
	if graph == nil || graph.Status == "not_applicable" {
		return "not applicable", "not applicable", "not applicable"
	}
	status := strings.ReplaceAll(graph.Status, "_", " ")
	keys, relationships := "pending", "pending"
	if graph.Status == "not_started" {
		return status, "not started", "not started"
	}
	if graph.KeysComplete {
		keys = "ready"
	} else if graph.Status == "failed" || graph.Status == "stopped" {
		keys = graph.Status
	}
	if graph.Phase == "primary_keys" && (graph.Status == "running" || graph.Status == "failed" || graph.Status == "stopped") {
		keys = graph.Status
	}
	if graph.Phase == "retrying" && !graph.KeysComplete {
		keys = "retrying"
	}
	if graph.Status == "completed" && graph.Relationships["pending"] == 0 && graph.Relationships["running"] == 0 && graph.Relationships["failed"] == 0 && len(graph.WaitingForTableIDs) == 0 {
		relationships = "ready"
	} else if graph.Relationships["failed"] > 0 || (graph.KeysComplete && graph.Status == "failed") {
		relationships = "failed"
	} else if graph.Relationships["running"] > 0 || (graph.Phase == "relationships" && graph.Status == "running") {
		relationships = "running"
	} else if graph.Phase == "relationships" && graph.Status == "stopped" {
		relationships = "stopped"
	}
	return status, keys, relationships
}

func newTableCommand(opts *options) *cobra.Command {
	var verbose bool
	var deployed bool
	var jobID string
	var asJSON bool
	command := &cobra.Command{Use: "table <table-reference>", Short: "Inspect a table's discovered keys and relationships", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		tables, err := client.ListTables(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		selected, err := resolveTable(tables, args[0])
		if err != nil {
			return err
		}
		names := map[string]string{}
		for _, table := range tables {
			names[table.ID] = tableLabel(table)
		}
		details := &api.TableDetails{Table: *selected}
		if deployed || jobID != "" || strings.HasPrefix(selected.Status, "ready") || selected.Type == "database" {
			selector := map[string]any{"deployed": deployed}
			if jobID != "" {
				selector["job_id"] = jobID
			}
			details, err = client.GetTableDetails(cmd.Context(), cfg.ProjectID, selected.ID, selector)
			if err != nil {
				return err
			}
			// New servers return the selected snapshot's status and discovery.
			if details.Table.Status == "" {
				details.Table.Status = selected.Status
			}
			if details.Table.KnowledgeGraph == nil {
				details.Table.KnowledgeGraph = selected.KnowledgeGraph
			}
			selected = &details.Table
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(details)
		}
		return printTableDetails(cmd.OutOrStdout(), details, names, verbose)
	}}
	command.Long = "Inspect a table's knowledge graph findings: candidate keys and uniqueness, row grain, and relationships with other tables, including join columns and cardinality.\n\nShows data and schema readiness, analysis errors, and associated source, schema-discovery and knowledge-graph jobs together. Uses the latest published output by default; select a producer job with --job or deployed output with --deployed. Read an associated job's logs with vortexify logs <job-id>. Use --verbose for full readable explanations, or --json for the complete structured response, including the schema."
	command.Example = "  vortexify table postgres.mes_items\n  vortexify table postgres.mes_items --verbose\n  vortexify table postgres.mes_work_orders --json\n  vortexify table postgres.mes_work_orders --job <job-id>\n  vortexify table postgres.mes_work_orders --deployed\n  vortexify logs <job-id>"
	command.Flags().BoolVar(&deployed, "deployed", false, "Inspect the deployed producer version's latest output")
	command.Flags().StringVar(&jobID, "job", "", "Inspect the output of a specific producer job")
	command.MarkFlagsMutuallyExclusive("deployed", "job")
	command.Flags().BoolVar(&verbose, "verbose", false, "Show grain, duplicate explanations, matching logic and evaluation reasons")
	command.Flags().BoolVar(&asJSON, "json", false, "Print findings and execution metadata as JSON")
	return command
}

func printTableJobs(out io.Writer, details *api.TableDetails) error {
	source, _ := details.Publication["source_job_id"].(string)
	schema, _ := details.Publication["discovery_job_id"].(string)
	if schema == "" {
		schema = details.Table.DiscoveryJobID
	}
	graph := ""
	if details.Table.KnowledgeGraph != nil {
		graph = details.Table.KnowledgeGraph.JobID
	}
	if source == "" && schema == "" && graph == "" {
		return nil
	}
	if _, err := fmt.Fprintln(out, "\nJobs"); err != nil {
		return err
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, job := range []struct{ label, id string }{
		{"Source run", source}, {"Schema discovery", schema}, {"Knowledge graph", graph},
	} {
		if job.id != "" {
			if _, err := fmt.Fprintf(writer, "  %s\t%s\n", job.label, job.id); err != nil {
				return err
			}
		}
	}
	return writer.Flush()
}
