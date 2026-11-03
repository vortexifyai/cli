package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"sort"
	"strings"
	"vortexify-cli/internal/output"
	"vortexify-cli/internal/projectfiles"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
)

func newStatusCommand(opts *options) *cobra.Command {
	filter := resources.Filter{}
	var asJSON bool
	command := &cobra.Command{Use: "status", Short: "Show local registration, upstream requirements and downstream impact", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := filter.Validate(); err != nil {
			return err
		}
		root, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}
		local, err := resources.Scan(root, cfg)
		if err != nil {
			return err
		}
		remote, err := client.GetResourceGraph(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		graph, err := resources.BuildGraph(cmd.Context(), local, remote)
		if err != nil {
			return err
		}
		st, err := state.Load(root)
		if err != nil {
			return err
		}
		for _, row := range remote {
			if row.Type == "sync" {
				state.QualifySync(st, row.ID, row.Name)
			}
		}
		entries := []resources.RegistryEntry{}
		for _, node := range graph {
			if node.Remote != nil {
				entries = append(entries, resources.RegistryEntry{ID: node.Remote.ID, Type: node.Remote.Type, Name: node.Remote.Name})
			}
			if node.Local != nil {
				entries = append(entries, resources.RegistryEntry{Type: node.Local.Type, Name: node.Local.Name})
			}
		}
		if err := filter.CheckSelection(entries); err != nil {
			return err
		}
		rows := []output.ResourceStatus{}
		keys := []string{}
		for key := range graph {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		selected := map[string]bool{}
		for _, key := range keys {
			node := graph[key]
			if node.Local == nil {
				continue
			}
			row := node.Local
			id := ""
			if node.Remote != nil {
				id = node.Remote.ID
			}
			if !filter.Match(row.Type, row.Name, id) {
				continue
			}
			selected[key] = true
			baseline := st.Resources[state.ResourceKey(row.Type, row.Name)]
			status := output.ResourceStatus{Name: key, Registration: "not registered", Change: "new", Problems: node.Problems}
			if node.Remote != nil {
				status.Registration = "registered"
				status.Files = node.Remote.Files
				status.Change = "modified"
				switch {
				case row.Hash == node.Remote.CodeHash:
					status.Change = "clean"
				case baseline.ResourceID == "":
					status.Change = "untracked local code"
				case baseline.ResourceID == id && baseline.CodeHash == row.Hash:
					status.Change = "server changed"
				case baseline.ResourceID != id || baseline.CodeHash != node.Remote.CodeHash:
					status.Change = "diverged; pull to reconcile"
				}
			}
			label := func(key string) string {
				n := graph[key]
				if n == nil {
					return key + " (missing)"
				}
				if n.Remote != nil && !n.Remote.HasCode {
					return key + " (registered; uncommitted)"
				}
				if n.Local == nil {
					return key + " (server-only)"
				}
				if n.Remote == nil {
					return key + " (not registered)"
				}
				if n.Local.Hash != n.Remote.CodeHash {
					return key + " (modified locally)"
				}
				return key
			}
			for _, up := range node.Upstream {
				status.Upstream = append(status.Upstream, label(up))
			}
			if !asJSON {
				status.Upstream, status.UpstreamTables = statusDependencies(graph, node, label)
			}
			for _, down := range graph.Downstream(key) {
				if graph[down].Local != nil || (graph[down].Remote != nil && graph[down].Remote.Type != "table") {
					status.Downstream = append(status.Downstream, label(down))
				}
			}
			rows = append(rows, status)
		}
		for _, node := range graph {
			if node.Local == nil && node.Remote != nil && node.Remote.Files != nil && filter.Match(node.Remote.Type, node.Remote.Name, node.Remote.ID) {
				rows = append(rows, output.ResourceStatus{Name: resources.Key(node.Remote.Type, node.Remote.Name), Registration: "registered", Change: "server-only", Files: node.Remote.Files})
			}
		}
		previews, err := client.PreviewStatus(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		for _, preview := range previews {
			for _, row := range remote {
				if row.ID == preview["id"] {
					preview["name"] = row.Name
				}
			}
		}
		filtered := previews[:0]
		for _, preview := range previews {
			kind, _ := preview["type"].(string)
			name, _ := preview["name"].(string)
			id, _ := preview["id"].(string)
			if filter.Match(kind, name, id) {
				filtered = append(filtered, preview)
			}
		}
		previews = filtered
		var collections []projectfiles.Status
		if filter.Type == "" && filter.Resource == "" {
			collections, err = projectfiles.States(cmd.Context(), root, cfg, client)
			if err != nil {
				return err
			}
		}
		if asJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"resources": rows, "previews": previews, "collections": collections}); err != nil {
				return err
			}
		} else {
			positions := map[string]int{}
			for i := range rows {
				positions[rows[i].Name] = i
			}
			for _, preview := range previews {
				kind, _ := preview["type"].(string)
				name, _ := preview["name"].(string)
				key := kind + ":" + name
				i, exists := positions[key]
				if !exists {
					i = len(rows)
					positions[key] = i
					rows = append(rows, output.ResourceStatus{Name: key, Registration: "registered", Change: "server-only"})
				}
				previewStatus, _ := preview["preview_status"].(string)
				switch previewStatus {
				case "source-only":
					previewStatus = ""
				case "unbuilt":
					previewStatus = "never run"
				}
				rows[i].Preview = previewStatus
				if deployed := preview["deployed_version"]; deployed != nil {
					rows[i].Deployment = fmt.Sprintf("v%v", deployed)
					if available, _ := preview["upgrade_available"].(bool); available {
						rows[i].Deployment += " (upgrade available)"
					}
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
			if err := output.PrintStatus(cmd.OutOrStdout(), rows); err != nil {
				return err
			}
		}
		// Include reachable local upstream nodes in status validation, without selecting them for commit.
		inspect := map[string]bool{}
		var add func(string)
		add = func(key string) {
			if inspect[key] {
				return
			}
			node := graph[key]
			if node == nil || node.Local == nil {
				return
			}
			inspect[key] = true
			for _, up := range node.Upstream {
				add(up)
			}
		}
		for key := range selected {
			add(key)
		}
		for _, collection := range collections {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n  Documents: %s (version %d)\n\n", collection.Collection, collection.State, collection.Version)
		}
		return statusValidationError(graph, inspect, rows)
	}}
	command.Flags().StringVar(&filter.Type, "type", "", "Filter resource type")
	command.Flags().StringVar(&filter.Resource, "resource", "", "Filter resource name or ID")
	command.Flags().BoolVar(&asJSON, "json", false, "Print registration and preview freshness as JSON")
	return command
}

func statusValidationError(graph resources.Graph, inspect map[string]bool, rows []output.ResourceStatus) error {
	shown := map[string]bool{}
	for _, row := range rows {
		for _, problem := range row.Problems {
			shown[row.Name+": "+problem] = true
			shown[problem] = true
		}
	}
	// Validate edges even when a node's already-displayed problems would make
	// Order stop before reaching a cycle or an undisplayed upstream problem.
	structural := resources.Graph{}
	additional := []string{}
	failed := false
	keys := []string{}
	for key := range inspect {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if node := graph[key]; node != nil {
			for _, problem := range node.Problems {
				failed = true
				message := key + ": " + problem
				if !shown[message] {
					additional = append(additional, message)
				}
			}
		}
	}
	for key, node := range graph {
		copy := *node
		if inspect[key] {
			copy.Problems = nil
		}
		structural[key] = &copy
	}
	if _, err := structural.Order(inspect); err != nil {
		failed = true
		if !shown[err.Error()] {
			additional = append(additional, err.Error())
		}
	}
	if len(additional) > 0 {
		return fmt.Errorf("upstream validation: %s", strings.Join(additional, "; "))
	}
	if failed {
		return &reportedError{fmt.Errorf("resource validation failed; see resource errors above")}
	}
	return nil
}
