package cmd

import (
	"fmt"
	"sort"
	"strings"

	"vortexify-cli/internal/api"
)

// The server labels tables with their owning resource path. Resolve those paths
// locally, keeping platform IDs an implementation detail of API requests.
func tableReferences(table api.TableSummary) []string {
	label := table.DisplayName
	if label == "" {
		label = table.Name
	}
	if table.Type == "database" && table.Environment != "" {
		label = strings.TrimSuffix(label, " ["+table.Environment+"]")
	}
	display := tableLabel(table)
	refs := []string{display, strings.ReplaceAll(display, "/", "."), label, strings.ReplaceAll(label, "/", "."), table.Name, table.DisplayName}
	parts := strings.Split(label, "/")
	switch table.Type {
	case "sync":
		if len(parts) >= 3 {
			refs = append(refs, strings.Join(parts[:2], "/"), strings.Join(parts[:2], "."))
		}
	case "transform":
		if len(parts) == 2 {
			refs = append(refs, parts[0])
		}
	case "database":
		if len(parts) == 2 && strings.HasPrefix(parts[1], "public.") {
			name := strings.TrimPrefix(parts[1], "public.")
			refs = append(refs, parts[0]+"/"+name, parts[0]+"."+name)
		}
	}
	return refs
}

func tableMatches(table api.TableSummary, reference string) bool {
	if kind, value, typed := strings.Cut(reference, ":"); typed {
		if kind != table.Type {
			return false
		}
		reference = value
	}
	for _, name := range tableReferences(table) {
		if name != "" && name == reference {
			return true
		}
	}
	return false
}

func resolveTable(tables []api.TableSummary, reference string) (*api.TableSummary, error) {
	// Keep explicit IDs accepted for compatibility, but never require them.
	for i := range tables {
		if tables[i].ID == reference {
			return &tables[i], nil
		}
	}
	var matches []int
	for i, table := range tables {
		if tableMatches(table, reference) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("table %q not found in this project; use 'vortexify tables' to see available names", reference)
	}
	if len(matches) > 1 {
		choices := []string{}
		for _, i := range matches {
			table := tables[i]
			label := tableReferences(table)[0]
			choices = append(choices, table.Type+":"+label)
		}
		sort.Strings(choices)
		return nil, fmt.Errorf("ambiguous table %q; choose a qualified name: %s", reference, strings.Join(choices, ", "))
	}
	return &tables[matches[0]], nil
}
