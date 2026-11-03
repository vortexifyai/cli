package cmd

import (
	"strings"
	"testing"

	"vortexify-cli/internal/api"
)

func TestTableReferencesFollowResourceStructure(t *testing.T) {
	tables := []api.TableSummary{
		{ID: "mes-id", Type: "sync", Name: "default", DisplayName: "mes/orders/default"},
		{ID: "wms-id", Type: "sync", Name: "default", DisplayName: "wms/orders/default"},
		{ID: "single-id", Type: "transform", Name: "default", DisplayName: "current_orders/default"},
		{ID: "totals-id", Type: "transform", Name: "totals", DisplayName: "summary/totals"},
		{ID: "daily-id", Type: "transform", Name: "daily", DisplayName: "summary/daily"},
		{ID: "notes-id", Type: "database", Name: "public.notes", DisplayName: "operations/public.notes [origin]", Environment: "origin"},
		{ID: "upload-id", Type: "upload", Name: "sample_orders", DisplayName: "sample_orders"},
	}
	for reference, expected := range map[string]string{
		"mes.orders": "mes-id", "mes/orders": "mes-id", "mes/orders/default": "mes-id",
		"wms.orders": "wms-id", "current_orders": "single-id",
		"summary.totals": "totals-id", "summary/daily": "daily-id",
		"operations.notes": "notes-id", "operations.public.notes": "notes-id",
		"operations/public.notes [origin]": "notes-id", "sample_orders": "upload-id",
		"sync:mes/orders": "mes-id", "transform:summary/totals": "totals-id",
		"database:operations.notes": "notes-id", "upload:sample_orders": "upload-id",
		"mes-id": "mes-id",
	} {
		table, err := resolveTable(tables, reference)
		if err != nil || table.ID != expected {
			t.Errorf("%s: %v, %v", reference, table, err)
		}
	}
	for _, reference := range []string{"default", "summary"} {
		_, err := resolveTable(tables, reference)
		if err == nil || !strings.Contains(err.Error(), "qualified name") || strings.Contains(err.Error(), "use the table ID") {
			t.Fatal("missing named disambiguation", reference, err)
		}
	}
	if _, err := resolveTable(tables, "other_project.orders"); err == nil {
		t.Fatal("resolved unknown table")
	}
}

func TestTableReferencesDisambiguateResourceTypes(t *testing.T) {
	tables := []api.TableSummary{
		{ID: "sync-id", Type: "sync", Name: "default", DisplayName: "mes/orders/default"},
		{ID: "transform-id", Type: "transform", Name: "orders", DisplayName: "mes/orders"},
	}
	if _, err := resolveTable(tables, "mes.orders"); err == nil || !strings.Contains(err.Error(), "sync:mes/orders") || strings.Contains(err.Error(), "/default") || !strings.Contains(err.Error(), "transform:mes/orders") {
		t.Fatal(err)
	}
	for _, reference := range []string{"sync:mes.orders", "transform:mes.orders"} {
		if _, err := resolveTable(tables, reference); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRootHelpGroupsAreAlphabetical(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.GroupID == "" {
			t.Fatalf("command %s is not grouped", command.Name())
		}
	}
	out, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for i, group := range root.Groups() {
		start := strings.Index(out, group.Title)
		if start < 0 {
			t.Fatal("missing help group", group.Title)
		}
		end := len(out)
		if i+1 < len(root.Groups()) {
			end = strings.Index(out, root.Groups()[i+1].Title)
		} else if flags := strings.Index(out[start:], "Flags:"); flags >= 0 {
			end = start + flags
		}
		previous := ""
		for _, line := range strings.Split(out[start:end], "\n") {
			if !strings.HasPrefix(line, "  ") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if fields[0] < previous {
				t.Fatalf("%s not alphabetical: %s before %s", group.Title, previous, fields[0])
			}
			previous = fields[0]
		}
	}
}
