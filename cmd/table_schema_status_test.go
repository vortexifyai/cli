package cmd

import (
	"testing"
	"vortexify-cli/internal/api"
)

func TestTableDataAndSchemaReadinessAreSeparate(t *testing.T) {
	for _, tt := range []struct{ status, data, schema string }{
		{"ready", "ready", "complete"},
		{"schema pending", "not ready", "pending"},
		{"discovering schema", "not ready", "discovering"},
		{"schema failed", "not ready", "failed"},
		{"schema stopped", "not ready", "stopped"},
		{"schema retrying", "not ready", "retrying"},
		{"publishing", "not ready", "publishing"},
		{"ready; discovering schema", "ready", "discovering"},
		{"ready; schema failed", "ready", "failed"},
		{"registered; publication unknown", "registered; publication unknown", "unknown"},
	} {
		t.Run(tt.status, func(t *testing.T) {
			data, schema := tableDataSchemaStatuses(api.TableSummary{Type: "sync", Status: tt.status})
			if data != tt.data || schema != tt.schema {
				t.Fatalf("got %q, %q; want %q, %q", data, schema, tt.data, tt.schema)
			}
		})
	}
	_, schema := tableDataSchemaStatuses(api.TableSummary{Type: "database", Status: "registered"})
	if schema != "not applicable" {
		t.Fatal(schema)
	}
}
