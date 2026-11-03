package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vortexify-cli/internal/config"
)

func TestRunSyncPreviewUsesSampleOperation(t *testing.T) {
	for _, wait := range []bool{false, true} {
		t.Run(map[bool]string{false: "async", true: "wait"}[wait], func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("VORTEXIFY_TOKEN", "token")
			config.Save(".", config.Default("project", "demo", ""))
			starts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/list_resources":
					w.Write([]byte(`[{"id":"c","type":"connector","name":"postgres"},{"id":"s","type":"sync","name":"items","connector_id":"c"}]`))
				case "/api/v2/execution/jobs/start_resource_job":
					starts++
					if body["action"] != "preview" || body["resource_id"] != "s" || body["resource_type"] != "sync" || body["version"] != float64(3) {
						t.Error("wrong preview request", body)
					}
					w.Write([]byte(`{"job_id":"job","type":"sync-preview","status":"pending"}`))
				case "/api/v2/execution/jobs/get_job_results":
					w.Write([]byte(`{"job_id":"job","type":"sync-preview","status":"success","completed":true,"result":{"preview":[{"id":1}],"avg_row_size_bytes":12.5}}`))
				default:
					t.Error("unexpected request", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			args := []string{"--api-url", server.URL, "--org-id", "org", "run", "sync", "postgres/items", "--preview", "--version", "3"}
			if wait {
				args = append(args, "--wait", "--timeout", "1s")
			}
			out, err := run(t, args...)
			if err != nil || starts != 1 {
				t.Fatal(out, err, starts)
			}
			if wait && (!strings.Contains(out, "Estimated row size: 12.5 bytes") || !strings.Contains(out, `"id": 1`)) {
				t.Fatal(out)
			}
		})
	}
}

func TestRunSyncPreviewRejectsIncompatibleFlags(t *testing.T) {
	for _, args := range [][]string{
		{"run", "transform", "test", "--preview"},
		{"run", "sync", "postgres/items", "--preview", "--rebuild-downstream"},
		{"run", "sync", "postgres/items", "--preview", "--inputs", "{}"},
	} {
		_, err := run(t, args...)
		if err == nil || !strings.Contains(err.Error(), "--preview requires a sync") {
			t.Fatal(args, err)
		}
	}
}
