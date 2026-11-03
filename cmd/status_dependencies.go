package cmd

import (
	"sort"
	"strings"

	"vortexify-cli/internal/resources"
)

// Project human-readable dependencies without changing the graph used for
// validation, commit ordering, impact analysis, or JSON inspection.
func statusDependencies(graph resources.Graph, node *resources.Node, label func(string) string) ([]string, []string) {
	byID := map[string]string{}
	for key, candidate := range graph {
		if candidate.Remote != nil {
			byID[candidate.Remote.ID] = key
		}
	}
	direct := map[string]bool{}
	for _, key := range node.DirectUpstream {
		direct[key] = true
	}
	covered := map[string]bool{}
	tables := []string{}
	tableKeys := map[string]bool{}
	for _, key := range node.Upstream {
		table := graph[key]
		if table == nil || table.Remote == nil || table.Remote.Type != "table" || tableKeys[key] {
			continue
		}
		tableKeys[key] = true
		name := table.Remote.Name
		for _, id := range table.Remote.UpstreamIDs {
			ownerKey := byID[id]
			owner := graph[ownerKey]
			if owner == nil || owner.Remote == nil {
				continue
			}
			kind := owner.Remote.Type
			if kind != "sync" && kind != "transform" && kind != "database" {
				continue
			}
			prefix := owner.Remote.Name + "/"
			if !strings.HasPrefix(name, prefix) {
				name = prefix + name
			}
			name = displayTableName(name, kind)
			if !direct[ownerKey] {
				covered[ownerKey] = true
			}
			if kind == "sync" {
				connector := byID[owner.Remote.ConnectorID]
				if connector != "" && !direct[connector] {
					covered[connector] = true
				}
			}
			break
		}
		if name == "" || (name == "default" && len(table.Remote.UpstreamIDs) > 0) {
			name = key + " (producer unavailable)"
		}
		tables = append(tables, name)
	}
	upstream := []string{}
	for _, key := range node.Upstream {
		if !tableKeys[key] && !covered[key] {
			upstream = append(upstream, label(key))
		}
	}
	sort.Strings(tables)
	sort.Strings(upstream)
	return upstream, tables
}
