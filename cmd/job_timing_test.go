package cmd

import (
	"bytes"
	"strings"
	"testing"

	"vortexify-cli/internal/api"
)

func TestDiscoveryJobDisplaysPhaseTimings(t *testing.T) {
	result := api.Execution{"job_id": "job", "type": "knowledge-graph-discovery", "status": "success", "keys_complete": true,
		"relationships": []any{map[string]any{"table_a_name": "items", "table_b_name": "boms", "status": "completed"}},
		"timings":       map[string]any{"elapsed_seconds": float64(300), "primary_keys_seconds": float64(120), "relationships_seconds": float64(180)}}
	var out bytes.Buffer
	if err := printExecution(&out, result, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Discovery elapsed: 5m0s", "Primary keys: complete (2m0s)", "0 failed (3m0s)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
}

func TestDiscoveryJobDisplaysSeparateAnalysisKind(t *testing.T) {
	var out bytes.Buffer
	result := api.Execution{"job_id": "pair-job", "type": "knowledge-graph-discovery", "analysis_kind": "pair", "status": "success", "completed": true}
	if err := printExecution(&out, result, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Analysis: Relationship evaluation") || strings.Contains(out.String(), "Primary keys:") {
		t.Fatalf("unexpected pair job output: %s", out.String())
	}
}
