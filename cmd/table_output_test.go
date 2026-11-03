package cmd

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"vortexify-cli/internal/api"
)

func TestTableOverviewAndVerbosePreserveSavedFindings(t *testing.T) {
	details := &api.TableDetails{
		Table:       api.TableSummary{ID: "items", DisplayName: "postgres/items/default", Description: "An item master describing the manufacturing catalog.", Status: "ready", KnowledgeGraph: &api.TableKnowledgeGraph{JobID: "kg", Status: "completed", KeysComplete: true}},
		Publication: map[string]any{"source_job_id": "run", "discovery_job_id": "schema", "source_resource_version": 2},
		KeyAnalysis: &api.TableKeyAnalysis{Completed: true, Keys: []api.TableKey{{Columns: []string{"item_code"}, UniquePercentage: 97.450424929, GrainDescription: "One record per item.", DuplicatesReason: "Some rows are repeated."}}},
		RelationshipEvaluations: []api.TableRelationshipEvaluation{
			{OtherTableID: "bom", Status: "completed", Reason: "Checked the assigned BOM pair.", Relationships: []api.TableRelationship{{SourceTableID: "items", TargetTableID: "bom", SourceColumns: []string{"item_code"}, TargetColumns: []string{"item"}, Cardinality: "one_to_many", Description: "Items are referenced by BOMs.", MatchingLogic: "Match item_code to item."}}},
			{OtherTableID: "wms", Status: "completed", Outcome: "no_relationship_found", Reason: "No identifier crosswalk is available."},
			{OtherTableID: "unknown", Status: "completed", Outcome: "inconclusive", LastError: "Investigation could not verify this pair."},
		},
	}
	names := map[string]string{"bom": "postgres/boms/default", "wms": "postgres/inventory/default"}
	for _, verbose := range []bool{false, true} {
		var out bytes.Buffer
		if err := printTableDetails(&out, details, names, verbose); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		for _, want := range []string{"An item master", "97.45% unique", "3 pairs evaluated · 1 relationship found", "item_code → postgres/boms.item  one-to-many", "Items are referenced by BOMs.", "No relationship found.", "inconclusive", "Investigation could not verify this pair."} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q in %s", want, text)
			}
		}
		if strings.Index(text, "Jobs\n") < strings.Index(text, "Relationships:") {
			t.Fatal("jobs should follow findings", text)
		}
		for _, detail := range []string{"One record per item.", "Some rows are repeated.", "Match item_code to item.", "Checked the assigned BOM pair."} {
			if strings.Contains(text, detail) != verbose {
				t.Fatalf("verbose=%v: %q in %s", verbose, detail, text)
			}
		}
	}
}

func TestTableProseWrapsWithoutSummarizing(t *testing.T) {
	text := strings.Repeat("Saved business explanation with Unicode café. ", 8)
	var out bytes.Buffer
	tableProse(&out, "      ", text)
	if strings.Join(strings.Fields(out.String()), " ") != strings.Join(strings.Fields(text), " ") {
		t.Fatal("saved prose changed", out.String())
	}
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if utf8.RuneCountInString(line) > 96 || !strings.HasPrefix(line, "      ") {
			t.Fatal("invalid prose wrapping", line)
		}
	}
}
