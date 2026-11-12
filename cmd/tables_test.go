package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
)

func TestTablesUsesExistingScopedServicesAndReportsDiscovery(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	var selectedJob any
	var selectedDeployment any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.Method != "POST" || body["project_id"] != "project" || body["org_id"] != "org" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing API scope", body)
		}
		switch r.URL.Path {
		case "/api/v2/data/tables/list_tables":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "orders", "name": "sample_orders", "type": "upload"},
				{"id": "pending", "name": "pending", "type": "transform"},
				{"id": "old", "name": "legacy", "type": "upload"},
				{"id": "failed", "name": "failed", "type": "upload"},
				{"id": "db", "name": "public.users", "type": "database", "environment": "origin"},
				{"id": "db-preview", "name": "public.users", "type": "database", "environment": "preview"},
			})
		case "/api/v2/data/tables/get_table":
			if body["include_analysis"] != true {
				t.Error("table details must request saved key and relationship findings", body)
				http.Error(w, "analysis not requested", http.StatusBadRequest)
				return
			}
			selectedJob, selectedDeployment = body["job_id"], body["deployed"]
			if body["table_id"] != "orders" {
				t.Error(body)
			}
			w.Write([]byte(`{"table":{"id":"orders","name":"sample_orders","type":"upload"},"schema":{"order_id":{"type":"integer"}},"key_analysis":{"completed":true,"keys":[{"columns":["order_id"],"unique_percentage":100,"grain_description":"One row per order"}]},"relationship_evaluations":[{"other_table_id":"pending","status":"completed","outcome":"relationships_found","relationships":[{"source_table_id":"orders","target_table_id":"pending","source_columns":["customer_name"],"target_columns":["name"],"cardinality":"many_to_one","description":"Each order belongs to a customer"}]}]}`))
		case "/api/v2/platform/projects/get_project_knowledge_graph_status":
			w.Write([]byte(`{"tables":[{"table_id":"orders","status":"running","phase":"primary_keys","job_id":"kg-job","keys_complete":false,"relationships":{"total":2,"completed":1,"pending":1},"waiting_for_table_ids":["pending"],"pending_job_ids":["kg-next"],"error":"agent retry"}]}`))
		case "/api/v2/execution/jobs/list_jobs":
			if body["type"] != "schema-discovery" || body["resource_type"] != "table" || body["resource_id"] == nil {
				t.Error("unexpected job filters", body)
			}
			if body["resource_id"] == nil || body["resource_id"] == "" {
				http.Error(w, "badly formed hexadecimal UUID string", http.StatusBadRequest)
				return
			}
			// Deliberately unsorted. Raw secrets/configuration must never be printed.
			w.Write([]byte(`[
                {"id":"published", "resource_id":"orders", "status":"success", "created_at":"2026-10-20T00:00:00Z", "config":{"watcher_complete":true}},
                {"id":"refresh", "resource_id":"orders", "status":"running", "created_at":"Wed, 21 Oct 2026 00:00:00 GMT", "config":{"watcher_error":"permission denied", "secret":"do-not-print"}},
                {"id":"preview", "resource_id":"orders", "status":"success", "created_at":"2026-10-22T00:00:00Z", "config":{"preview":true,"watcher_complete":true,"project_table_publication":true}},
                {"id":"pending-job", "resource_id":"pending", "status":"created", "config":{}},
                {"id":"failed-job", "resource_id":"failed", "status":"failed", "config":{"watcher_error":"schema failed"}}
            ]`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org", "tables"}
	out, err := run(t, append(base, "--json")...)
	if err != nil {
		t.Fatal(out, err)
	}
	var tables []api.TableSummary
	if err := json.Unmarshal([]byte(out), &tables); err != nil {
		t.Fatal(out, err)
	}
	found := map[string]api.TableSummary{}
	for _, table := range tables {
		found[table.ID] = table
	}
	if len(tables) != 6 || found["orders"].Status != "ready" || found["orders"].DiscoveryJobID != "preview" || found["pending"].Status != "schema pending" || found["failed"].Status != "schema failed" || found["old"].Status != "registered; publication unknown" || found["db"].Status != "registered" || found["db-preview"].Status != "registered" || found["pending"].KnowledgeGraph.Status != "not_applicable" || found["db-preview"].KnowledgeGraph.Status != "not_applicable" {
		t.Fatal(tables)
	}
	if found["orders"].KnowledgeGraph == nil || found["orders"].KnowledgeGraph.Phase != "primary_keys" || found["orders"].KnowledgeGraph.Relationships["pending"] != 1 || found["orders"].KnowledgeGraph.JobID != "kg-job" {
		t.Fatal(found["orders"])
	}
	if strings.Contains(out, "do-not-print") {
		t.Fatal("raw config leaked", out)
	}
	out, err = run(t, append(base, "--name", "sample_orders", "--json")...)
	if err != nil {
		t.Fatal(out, err)
	}
	tables = nil
	if err = json.Unmarshal([]byte(out), &tables); err != nil || len(tables) != 1 || tables[0].Status != "ready" || tables[0].DiscoveryJobID != "preview" || tables[0].KnowledgeGraph == nil {
		t.Fatal(out, err)
	}
	out, err = run(t, append(base, "--name", "orders")...)
	if err != nil || !strings.Contains(out, "SCHEMA") || !strings.Contains(out, "complete") || !strings.Contains(out, "PRIMARY KEYS") || !strings.Contains(out, "RELATIONSHIPS") || !strings.Contains(out, "running") || strings.Contains(out, "kg-job") || strings.Contains(out, "primary keys complete:") {
		t.Fatal(out, err)
	}
	detailArgs := []string{"--api-url", server.URL, "--org-id", "org", "table", "sample_orders"}
	out, err = run(t, detailArgs...)
	if err != nil {
		t.Fatal(out, err)
	}
	for _, expected := range []string{"Jobs\n", "Knowledge graph  kg-job", "order_id — 100.00% unique", "customer_name → pending.name  many-to-one", "Each order belongs to a customer", "1 pair evaluated · 1 relationship found"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("missing %q in %s", expected, out)
		}
	}
	out, err = run(t, append(detailArgs, "--verbose")...)
	if err != nil || !strings.Contains(out, "Grain: One row per order") {
		t.Fatal(out, err)
	}
	out, err = run(t, append(detailArgs, "--json")...)
	var details api.TableDetails
	if err != nil {
		t.Fatal(out, err)
	}
	if err = json.Unmarshal([]byte(out), &details); err != nil || len(details.KeyAnalysis.Keys) != 1 || details.Table.KnowledgeGraph.JobID != "kg-job" {
		t.Fatal(out, err)
	}
	out, err = run(t, append(detailArgs, "--deployed")...)
	if err != nil || selectedDeployment != true || selectedJob != nil {
		t.Fatal(out, err, selectedDeployment, selectedJob)
	}
	out, err = run(t, append(detailArgs, "--job", "producer-job")...)
	if err != nil || selectedJob != "producer-job" || selectedDeployment != false {
		t.Fatal(out, err, selectedDeployment, selectedJob)
	}
	if _, err = run(t, append(detailArgs, "--deployed", "--job", "producer-job")...); err == nil {
		t.Fatal("conflicting output selectors accepted")
	}
	out, err = run(t, "--api-url", server.URL, "--org-id", "org", "table", "pending")
	if err != nil || strings.Contains(out, "None found") || !strings.Contains(out, "Data: not ready") || !strings.Contains(out, "Schema: pending") {
		t.Fatal(out, err)
	}

}

func TestTablesDeleteUsesExistingScopedDeleteRoute(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(".", config.Default("project", "demo", ""))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/api/v2/data/tables/list_tables" {
			w.Write([]byte(`[{"id":"table-id","name":"sample_orders","type":"upload","status":"ready"}]`))
			return
		}
		if r.URL.Path != "/api/v2/artifacts/resources/delete_resource" || body["resource_type"] != "table" || body["resource_id"] != "table-id" || body["project_id"] != "project" || body["org_id"] != "org" {
			t.Error(r.URL.Path, body)
		}
		w.Write([]byte(`{"deleted":true}`))
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "tables", "delete", "sample_orders")
	if err != nil || !strings.Contains(out, "Deleted uploaded table sample_orders") {
		t.Fatal(out, err)
	}
}

func TestAuthoritativeInventoryIncludesDatabaseEnvironmentsAndExcludesDerivedGraph(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/data/tables/list_tables" {
			t.Error("unexpected endpoint", r.URL.Path)
		}
		w.Write([]byte(`[
			{"id":"reviews","name":"reason_reviews","type":"database","environment":"preview","status":"registered"},
			{"id":"audit","name":"review_audit","type":"database","environment":"preview","status":"registered"},
			{"id":"tasks","name":"follow_up_tasks","type":"database","environment":"preview","status":"registered"},
			{"id":"origin","name":"reason_reviews","type":"database","environment":"origin","status":"registered"},
			{"id":"derived","name":"metrics","type":"transform","status":"ready","knowledge_graph":{"status":"pending"}}
		]`))
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org", "tables"}
	out, err := run(t, append(base, "--json")...)
	if err != nil {
		t.Fatal(out, err)
	}
	var rows []api.TableSummary
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 5 {
		t.Fatal(out, err)
	}
	for _, row := range rows {
		if row.KnowledgeGraph == nil || row.KnowledgeGraph.Status != "not_applicable" {
			t.Fatal("excluded table has graph work", row)
		}
	}
	out, err = run(t, base...)
	if err != nil {
		t.Fatal(out, err)
	}
	for _, name := range []string{"reason_reviews [preview]", "reason_reviews [origin]", "review_audit", "follow_up_tasks", "metrics", "not applicable"} {
		if !strings.Contains(out, name) {
			t.Fatalf("missing %q in %s", name, out)
		}
	}
	if strings.Contains(out, "pending") {
		t.Fatal("excluded tables should never display pending graph work", out)
	}
}
