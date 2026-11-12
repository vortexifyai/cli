package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/api"
)

func newAnalyzeCommand(opts *options) *cobra.Command {
	var query, queryFile, job string
	var tables []string
	var limit int
	var wait, deployed, asJSON, interactive bool
	cmd := &cobra.Command{Use: "analyze", Short: "Run read-only SQL inline, from a file, or in a SQL prompt", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if job != "" && (cmd.Flags().Changed("query") || cmd.Flags().Changed("query-file") || interactive || len(tables) > 0) {
			return fmt.Errorf("use --job alone to retrieve an analysis")
		}
		mapping := map[string]string{}
		if job == "" {
			if len(tables) == 0 {
				return fmt.Errorf("provide --table alias=table-reference (for example orders=mes.orders)")
			}
			if limit < 1 || limit > 100 {
				return fmt.Errorf("limit must be between 1 and 100")
			}
			for _, table := range tables {
				alias, id, ok := strings.Cut(table, "=")
				if !ok || strings.TrimSpace(alias) == "" || strings.TrimSpace(id) == "" {
					return fmt.Errorf("expected --table alias=table-reference (for example orders=mes.orders)")
				}
				if _, ok := mapping[alias]; ok {
					return fmt.Errorf("duplicate table alias %s", alias)
				}
				mapping[alias] = id
			}
			if cmd.Flags().Changed("query-file") {
				data, err := readInputFile(cmd, queryFile)
				if err != nil {
					return err
				}
				query = string(data)
			}
			if (cmd.Flags().Changed("query") || cmd.Flags().Changed("query-file")) && strings.TrimSpace(query) == "" {
				return fmt.Errorf("SQL query must not be empty")
			}
		}
		_, cfg, c, err := workspace(opts)
		if err != nil {
			return err
		}
		if job != "" {
			result, err := analysisResult(cmd, c, cfg.ProjectID, job)
			if err != nil {
				return err
			}
			if wait {
				if _, err = waitForJobDisplay(cmd, c, cfg.ProjectID, job, 30*time.Minute, asJSON); err != nil {
					return err
				}
				result, err = analysisResult(cmd, c, cfg.ProjectID, job)
				if err != nil {
					return err
				}
			}
			if err = printExecution(cmd.OutOrStdout(), result, asJSON); err != nil {
				return err
			}
			return executionFailure(result)
		}
		available, err := c.ListTables(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		for alias, reference := range mapping {
			table, err := resolveAnalysisTable(available, reference, deployed)
			if err != nil {
				return fmt.Errorf("analysis input %q: %w", alias, err)
			}
			mapping[alias] = table.ID
		}
		execute := func(sql string, waitForResult bool) error {
			response, err := c.Service(cmd.Context(), "data/tables/start_analysis", cfg.ProjectID, map[string]any{"query": sql, "tables": mapping, "limit": limit, "deployed": deployed})
			if err != nil {
				return err
			}
			var result api.Execution
			err = json.NewDecoder(response.Body).Decode(&result)
			response.Body.Close()
			if err != nil {
				return err
			}
			if waitForResult {
				id, _ := result["job_id"].(string)
				if id == "" {
					id, _ = result["id"].(string)
				}
				if id == "" {
					return fmt.Errorf("server returned an empty analysis job ID")
				}
				if _, err = waitForJobDisplay(cmd, c, cfg.ProjectID, id, 30*time.Minute, asJSON); err != nil {
					return err
				}
				result, err = analysisResult(cmd, c, cfg.ProjectID, id)
				if err != nil {
					return err
				}
			}
			if err = printExecution(cmd.OutOrStdout(), result, asJSON); err != nil {
				return err
			}
			return executionFailure(result)
		}
		if query != "" {
			return execute(query, wait)
		}
		return analysisSession(cmd, func(sql string) error { return execute(sql, true) })
	}}
	cmd.Flags().StringVarP(&query, "query", "c", "", "Inline SQL query")
	cmd.Flags().StringVar(&queryFile, "query-file", "", "SQL file, or - to read stdin")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Open a SQL prompt (default when no query is supplied)")
	cmd.MarkFlagsMutuallyExclusive("query", "query-file", "interactive")
	cmd.Flags().StringArrayVar(&tables, "table", nil, "Table alias=reference, e.g. orders=mes.orders (repeat for joins)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum preview rows")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print structured JSON without animation")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for completion (always enabled in the SQL prompt)")
	cmd.Flags().BoolVar(&deployed, "deployed", false, "Use deployed file-backed outputs and origin database tables (default: preview)")
	cmd.Flags().StringVar(&job, "job", "", "Retrieve a completed analysis")
	return cmd
}

// Database table IDs are environment-specific. Select names in the requested
// environment, and reject explicit references to a different environment.
func resolveAnalysisTable(tables []api.TableSummary, reference string, deployed bool) (*api.TableSummary, error) {
	environment := "preview"
	if deployed {
		environment = "origin"
	}
	selected, err := resolveTable(tables, reference)
	if err == nil {
		if selected.Type == "database" && selected.Environment != environment {
			return nil, fmt.Errorf("database table %q belongs to %s; analysis selects %s (use --deployed for origin or select a preview table)", reference, selected.Environment, environment)
		}
		return selected, nil
	}
	eligible := []api.TableSummary{}
	for _, table := range tables {
		if table.Type != "database" || table.Environment == environment {
			eligible = append(eligible, table)
		}
	}
	return resolveTable(eligible, reference)
}

func analysisResult(cmd *cobra.Command, c *api.Client, projectID, id string) (api.Execution, error) {
	response, err := c.Service(cmd.Context(), "data/tables/get_analysis", projectID, map[string]any{"job_id": id})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result api.Execution
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}
