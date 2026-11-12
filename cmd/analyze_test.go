package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
)

func TestAnalysisQuerySources(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("query.sql", []byte("select 2 from orders"), 0600); err != nil {
		t.Fatal(err)
	}
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["project_id"] != "project" || body["org_id"] != "org" {
			t.Error("missing scope", body)
		}
		switch r.URL.Path {
		case "/api/v2/data/tables/list_tables":
			w.Write([]byte(`[{"id":"table-id","name":"default","display_name":"mes/orders/default","type":"sync","status":"ready"}]`))
		case "/api/v2/data/tables/start_analysis":
			queries = append(queries, body["query"].(string))
			if body["tables"].(map[string]any)["orders"] != "table-id" || body["deployed"] != true || body["limit"] != float64(5) {
				t.Error("changed analysis options", body)
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "job", "status": "pending"})
		case "/api/v2/execution/jobs/get_job_results":
			json.NewEncoder(w).Encode(map[string]any{"job_id": "job", "status": "success", "completed": true})
		case "/api/v2/data/tables/get_analysis":
			json.NewEncoder(w).Encode(map[string]any{"job_id": "job", "type": "table-analysis", "status": "success", "completed": true, "result": map[string]any{"preview": []map[string]any{{"answer": 42}}}})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org", "analyze", "--table", "orders=mes.orders", "--limit", "5", "--deployed"}
	for _, tc := range []struct {
		input, query string
		args         []string
	}{
		{"", "select 1 from orders", []string{"-c", "select 1 from orders", "--wait", "--json"}},
		{"", "select 2 from orders", []string{"--query-file", "query.sql", "--wait", "--json"}},
		{"select 3 from orders", "select 3 from orders", []string{"--query-file", "-", "--wait", "--json"}},
	} {
		out, _, err := runOrganizationCommand(tc.input, append(append([]string{}, base...), tc.args...)...)
		if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, "42") || queries[len(queries)-1] != tc.query {
			t.Fatal(out, err, queries)
		}
	}
	out, prompts, err := runOrganizationCommand("select\n 4 from orders;\nselect 5 from orders\n\\g\n\\q\n", base...)
	if err != nil || strings.Count(out, "42") != 2 || !strings.Contains(prompts, "vortexify->") || len(queries) != 5 || queries[3] != "select\n 4 from orders;" || queries[4] != "select 5 from orders" {
		t.Fatal(out, prompts, err, queries)
	}
	out, _, err = runOrganizationCommand("", "--api-url", server.URL, "--org-id", "org", "analyze", "--job", "job", "--wait", "--json")
	if err != nil || !strings.Contains(out, "42") || len(queries) != 5 {
		t.Fatal(out, err)
	}
	_, _, err = runOrganizationCommand("", "--api-url", server.URL, "--org-id", "org", "analyze", "--table", "orders=unknown.orders", "-c", "select 1")
	if err == nil || !strings.Contains(err.Error(), "not found in this project") || len(queries) != 5 {
		t.Fatal("submitted an analysis with an unknown reference", err, queries)
	}
}

func TestAnalysisRejectsAmbiguousOrEmptySources(t *testing.T) {
	for _, args := range [][]string{
		{"--query", "select 1", "--query-file", "query.sql"},
		{"--query", "select 1", "--interactive"},
		{"--job", "job", "--query", "select 1"},
		{"--query", " "},
		{"--query-file", ""},
		{"--query-file", "-"},
		{"--query", "select 1", "--limit", "0"},
	} {
		_, _, err := runOrganizationCommand("", append([]string{"analyze", "--table", "orders=table"}, args...)...)
		if err == nil {
			t.Fatal("accepted invalid query flags", args)
		}
	}
}

func TestAnalysisSessionBoundariesAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		input     string
		count     int
		wantError bool
	}{
		{"select 'multi;\nline';\n\\q\n", 1, false},
		{"select $$multi;\nline$$;\n\\q\n", 1, false},
		{"select 1 /* ;\n nested /* ; */ */; -- done\n\\q\n", 1, false},
		{"select 1\n\\r\nselect 2;\n\\q\n", 1, false},
		{"select 1\n", 0, true},
		{"select 1;\nselect 2;\n\\q\n", 2, false},
		{"\\g\n\\q\n", 0, false},
		{"select '\n\\q\n';\n\\q\n", 1, false},
	} {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			cmd.SetIn(strings.NewReader(tc.input))
			var out strings.Builder
			cmd.SetErr(&out)
			count := 0
			err := analysisSession(cmd, func(query string) error {
				count++
				return fmt.Errorf("query failed")
			})
			if (err != nil) != tc.wantError || count != tc.count {
				t.Fatal(err, count, out.String())
			}
		})
	}
}

func TestSQLPromptCompletion(t *testing.T) {
	for _, tc := range []struct {
		sql      string
		complete bool
	}{
		{`SELECT 'it''s;';`, true},
		{`SELECT 'open;`, false},
		{`SELECT E'it\'s;';`, true},
		{`SELECT "semi;column"; -- comment`, true},
		{`SELECT $tag$semi;colon$tag$;`, true},
		{`SELECT $tag$open;`, false},
		{`SELECT column$part$ FROM orders;`, true},
		{`SELECT 1; /* open`, false},
		{`SELECT 1; /* outer /* inner */ done */`, true},
		{`-- only a comment;`, false},
	} {
		if complete, _ := sqlQueryState(tc.sql); complete != tc.complete {
			t.Errorf("%q: got %t, want %t", tc.sql, complete, tc.complete)
		}
	}
}

func TestRunInlineAndStdinInputs(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(".", config.Default("project", "demo", ""))
	path := filepath.Join(t.TempDir(), "inputs.json")
	os.WriteFile(path, []byte(`{"limit":3,"active":false}`), 0600)
	starts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			w.Write([]byte(`[{"id":"resource","type":"api","name":"orders"}]`))
		case "/api/v2/execution/jobs/start_resource_job":
			starts++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			inputs := body["inputs"].(map[string]any)
			if inputs["limit"] != float64(3) || inputs["active"] != false {
				t.Error("inputs changed", inputs)
			}
			w.Write([]byte(`{"job_id":"job","status":"pending"}`))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org", "run", "api", "orders"}
	for _, flags := range [][]string{{"--inputs", `{"limit":3,"active":false}`}, {"--inputs-file", "-"}, {"--inputs-file", path}} {
		if _, _, err := runOrganizationCommand(`{"limit":3,"active":false}`, append(append([]string{}, base...), flags...)...); err != nil {
			t.Fatal(err)
		}
	}
	for _, flags := range [][]string{{"--inputs", "[]"}, {"--inputs", "null"}, {"--inputs", "{"}, {"--inputs", ""}, {"--inputs", "{}", "--inputs-file", path}} {
		if _, _, err := runOrganizationCommand("", append(append([]string{}, base...), flags...)...); err == nil {
			t.Fatal("accepted invalid inputs", flags)
		}
	}
	if starts != 3 {
		t.Fatal("started a job with invalid inputs", starts)
	}
}

func TestAnalysisSelectsDatabaseEnvironmentWithoutProductionFallback(t *testing.T) {
	tables := []api.TableSummary{
		{ID: "preview", Type: "database", Name: "public.reason_reviews", DisplayName: "operations/public.reason_reviews [preview]", Environment: "preview", ResourceReference: "vortexify://table/database/operations/preview/public.reason_reviews"},
		{ID: "origin", Type: "database", Name: "public.reason_reviews", DisplayName: "operations/public.reason_reviews [origin]", Environment: "origin", ResourceReference: "vortexify://table/database/operations/origin/public.reason_reviews"},
		{ID: "sync", Type: "sync", Name: "default", DisplayName: "mes/orders/default"},
	}
	for _, deployed := range []bool{false, true} {
		want := "preview"
		if deployed {
			want = "origin"
		}
		for _, reference := range []string{"operations.reason_reviews", "database:operations.reason_reviews", want, tables[map[bool]int{false: 0, true: 1}[deployed]].ResourceReference} {
			table, err := resolveAnalysisTable(tables, reference, deployed)
			if err != nil || table.ID != want {
				t.Fatal(reference, deployed, table, err)
			}
		}
		other := "origin"
		if deployed {
			other = "preview"
		}
		if _, err := resolveAnalysisTable(tables, other, deployed); err == nil {
			t.Fatal("accepted a conflicting explicit environment", other, deployed)
		}
		if _, err := resolveAnalysisTable(tables, "mes.orders", deployed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := resolveAnalysisTable(tables[1:2], "operations.reason_reviews", false); err == nil {
		t.Fatal("fell back to origin without preview")
	}
}

func TestAnalyzeSubmitsTheSelectedDatabaseAndSyncedEnvironments(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/v2/data/tables/list_tables":
			w.Write([]byte(`[{"id":"preview","type":"database","name":"public.reason_reviews","display_name":"operations/public.reason_reviews [preview]","environment":"preview","status":"registered"},{"id":"origin","type":"database","name":"public.reason_reviews","display_name":"operations/public.reason_reviews [origin]","environment":"origin","status":"registered"},{"id":"orders","type":"sync","name":"default","display_name":"mes/orders/default","status":"ready"}]`))
		case "/api/v2/data/tables/start_analysis":
			calls++
			want := "preview"
			if body["deployed"] == true {
				want = "origin"
			}
			inputs := body["tables"].(map[string]any)
			if inputs["reviews"] != want || inputs["orders"] != "orders" {
				t.Error(body)
			}
			w.Write([]byte(`{"id":"job","status":"pending"}`))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org", "analyze", "--table", "reviews=operations.reason_reviews", "--table", "orders=mes.orders", "-c", "select * from reviews join orders on true", "--json"}
	for _, deployed := range []bool{false, true} {
		args := append([]string{}, base...)
		if deployed {
			args = append(args, "--deployed")
		}
		if out, err := run(t, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
