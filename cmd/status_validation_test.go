package cmd

import (
	"errors"
	"strings"
	"testing"

	"vortexify-cli/internal/output"
	"vortexify-cli/internal/resources"
)

func TestStatusValidationDoesNotRepeatDisplayedProblems(t *testing.T) {
	key := "sync:postgres/items"
	problem := "register this sync with vortexify create sync postgres/items first"
	graph := resources.Graph{key: {Problems: []string{problem}}}
	rows := []output.ResourceStatus{{Name: key, Problems: []string{problem}}}
	err := statusValidationError(graph, map[string]bool{key: true}, rows)
	var reported *reportedError
	if !errors.As(err, &reported) {
		t.Fatalf("expected already-reported failure, got %v", err)
	}
	if len(graph[key].Problems) != 1 {
		t.Fatal("validation mutated the resource graph")
	}
}

func TestStatusValidationShowsUndisplayedUpstreamProblems(t *testing.T) {
	graph := resources.Graph{
		"transform:report":    {Upstream: []string{"sync:postgres/items"}},
		"sync:postgres/items": {Problems: []string{"registration required"}},
	}
	err := statusValidationError(graph, map[string]bool{"transform:report": true, "sync:postgres/items": true}, nil)
	var reported *reportedError
	if err == nil || errors.As(err, &reported) || !strings.Contains(err.Error(), "sync:postgres/items: registration required") {
		t.Fatalf("expected visible upstream problem, got %v", err)
	}
}

func TestStatusValidationShowsCycleAlongsideDisplayedProblems(t *testing.T) {
	graph := resources.Graph{
		"transform:a": {Upstream: []string{"transform:b"}, Problems: []string{"source error"}},
		"transform:b": {Upstream: []string{"transform:a"}},
	}
	rows := []output.ResourceStatus{{Name: "transform:a", Problems: []string{"source error"}}}
	err := statusValidationError(graph, map[string]bool{"transform:a": true, "transform:b": true}, rows)
	if err == nil || !strings.Contains(err.Error(), "upstream cycle:") || strings.Contains(err.Error(), "source error") {
		t.Fatalf("expected only the additional cycle problem, got %v", err)
	}
}

func TestStatusValidationSucceedsForValidGraph(t *testing.T) {
	if err := statusValidationError(resources.Graph{"transform:a": {}}, map[string]bool{"transform:a": true}, nil); err != nil {
		t.Fatal(err)
	}
}
