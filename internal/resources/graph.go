package resources

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type AttachedFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type FileStatus struct {
	Changed bool           `json:"changed"`
	Total   int            `json:"total"`
	Added   []AttachedFile `json:"added"`
	Removed []AttachedFile `json:"removed"`
}

type Registered struct {
	SourceType  string      `json:"source_type"`
	Files       *FileStatus `json:"files,omitempty"`
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	Version     int         `json:"version"`
	CodeHash    string      `json:"code_hash"`
	HasCode     bool        `json:"has_code"`
	UpstreamIDs []string    `json:"upstream_ids"`
	ConnectorID string      `json:"connector_id"`
	SyncID      string      `json:"sync_id"`
	DatabaseID  string      `json:"database_id"`
}

func Key(kind, name string) string { return kind + ":" + name }

type Node struct {
	Local    *Resource
	Remote   *Registered
	Upstream []string
	// DirectUpstream excludes the producer edges inferred from table inputs.
	DirectUpstream []string
	Problems       []string
}
type Graph map[string]*Node

func BuildGraph(ctx context.Context, local []Resource, remote []Registered) (Graph, error) {
	remote, err := QualifySyncNames(remote)
	if err != nil {
		return nil, err
	}
	graph := Graph{}
	ids := map[string]string{}
	for i := range remote {
		row := &remote[i]
		key := Key(row.Type, row.Name)
		// Tables can share names. Their IDs are authoritative graph identities.
		if row.Type == "table" {
			key = "table:" + row.ID
		}
		if graph[key] != nil {
			return nil, fmt.Errorf("ambiguous resource %s", key)
		}
		graph[key] = &Node{Remote: row}
		if row.ID == "" || ids[row.ID] != "" {
			return nil, fmt.Errorf("missing or duplicate server resource ID")
		}
		ids[row.ID] = key
	}
	for _, node := range graph {
		for _, id := range node.Remote.UpstreamIDs {
			if key, ok := ids[id]; ok {
				node.Upstream = append(node.Upstream, key)
			} else {
				node.Problems = append(node.Problems, "missing upstream ID "+id)
			}
		}
	}
	for i := range local {
		row := &local[i]
		key := Key(row.Type, row.Name)
		if graph[key] == nil {
			graph[key] = &Node{}
		}
		graph[key].Local = row
	}
	for _, row := range local {
		node := graph[Key(row.Type, row.Name)]
		refs, err := ReadReferences(ctx, row)
		if err != nil {
			node.Problems = append(node.Problems, err.Error())
			continue
		}
		if row.Type == "app" || row.Type == "database" {
			continue
		} // No source-owned upstream declarations.
		node.Upstream = nil
		node.DirectUpstream = nil
		add := func(key string) { node.Upstream = append(node.Upstream, key) }
		direct := func(key string) {
			add(key)
			node.DirectUpstream = append(node.DirectUpstream, key)
		}
		for _, ref := range refs {
			parts := strings.Split(ref.Name, ".")
			switch ref.Kind {
			case "TransformOutput":
				direct(Key("transform", parts[0]))
			case "ApiRunner":
				key := Key("api", ref.Name)
				direct(key)
				if ref.APIID != "" && (graph[key] == nil || graph[key].Remote == nil || graph[key].Remote.ID != ref.APIID) {
					node.Problems = append(node.Problems, "ApiRunner name and ID differ: "+ref.Name)
				}
			case "Database":
				direct(Key("database", ref.Name))
			case "Secret":
				direct(Key("secret", ref.Name))
			case "Table", "DatabaseTable":
				if ref.Kind == "Table" && parts[0] == "_upload" {
					matches := []string{}
					for key, table := range graph {
						if table.Remote != nil && table.Remote.Type == "table" && table.Remote.SourceType == "upload" && table.Remote.Name == parts[1] {
							matches = append(matches, key)
						}
					}
					if len(matches) != 1 {
						node.Problems = append(node.Problems, "expected one uploaded table: "+ref.Name)
					} else {
						add(matches[0])
					}
					continue
				}
				parent := Key("database", parts[0])
				if ref.Kind == "Table" {
					parent = Key("sync", parts[0]+"/"+parts[1])
					add(Key("connector", parts[0]))
				}
				// Commit source against its producer; published data is required at execution.
				add(parent)
			}
		}
		if row.Type == "sync" {
			owner, _, _ := strings.Cut(row.Name, "/")
			add(Key("connector", owner))
			if node.Remote != nil {
				if parent := graph[ids[node.Remote.ConnectorID]]; parent == nil || parent.Remote.Name != owner {
					node.Problems = append(node.Problems, "sync folder does not match its registered connector")
				}
			}
		}
		sort.Strings(node.Upstream)
		node.Upstream = unique(node.Upstream)
		for _, key := range node.Upstream {
			if graph[key] == nil {
				node.Problems = append(node.Problems, "missing upstream "+key)
			}
		}
	}
	return graph, nil
}
func unique(values []string) []string {
	result := []string{}
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

// Order validates selected local edits against committed, unselected upstream code.
// It never includes another local edit implicitly.
func (g Graph) Order(selected map[string]bool) ([]string, error) {
	keys := []string{}
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return g.order(selected, keys, nil)
}

// CommitPlan isolates invalid branches without falling back to older server
// code for selected dependencies. Valid changes remain in upstream-first order.
func (g Graph) CommitPlan(selected map[string]bool, blocked map[string]string) ([]string, map[string]string) {
	keys := []string{}
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	order := []string{}
	skipped := map[string]string{}
	seen := map[string]bool{}
	for _, key := range keys {
		branch, err := g.order(selected, []string{key}, blocked)
		if err != nil {
			skipped[key] = strings.TrimPrefix(err.Error(), key+": ")
			continue
		}
		for _, upstream := range branch {
			if !seen[upstream] {
				seen[upstream] = true
				order = append(order, upstream)
			}
		}
	}
	return order, skipped
}

func (g Graph) order(selected map[string]bool, roots []string, blocked map[string]string) ([]string, error) {
	state := map[string]int{}
	result, chain := []string{}, []string{}
	ids := map[string]string{}
	for key, node := range g {
		if node.Remote != nil {
			ids[node.Remote.ID] = key
		}
	}
	var visit func(string) error
	visit = func(key string) error {
		if reason := blocked[key]; reason != "" {
			return fmt.Errorf("%s: %s", key, reason)
		}
		if state[key] == 1 {
			return fmt.Errorf("upstream cycle: %s", strings.Join(append(chain, key), " -> "))
		}
		if state[key] == 2 {
			return nil
		}
		node := g[key]
		if node == nil {
			return fmt.Errorf("missing upstream %s", key)
		}
		edges := node.Upstream
		if !selected[key] {
			if node.Remote == nil || !node.Remote.HasCode {
				return fmt.Errorf("upstream %s is not committed and is not selected", key)
			}
			edges = nil
			for _, id := range node.Remote.UpstreamIDs {
				upstream, ok := ids[id]
				if !ok {
					return fmt.Errorf("missing upstream ID %s", id)
				}
				edges = append(edges, upstream)
			}
		} else if len(node.Problems) > 0 {
			return fmt.Errorf("%s: %s", key, strings.Join(node.Problems, "; "))
		}
		state[key] = 1
		chain = append(chain, key)
		for _, upstream := range edges {
			if err := visit(upstream); err != nil {
				return err
			}
		}
		chain = chain[:len(chain)-1]
		state[key] = 2
		if selected[key] {
			result = append(result, key)
		}
		return nil
	}
	for _, key := range roots {
		if err := visit(key); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Include current server edges as well as local edits when reporting potential impact.
func (g Graph) Downstream(key string) []string {
	reverse := map[string][]string{}
	ids := map[string]string{}
	for name, node := range g {
		if node.Remote != nil {
			ids[node.Remote.ID] = name
		}
	}
	for name, node := range g {
		for _, upstream := range node.Upstream {
			reverse[upstream] = append(reverse[upstream], name)
		}
		if node.Remote != nil {
			for _, id := range node.Remote.UpstreamIDs {
				if upstream, ok := ids[id]; ok {
					reverse[upstream] = append(reverse[upstream], name)
				}
			}
		}
	}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(upstream string) {
		for _, name := range reverse[upstream] {
			if name != key && !seen[name] {
				seen[name] = true
				visit(name)
			}
		}
	}
	visit(key)
	result := []string{}
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// The server keeps sync basename and connector_id separate; CLI identity is qualified.
func QualifySyncNames(rows []Registered) ([]Registered, error) {
	rows = append([]Registered(nil), rows...)
	owners := map[string]string{}
	for _, row := range rows {
		if row.Type == "connector" {
			owners[row.ID] = row.Name
		}
	}
	for i := range rows {
		row := &rows[i]
		if row.Type != "sync" {
			continue
		}
		owner := owners[row.ConnectorID]
		if owner == "" {
			return nil, fmt.Errorf("sync %s has a missing connector", row.ID)
		}
		if !strings.Contains(row.Name, "/") {
			row.Name = owner + "/" + row.Name
		}
		if err := ValidateResourceName("sync", row.Name); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(row.Name, owner+"/") {
			return nil, fmt.Errorf("sync %s belongs to a different connector", row.Name)
		}
	}
	return rows, nil
}
