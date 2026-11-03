package resources

import (
	"strings"
	"testing"
)

func TestCommitPlanKeepsValidBranchesAndBlocksConsumers(t *testing.T) {
	for _, failure := range []string{"missing table", "stale baseline", "cycle"} {
		t.Run(failure, func(t *testing.T) {
			g := Graph{
				"transform:source":   {},
				"transform:ready":    {Upstream: []string{"transform:source"}},
				"transform:blocked":  {Remote: &Registered{ID: "old", HasCode: true}},
				"transform:consumer": {Upstream: []string{"transform:blocked"}},
			}
			blocked := map[string]string{}
			switch failure {
			case "missing table":
				g["transform:blocked"].Problems = []string{"upstream table must already exist: shop.orders"}
			case "stale baseline":
				blocked["transform:blocked"] = "pull and reconcile before committing"
			case "cycle":
				g["transform:blocked"].Upstream = []string{"transform:consumer"}
			}
			selected := map[string]bool{}
			for key := range g {
				selected[key] = true
			}
			order, skipped := g.CommitPlan(selected, blocked)
			if strings.Join(order, ",") != "transform:source,transform:ready" || len(skipped) != 2 {
				t.Fatalf("order=%v skipped=%v", order, skipped)
			}
			if skipped["transform:blocked"] == "" || skipped["transform:consumer"] == "" {
				t.Fatal("lost blocked branch", skipped)
			}
		})
	}
}

func TestCommitPlanUsesServerDependenciesForUnselectedCode(t *testing.T) {
	g := Graph{
		"transform:consumer": {Upstream: []string{"transform:middle"}},
		"transform:middle": {Remote: &Registered{ID: "middle", HasCode: true, UpstreamIDs: []string{"source"}},
			Upstream: []string{"transform:unselected-local-edit"}},
		"transform:source": {Remote: &Registered{ID: "source", HasCode: true}},
	}
	selected := map[string]bool{"transform:consumer": true, "transform:source": true}
	order, skipped := g.CommitPlan(selected, map[string]string{"transform:source": "version conflict"})
	if len(order) != 0 || len(skipped) != 2 || !strings.Contains(skipped["transform:consumer"], "version conflict") {
		t.Fatal(order, skipped)
	}
	order, skipped = g.CommitPlan(map[string]bool{"transform:consumer": true}, nil)
	if strings.Join(order, ",") != "transform:consumer" || len(skipped) != 0 {
		t.Fatal("followed an unselected local edit instead of server code", order, skipped)
	}
}
