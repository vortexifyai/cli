package resources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func graphSource(t *testing.T, name, upstream string) Resource {
	t.Helper()
	dir := t.TempDir()
	source := "from vortexify import vxfy_transform, TransformOutput, DatabaseTable, Database\n@vxfy_transform(inputs={" + upstream + "})\ndef run(**kwargs): return None\n"
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	return Resource{Type: "transform", Name: name, Path: dir}
}
func TestGraphOrdersSelectedUpstreamBeforeAlphabeticalDownstream(t *testing.T) {
	local := []Resource{graphSource(t, "a_final", `"x": TransformOutput("z_middle.named")`), graphSource(t, "z_middle", `"x": TransformOutput("z_source")`), graphSource(t, "z_source", "")}
	g, err := BuildGraph(context.Background(), local, nil)
	if err != nil {
		t.Fatal(err)
	}
	order, err := g.Order(map[string]bool{"transform:a_final": true, "transform:z_middle": true, "transform:z_source": true})
	if err != nil || strings.Join(order, ",") != "transform:z_source,transform:z_middle,transform:a_final" {
		t.Fatal(order, err)
	}
	if _, err := g.Order(map[string]bool{"transform:a_final": true}); err == nil {
		t.Fatal("included an unselected, uncommitted upstream")
	}
}
func TestGraphRejectsCyclesAndMissingUpstream(t *testing.T) {
	for _, up := range []string{"missing", "a_final"} {
		local := []Resource{graphSource(t, "a_final", `"x": TransformOutput("middle")`), graphSource(t, "middle", `"x": TransformOutput("`+up+`")`)}
		g, err := BuildGraph(context.Background(), local, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.Order(map[string]bool{"transform:a_final": true, "transform:middle": true}); err == nil {
			t.Fatal("invalid graph accepted")
		}
	}
}

func TestGraphDistinguishesTableProducersFromExplicitConnections(t *testing.T) {
	local := graphSource(t, "review", `"reviews": DatabaseTable("ops.reviews"), "connection": Database("ops")`)
	graph, err := BuildGraph(context.Background(), []Resource{local}, []Registered{
		{ID: "db", Type: "database", Name: "ops", HasCode: true},
		{ID: "table", Type: "table", Name: "public.reviews", DatabaseID: "db", UpstreamIDs: []string{"db"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	node := graph["transform:review"]
	if len(node.Problems) != 0 || strings.Join(node.Upstream, ",") != "database:ops,table:table" || strings.Join(node.DirectUpstream, ",") != "database:ops" {
		t.Fatalf("unexpected dependency provenance: %+v", node)
	}
}

func TestNewSyncRequiresItsConnectorButNotRegistration(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sync.py"), []byte("from vortexify import Sync\nclass Orders(Sync): pass\n"), 0644)
	sync := Resource{Type: "sync", Name: "shop/orders", Path: dir}
	for _, present := range []bool{false, true} {
		var remote []Registered
		if present {
			remote = []Registered{{ID: "c", Type: "connector", Name: "shop", HasCode: true}}
		}
		graph, err := BuildGraph(context.Background(), []Resource{sync}, remote)
		if err != nil {
			t.Fatal(err)
		}
		_, err = graph.Order(map[string]bool{"sync:shop/orders": true})
		if present && err != nil {
			t.Fatal(err)
		}
		if !present && (err == nil || !strings.Contains(err.Error(), "missing upstream connector:shop")) {
			t.Fatalf("expected missing connector, got %v", err)
		}
	}
}
func TestUnselectedLocalEditUsesCommittedServerGraph(t *testing.T) {
	local := []Resource{graphSource(t, "final", `"x": TransformOutput("source")`), graphSource(t, "source", `"x": TransformOutput("missing")`)}
	remote := []Registered{{ID: "source-id", Type: "transform", Name: "source", HasCode: true}, {ID: "agent-id", Type: "agent", Name: "server_agent", HasCode: true, UpstreamIDs: []string{"source-id"}}}
	g, err := BuildGraph(context.Background(), local, remote)
	if err != nil {
		t.Fatal(err)
	}
	order, err := g.Order(map[string]bool{"transform:final": true})
	if err != nil || len(order) != 1 {
		t.Fatal(order, err)
	}
	if strings.Join(g.Downstream("transform:source"), ",") != "agent:server_agent,transform:final" {
		t.Fatal(g.Downstream("transform:source"))
	}
}
func TestReadReferencesUsesASTAliasesConstantsAndDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"refs.py": `from vortexify import TransformOutput as Output
UPSTREAM = Output("base.customers")
`,
		"main.py": `import vortexify as vx
from refs import UPSTREAM as source, Output
raise RuntimeError("must never execute")
@vx.vxfy_transform(inputs={"rows": source, "other": Output("base.other"), "count": vx.Input(int, default=1)})
def run(rows, count): return rows
`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := ReadReferences(context.Background(), Resource{Type: "transform", Name: "local", Path: dir})
	if err != nil || len(refs) != 2 || refs[0].Name != "base.customers" || refs[1].Name != "base.other" {
		t.Fatal(refs, err)
	}
}
func TestReadReferencesRejectsDynamicCalls(t *testing.T) {
	row := graphSource(t, "invalid", `"x": TransformOutput(get_name())`)
	if _, err := ReadReferences(context.Background(), row); err == nil {
		t.Fatal("dynamic source accepted")
	}
}
func TestTableReferenceNeedsPublishedTableAndCorrectConnector(t *testing.T) {
	row := graphSource(t, "final", "")
	source := `from vortexify import Table, vxfy_transform
@vxfy_transform(inputs={"rows": Table("shop.orders")})
def run(rows): return rows
`
	os.WriteFile(filepath.Join(row.Path, "main.py"), []byte(source), 0644)
	remote := []Registered{{ID: "c", Type: "connector", Name: "shop", HasCode: true}, {ID: "s", Type: "sync", Name: "orders", ConnectorID: "c", HasCode: true, UpstreamIDs: []string{"c"}}}
	g, err := BuildGraph(context.Background(), []Resource{row}, remote)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Order(map[string]bool{"transform:final": true}); err == nil {
		t.Fatal("committing code incorrectly created table data")
	}
	remote = append(remote, Registered{ID: "table", Type: "table", Name: "default", SyncID: "s", ConnectorID: "c", HasCode: true, UpstreamIDs: []string{"s"}})
	g, err = BuildGraph(context.Background(), []Resource{row}, remote)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Order(map[string]bool{"transform:final": true}); err != nil {
		t.Fatal(err)
	}
}

func TestTriggerConstantsAndAgentToolsUseSameUpstreamReferences(t *testing.T) {
	for _, example := range []struct {
		kind, source string
		want         int
	}{
		{"transform", `from vortexify import vxfy_transform, TriggerSchedule, TransformOutput
SCHEDULE = TriggerSchedule(triggers=[TransformOutput("source.named")])
@vxfy_transform(schedule=SCHEDULE)
def run(): return None
`, 1},
		{"agent", `from vortexify import Agent, ApiRunner, TransformOutput
agent = Agent(name="assistant", context=[TransformOutput("source.named")], tools=[ApiRunner("lookup")])
`, 2},
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "main.py"), []byte(example.source), 0644)
		refs, err := ReadReferences(context.Background(), Resource{Type: example.kind, Name: "example", Path: dir})
		if err != nil || len(refs) != example.want {
			t.Fatal(refs, err)
		}
	}
}
