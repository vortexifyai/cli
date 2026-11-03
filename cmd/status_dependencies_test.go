package cmd

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/output"
	"vortexify-cli/internal/resources"
)

func TestStatusDependenciesShowNamedTablesWithoutDuplicatingProducers(t *testing.T) {
	graph := resources.Graph{
		"connector:shop":   {Remote: &resources.Registered{ID: "connector", Type: "connector", Name: "shop"}},
		"sync:shop/orders": {Remote: &resources.Registered{ID: "sync", Type: "sync", Name: "shop/orders", ConnectorID: "connector"}},
		"database:ops":     {Remote: &resources.Registered{ID: "db", Type: "database", Name: "ops"}},
		"transform:rollup": {Remote: &resources.Registered{ID: "transform", Type: "transform", Name: "rollup"}},
		"table:orders":     {Remote: &resources.Registered{ID: "orders", Type: "table", Name: "default", SyncID: "sync", UpstreamIDs: []string{"sync"}}},
		"table:reviews":    {Remote: &resources.Registered{ID: "reviews", Type: "table", Name: "public.reviews", DatabaseID: "db", UpstreamIDs: []string{"db"}}},
		"table:decisions":  {Remote: &resources.Registered{ID: "decisions", Type: "table", Name: "decisions", UpstreamIDs: []string{"transform"}}},
	}
	node := &resources.Node{Upstream: []string{"connector:shop", "sync:shop/orders", "table:orders", "database:ops", "table:reviews", "transform:rollup", "table:decisions"}}
	original := append([]string(nil), node.Upstream...)
	upstream, tables := statusDependencies(graph, node, func(key string) string { return key })
	if len(upstream) != 0 || !reflect.DeepEqual(tables, []string{"ops/public.reviews", "rollup/decisions", "shop/orders"}) {
		t.Fatal(upstream, tables)
	}
	if !reflect.DeepEqual(node.Upstream, original) {
		t.Fatal("display altered dependency graph")
	}
	node.DirectUpstream = []string{"database:ops"}
	upstream, _ = statusDependencies(graph, node, func(key string) string { return key })
	if !reflect.DeepEqual(upstream, []string{"database:ops"}) {
		t.Fatal("hid an explicit database connection", upstream)
	}
}

func TestStatusDependenciesRetainNonTableInputsAndMissingProducers(t *testing.T) {
	graph := resources.Graph{
		"table:upload": {Remote: &resources.Registered{ID: "upload", Type: "table", Name: "default"}},
		"table:broken": {Remote: &resources.Registered{ID: "broken", Type: "table", Name: "default", UpstreamIDs: []string{"missing"}}},
	}
	node := &resources.Node{Upstream: []string{"api:lookup", "secret:token", "table:upload", "table:broken"}}
	upstream, tables := statusDependencies(graph, node, func(key string) string { return key + " (server-only)" })
	if !reflect.DeepEqual(upstream, []string{"api:lookup (server-only)", "secret:token (server-only)"}) || !reflect.DeepEqual(tables, []string{"default", "table:broken (producer unavailable)"}) {
		t.Fatal(upstream, tables)
	}
}

func TestStatusRendersTableGroupWithoutChangingJSON(t *testing.T) {
	row := output.ResourceStatus{Name: "api:lookup", Registration: "registered", Change: "clean", Upstream: []string{"api:other"}, UpstreamTables: []string{"shop/orders", "rollup/decisions"}}
	var out bytes.Buffer
	if err := output.PrintStatus(&out, []output.ResourceStatus{row}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  Upstream tables:\n    shop/orders\n    rollup/decisions\n  Upstream: api:other") {
		t.Fatal(out.String())
	}
	encoded, _ := json.Marshal(row)
	if strings.Contains(string(encoded), "shop/orders") {
		t.Fatal("display projection leaked into JSON", string(encoded))
	}
}

func TestDefaultOutputNamesAreHiddenOnlyInDisplay(t *testing.T) {
	for _, pair := range []struct{ kind, raw, want string }{
		{"sync", "shop/orders/default", "shop/orders"},
		{"transform", "sales/default", "sales"},
		{"transform", "rollup/decisions", "rollup/decisions"},
		{"database", "ops/default", "ops/default"},
		{"upload", "data/default", "data/default"},
	} {
		table := api.TableSummary{Type: pair.kind, DisplayName: pair.raw}
		if tableLabel(table) != pair.want || table.DisplayName != pair.raw {
			t.Fatal(pair, tableLabel(table))
		}
		if !tableMatches(table, pair.raw) || !tableMatches(table, pair.want) {
			t.Fatal("lost raw or displayed reference", pair)
		}
	}
}
