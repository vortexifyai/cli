package cmd

import (
	"bytes"
	"strings"
	"testing"

	"vortexify-cli/internal/api"
)

func TestTableJobsGroupsSelectedOutputJobs(t *testing.T) {
	details := &api.TableDetails{
		Publication: map[string]any{"source_job_id": "run-1", "discovery_job_id": "schema-1"},
		Table:       api.TableSummary{DiscoveryJobID: "newer-schema", KnowledgeGraph: &api.TableKnowledgeGraph{JobID: "graph-1"}},
	}
	var out bytes.Buffer
	if err := printTableJobs(&out, details); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Jobs\n", "Source run", "run-1", "Schema discovery", "schema-1", "Knowledge graph", "graph-1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	for _, unwanted := range []string{"newer-schema", "vortexify logs", "<nil>"} {
		if strings.Contains(out.String(), unwanted) {
			t.Fatal(out.String())
		}
	}
}

func TestTableJobsHandlesPendingAndMissingJobs(t *testing.T) {
	var out bytes.Buffer
	if err := printTableJobs(&out, &api.TableDetails{}); err != nil || out.Len() != 0 {
		t.Fatal(out.String(), err)
	}
	details := &api.TableDetails{Table: api.TableSummary{DiscoveryJobID: "pending-schema"}}
	if err := printTableJobs(&out, details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pending-schema") || strings.Contains(out.String(), "Source run") {
		t.Fatal(out.String())
	}
}
