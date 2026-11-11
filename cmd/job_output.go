package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"vortexify-cli/internal/api"
)

func jobDuration(value any) string {
	seconds, ok := value.(float64)
	if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return ""
	}
	if seconds > 0 && seconds < 1 {
		return "<1s"
	}
	return (time.Duration(seconds * float64(time.Second))).Round(time.Second).String()
}

func jobDurationSuffix(value any) string {
	if duration := jobDuration(value); duration != "" {
		return " (" + duration + ")"
	}
	return ""
}

func textValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
func humanLabel(value string) string {
	known := map[string]string{"table": "Table analysis", "candidates": "Relationship candidate selection", "pair": "Relationship evaluation", "app-deploy": "App deployment", "app-undeploy": "App undeployment", "app-shell-build": "Shell build", "function-run": "API run", "function-test": "API tests", "transform-test": "Transform tests", "api-prepare": "API preparation"}
	if label, ok := known[value]; ok {
		return label
	}
	value = strings.ReplaceAll(strings.ReplaceAll(value, "-", " "), "_", " ")
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
func objectList(value any) []map[string]any {
	result := []map[string]any{}
	switch rows := value.(type) {
	case []any:
		for _, row := range rows {
			if object, ok := row.(map[string]any); ok {
				result = append(result, object)
			}
		}
	case []map[string]any:
		result = rows
	}
	return result
}
func printExecution(out io.Writer, result api.Execution, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	timings, _ := result["timings"].(map[string]any)
	for _, field := range []struct{ key, label string }{{"job_id", "Job"}, {"type", "Type"}, {"resource_name", "Resource"}, {"resource_version", "Version"}, {"status", "Status"}, {"analysis_kind", "Analysis"}, {"environment", "Environment"}} {
		if value := result[field.key]; value != nil {
			label := textValue(value)
			if field.key == "type" || field.key == "analysis_kind" {
				label = humanLabel(label)
			}
			if field.key == "resource_name" && (result["resource_type"] == "table" || result["type"] == "knowledge-graph-discovery" || result["type"] == "schema-discovery" || result["type"] == "table-analysis") {
				label = displayTableName(label, "")
			}
			fmt.Fprintf(out, "%s: %s\n", field.label, label)
		}
	}
	if duration := jobDuration(timings["elapsed_seconds"]); duration != "" {
		fmt.Fprintf(out, "Discovery elapsed: %s\n", duration)
	}
	if result["resource_name"] == nil && result["resource_id"] != nil {
		fmt.Fprintf(out, "Resource ID: %v\n", result["resource_id"])
	}
	if result["completed"] != true && result["stage"] != nil {
		fmt.Fprintf(out, "Stage: %s\n", humanLabel(textValue(result["stage"])))
	}
	for _, field := range []struct{ key, label string }{{"waiting_reason", "Waiting"}, {"error", "Error"}, {"retry_error", "Retrying"}, {"downstream_error", "Downstream error"}, {"preview_url", "Preview URL"}, {"url", "URL"}} {
		if value := textValue(result[field.key]); value != "" {
			fmt.Fprintf(out, "%s: %s\n", field.label, value)
		}
	}
	if deployment, ok := result["deployment"].(map[string]any); ok && result["url"] == nil && deployment["domain"] != nil {
		fmt.Fprintf(out, "URL: https://%v/\n", deployment["domain"])
	}
	if deployment, ok := result["deployment"].(map[string]any); ok {
		if message := textValue(deployment["message"]); message != "" {
			fmt.Fprintln(out, message)
		}
	}
	for _, step := range objectList(result["steps"]) {
		mark := "…"
		if step["status"] == "success" {
			mark = "✓"
		}
		if step["status"] == "failed" {
			mark = "✗"
		}
		fmt.Fprintf(out, "  %s %v\n", mark, step["label"])
	}
	children := objectList(result["children"])
	if len(children) > 0 {
		fmt.Fprintln(out, "Related jobs:")
		for _, child := range children {
			name := textValue(child["resource_name"])
			if name == textValue(result["resource_name"]) && child["type"] != "schema-discovery" {
				name = ""
			}
			if name != "" {
				name += " — "
			}
			status := child["status"]
			if status == "success" && child["completed"] == false {
				status = "finalizing"
			}
			fmt.Fprintf(out, "  %s: %v — %s%v\n", humanLabel(textValue(child["type"])), status, name, child["job_id"])
		}
	} else {
		for _, field := range []struct{ key, label string }{{"build_job_id", "App build job"}, {"shell_build_job_id", "Shell build job"}, {"dependency_job_id", "Dependency build job"}, {"parent_job_id", "Parent job"}} {
			if result[field.key] != nil {
				fmt.Fprintf(out, "%s: %v\n", field.label, result[field.key])
			}
		}
	}
	for _, table := range objectList(result["input_tables"]) {
		fmt.Fprintf(out, "Input: %v = %s\n", table["alias"], displayTableName(textValue(table["name"]), ""))
	}
	for _, table := range objectList(result["tables"]) {
		fmt.Fprintf(out, "Table: %s", displayTableName(textValue(table["name"]), ""))
		if table["rows"] != nil {
			fmt.Fprintf(out, " — %v rows", table["rows"])
		}
		fmt.Fprintln(out)
	}
	if keys, ok := result["keys_complete"].(bool); ok {
		status := "pending"
		if keys {
			status = "complete"
		}
		fmt.Fprintf(out, "Primary keys: %s%s\n", status, jobDurationSuffix(timings["primary_keys_seconds"]))
	}
	pairs := objectList(result["relationships"])
	if result["relationships"] != nil {
		counts := map[string]int{}
		for _, pair := range pairs {
			counts[textValue(pair["status"])]++
		}
		fmt.Fprintf(out, "Relationships: %d completed, %d pending, %d running, %d failed%s\n", counts["completed"], counts["pending"], counts["running"], counts["failed"], jobDurationSuffix(timings["relationships_seconds"]))
		for _, pair := range pairs {
			fmt.Fprintf(out, "  %s ↔ %s: %v\n", displayTableName(textValue(pair["table_a_name"]), ""), displayTableName(textValue(pair["table_b_name"]), ""), pair["status"])
		}
	}
	if reason := textValue(timings["phase_details_unavailable"]); reason != "" {
		fmt.Fprintln(out, reason)
	}
	if value, ok := result["validation"].(map[string]any); ok {
		fmt.Fprintf(out, "Validation: %v\n", value["status"])
	}
	if result["passed"] != nil {
		fmt.Fprintf(out, "Tests passed: %v\n", result["passed"])
	}
	payload, _ := result["result"].(map[string]any)
	for i := 0; i < 3; i++ {
		nested, ok := payload["result"].(map[string]any)
		if !ok {
			break
		}
		payload = nested
	}
	if result["type"] == "connector-check" {
		if connected, ok := payload["connected"].(bool); ok {
			if connected {
				fmt.Fprintln(out, "Connection: successful")
			} else {
				fmt.Fprintln(out, "Connection: failed")
			}
		}
	}
	if result["type"] == "connector-discovery" && payload["tables"] != nil {
		tables := objectList(payload["tables"])
		fmt.Fprintf(out, "Discovered tables: %d\n", len(tables))
		for _, table := range tables {
			fmt.Fprintf(out, "  %v\n", table["name"])
			for _, column := range objectList(table["schema"]) {
				nullable := ""
				if column["nullable"] == false {
					nullable = " NOT NULL"
				}
				fmt.Fprintf(out, "    %v: %v%s\n", column["name"], column["type"], nullable)
			}
		}
	}
	for _, test := range objectList(payload["tests"]) {
		fmt.Fprintf(out, "  %v: %v\n", test["name"], test["status"])
		if test["error"] != nil {
			fmt.Fprintf(out, "    %v\n", test["error"])
		}
	}
	if counts, ok := payload["counts"].(map[string]any); ok {
		if counts["chunks"] != nil {
			fmt.Fprintf(out, "Chunks: %v\n", counts["chunks"])
		}
	}
	if payload["rows"] != nil && len(objectList(result["tables"])) == 0 {
		fmt.Fprintf(out, "Rows: %v\n", payload["rows"])
	}
	if result["type"] == "sync-preview" {
		if estimate := payload["avg_row_size_bytes"]; estimate != nil {
			fmt.Fprintf(out, "Estimated row size: %v bytes\n", estimate)
		} else if result["status"] == "success" {
			fmt.Fprintln(out, "Estimated row size: unavailable (empty sample)")
		}
	}
	if (result["type"] == "table-analysis" || result["type"] == "sync-preview") && payload["preview"] != nil {
		fmt.Fprintln(out, "Result preview:")
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(payload["preview"])
	}
	if result["type"] == "function-run" {
		var value any = result["result"]
		for i := 0; i < 4; i++ {
			object, ok := value.(map[string]any)
			if !ok {
				break
			}
			if object["job_status"] == nil && object["contract_version"] == nil && object["success"] == nil {
				break
			}
			next, exists := object["result"]
			if !exists {
				break
			}
			value = next
		}
		if value != nil {
			fmt.Fprintln(out, "Result:")
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(value)
		}
	}
	// Immediate non-job responses (deploy metadata, upload IDs) still remain visible.
	if result["job_id"] == nil {
		keys := []string{}
		for key := range result {
			if key != "resource_id" && key != "status" && key != "resource_name" && key != "type" && key != "resource_version" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(out, "%s: %v\n", humanLabel(key), result[key])
		}
	}
	return nil
}
