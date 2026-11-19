package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"vortexify-cli/internal/config"
)

func TestTableJSONRetainsValidatedMeasurementDetails(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/data/tables/list_tables":
			w.Write([]byte(`[{"id":"orders","name":"orders","type":"sync","status":"ready"}]`))
		case "/api/v2/platform/projects/get_project_knowledge_graph_status":
			w.Write([]byte(`{"tables":[]}`))
		case "/api/v2/execution/jobs/list_jobs":
			w.Write([]byte(`[]`))
		case "/api/v2/data/tables/get_table":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["deployed"] != true {
				t.Error("production selection missing", body)
			}
			w.Write([]byte(`{"table":{"id":"orders","name":"orders","type":"sync","knowledge_graph":{"environment":"production","status":"completed"}},"key_analysis":{"environment":"production","completed":true,"keys":[{"columns":["id"],"unique_percentage":97,"measurement_query_id":"key-query","measurement":{"analysis_job_id":"key-run","values":{"total_rows":100},"error":"last refresh failed"}}]},"relationship_evaluations":[{"environment":"production","other_table_id":"customers","status":"completed","relationships":[{"source_table_id":"orders","target_table_id":"customers","query_id":"join-query","left_match_percentage":0,"right_match_percentage":80,"measurement":{"analysis_job_id":"join-run","values":{"left_eligible_rows":100}}}]}]}`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
		}
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "table", "orders", "--deployed", "--json")
	if err != nil {
		t.Fatal(out, err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(out, err)
	}
	analysis := result["key_analysis"].(map[string]any)
	key := analysis["keys"].([]any)[0].(map[string]any)
	measurement := key["measurement"].(map[string]any)
	if analysis["environment"] != "production" || key["measurement_query_id"] != "key-query" || measurement["analysis_job_id"] != "key-run" || measurement["error"] != "last refresh failed" {
		t.Fatal(out)
	}
	evaluation := result["relationship_evaluations"].([]any)[0].(map[string]any)
	relation := evaluation["relationships"].([]any)[0].(map[string]any)
	if evaluation["environment"] != "production" || relation["query_id"] != "join-query" || relation["left_match_percentage"] != float64(0) || relation["right_match_percentage"] != float64(80) || relation["measurement"] == nil {
		t.Fatal(out)
	}
}
