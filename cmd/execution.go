package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"strings"
	"time"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/resources"
)

func executionCommands(opts *options) []*cobra.Command {
	result := []*cobra.Command{}
	for _, action := range []string{"run", "test", "build", "deploy", "upgrade", "undeploy"} {
		result = append(result, newExecutionCommand(opts, action))
	}
	result = append(result, newJobCommand(opts, false), newJobCommand(opts, true))
	return result
}
func newExecutionCommand(opts *options, action string) *cobra.Command {
	var version, buildMemory int
	var inputsFile, inlineInputs, subdomain string
	var wait, asJSON, downstream, preview, production, deployRun bool
	var timeout time.Duration
	command := &cobra.Command{Use: action + " <type> <name-or-id>", Short: executionDescription(action), Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if production && ((args[0] != "transform" && args[0] != "sync") || preview || downstream) {
			return fmt.Errorf("--production requires a sync or transform run without --preview or --rebuild-downstream")
		}
		if deployRun && args[0] != "sync" && args[0] != "transform" {
			return fmt.Errorf("--run requires a sync or transform deployment")
		}
		if cmd.Flags().Changed("build-memory") && (args[0] != "app" || buildMemory < 1 || buildMemory > 8192) {
			return fmt.Errorf("--build-memory requires an app and a value from 1 to 8192 MiB")
		}
		if preview && (args[0] != "sync" || downstream || cmd.Flags().Changed("inputs") || cmd.Flags().Changed("inputs-file")) {
			return fmt.Errorf("--preview requires a sync and cannot be combined with runtime inputs or --rebuild-downstream")
		}
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		allowed := map[string]bool{"api": true, "transform": true, "app": true, "sync": true}
		if action == "deploy" || action == "upgrade" || action == "undeploy" {
			allowed["agent"], allowed["connector"] = true, true
		}
		if !allowed[args[0]] {
			return fmt.Errorf("unsupported resource type %q", args[0])
		}
		rows, err := client.ListRegisteredResources(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		selection := resources.Filter{Type: args[0], Resource: args[1]}
		entries := []resources.RegistryEntry{}
		for _, row := range rows {
			entries = append(entries, resources.RegistryEntry{ID: row.ID, Type: row.Type, Name: row.Name})
		}
		if err := selection.CheckSelection(entries); err != nil {
			return err
		}
		id, selectedName := "", ""
		for _, row := range rows {
			if selection.Match(row.Type, row.Name, row.ID) {
				if id != "" {
					return fmt.Errorf("ambiguous resource")
				}
				id = row.ID
				selectedName = row.Name
			}
		}
		if id == "" {
			return fmt.Errorf("resource is not registered; create and commit it first")
		}

		values := map[string]any{"action": action, "resource_type": args[0], "resource_id": id}
		if deployRun {
			values["run"] = true
		}
		if production {
			values["production"] = true
		}
		if preview {
			values["action"] = "preview"
		}
		if cmd.Flags().Changed("subdomain") {
			if args[0] != "app" || strings.TrimSpace(subdomain) == "" {
				return fmt.Errorf("subdomain requires an app and a nonempty subdomain label")
			}
			values["subdomain"] = subdomain
		}
		if version > 0 {
			values["version"] = version
		} else if cmd.Flags().Changed("version") {
			return fmt.Errorf("version must be positive")
		}
		if cmd.Flags().Changed("inputs-file") || cmd.Flags().Changed("inputs") {
			data := []byte(inlineInputs)
			if cmd.Flags().Changed("inputs-file") {
				data, err = readInputFile(cmd, inputsFile)
				if err != nil {
					return err
				}
			}
			var inputs map[string]any
			if err = json.Unmarshal(data, &inputs); err != nil || inputs == nil {
				return fmt.Errorf("runtime inputs must contain a JSON object")
			}
			values["inputs"] = inputs
		}
		if downstream {
			values["rebuild_downstream"] = true
		}
		if cmd.Flags().Changed("build-memory") {
			if _, err := client.Service(cmd.Context(), "artifacts/apps/set_build_memory", cfg.ProjectID,
				map[string]any{"app_id": id, "memory_mib": buildMemory}); err != nil {
				return err
			}
		}
		var result api.Execution
		if action == "deploy" || action == "upgrade" || action == "undeploy" {
			result, err = client.DeployResource(cmd.Context(), cfg.ProjectID, values)
		} else {
			result, err = client.StartResourceJob(cmd.Context(), cfg.ProjectID, values)
		}
		if err != nil {
			return err
		}
		if result["resource_name"] == nil {
			result["resource_name"] = selectedName
		}
		id, _ = result["job_id"].(string)
		if wait && id != "" {
			result, err = waitForJobDisplay(cmd, client, cfg.ProjectID, id, timeout, asJSON)
			if err != nil {
				return err
			}
		}
		if err = printExecution(cmd.OutOrStdout(), result, asJSON); err != nil {
			return err
		}
		return executionFailure(result)
	}}
	command.Flags().BoolVar(&wait, "wait", false, "Wait for execution and selected downstream work")
	command.Flags().BoolVar(&asJSON, "json", false, "Print structured JSON")
	command.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Maximum polling duration; does not cancel jobs")
	if action != "upgrade" && action != "undeploy" {
		command.Flags().IntVar(&version, "version", 0, "Committed version (production defaults to deployed; development/deploy to latest)")
	}
	if action == "deploy" || action == "upgrade" {
		command.Flags().BoolVar(&deployRun, "run", false, "Run the sync or transform in production and activate after output publication")
		command.Flags().StringVar(&subdomain, "subdomain", "", "App hostname label, e.g. orders for orders.vortexify.dev on the dev stack")
	}
	if action == "build" || action == "deploy" || action == "upgrade" {
		command.Flags().IntVar(&buildMemory, "build-memory", 0, "App build memory in MiB (1–8192); saves the setting for subsequent builds")
	}
	if action == "run" {
		command.Flags().BoolVar(&production, "production", false, "Run the deployed sync or transform against production inputs and origin databases")
		command.Flags().BoolVar(&preview, "preview", false, "Sample a sync and estimate row size without publishing data or advancing checkpoints")
	}
	if action == "run" || action == "deploy" || action == "upgrade" {
		command.Flags().StringVar(&inlineInputs, "inputs", "", "Inline JSON object containing runtime inputs")
		command.Flags().StringVar(&inputsFile, "inputs-file", "", "Runtime inputs JSON file, or - to read stdin")
		command.MarkFlagsMutuallyExclusive("inputs", "inputs-file")
	}
	if action == "run" || action == "build" {
		command.Flags().BoolVar(&downstream, "rebuild-downstream", false, "Rebuild affected downstream previews in upstream-first order")
	}
	return command
}
func executionDescription(action string) string {
	switch action {
	case "run":
		return "Run committed code in preview, or a deployed sync/transform with --production"
	case "test":
		return "Run SDK tests against a committed API or transform"
	case "build":
		return "Build an app artifact or prepare a function preview runtime"
	case "deploy":
		return "Deploy a committed version; --run executes syncs/transforms before activation"
	case "upgrade":
		return "Deploy the latest committed version, pinned when requested"
	default:
		return "Disable a resource deployment"
	}
}
func executionFailure(result api.Execution) error {
	if result["status"] == "failed" || result["status"] == "stopped" || result["downstream_status"] == "failed" {
		return fmt.Errorf("execution did not complete successfully; inspect job results and logs")
	}
	return nil
}
func newJobCommand(opts *options, logs bool) *cobra.Command {
	var wait, asJSON bool
	var timeout time.Duration
	name := "job"
	if logs {
		name = "logs"
	}
	command := &cobra.Command{Use: name + " <job-id>", Short: "Retrieve job status, results, validation, or build logs", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		var result api.Execution
		if wait {
			result, err = waitForJobDisplay(cmd, client, cfg.ProjectID, args[0], timeout, asJSON)
		} else {
			result, err = client.GetJobResults(cmd.Context(), cfg.ProjectID, args[0])
		}
		if err != nil {
			return err
		}
		if !logs {
			if err = printExecution(cmd.OutOrStdout(), result, asJSON); err != nil {
				return err
			}
			return executionFailure(result)
		}
		collected := []api.Execution{}
		seen := map[string]bool{}
		var collect func(api.Execution) error
		collect = func(row api.Execution) error {
			id, _ := row["job_id"].(string)
			if seen[id] {
				return nil
			}
			seen[id] = true
			if len(seen) > 100 {
				return fmt.Errorf("too many related jobs; inspect child jobs directly")
			}
			collected = append(collected, api.Execution{"job_id": id, "logs": row["logs"],
				"logs_error": row["logs_error"], "failure_message": row["failure_message"],
				"failure_reason_code": row["failure_reason_code"], "error": row["error"]})
			ids := []string{}
			for _, key := range []string{"build_job_id", "shell_build_job_id", "dependency_job_id"} {
				if child, ok := row[key].(string); ok && child != "" {
					ids = append(ids, child)
				}
			}
			for _, key := range []string{"downstream_job_ids", "schema_discovery_job_ids"} {
				if children, ok := row[key].([]any); ok {
					for _, child := range children {
						if value, ok := child.(string); ok {
							ids = append(ids, value)
						}
					}
				}
			}
			for _, id := range ids {
				if seen[id] {
					continue
				}
				child, err := client.GetJobResults(cmd.Context(), cfg.ProjectID, id)
				if err != nil {
					return err
				}
				if err = collect(child); err != nil {
					return err
				}
			}
			return nil
		}
		if err = collect(result); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(collected)
		}
		for _, row := range collected {
			text, _ := row["logs"].(string)
			fmt.Fprintf(cmd.OutOrStdout(), "Job %v\n", row["job_id"])
			if reason := textValue(row["logs_error"]); reason != "" {
				cause := textValue(row["failure_message"])
				if cause == "" {
					cause = textValue(row["error"])
				}
				if cause != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Failure: %s\n", cause)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Logs unavailable: %s\n", reason)
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(text, "\n"))
		}
		return nil
	}}
	command.Flags().BoolVar(&asJSON, "json", false, "Print structured JSON")
	command.Flags().BoolVar(&wait, "wait", false, "Wait for execution and selected downstream work")
	command.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Maximum polling duration")
	return command
}
