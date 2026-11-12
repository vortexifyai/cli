package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
)

func TestRunAPIResponseOnlyWaitsWithoutProgressOnStdout(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			w.Write([]byte(`[{"id":"api","type":"api","name":"orders"}]`))
		case "/api/v2/execution/jobs/start_resource_job":
			w.Write([]byte(`{"job_id":"job","type":"function-run","status":"running","completed":false}`))
		case "/api/v2/execution/jobs/get_job_results":
			polls++
			w.Write([]byte(`{"job_id":"job","type":"function-run","status":"success","completed":true,"response":{"result":{"result":5},"success":false}}`))
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "run", "api", "orders", "--response-only")
	if err != nil || strings.TrimSpace(out) != `{"result":{"result":5},"success":false}` || polls != 1 {
		t.Fatal(out, err, polls)
	}
}

func TestAPIResponseOnlyPreservesJSONValuesAndRejectsFailures(t *testing.T) {
	for _, encoded := range []string{`null`, `false`, `0`, `""`, `[1,2]`, `{"result":{"result":5}}`} {
		var value any
		json.Unmarshal([]byte(encoded), &value)
		result := api.Execution{"type": "function-run", "status": "success", "completed": true, "response": value}
		var out bytes.Buffer
		if err := printAPIResponse(&out, result); err != nil || strings.TrimSpace(out.String()) != encoded {
			t.Fatal(encoded, out.String(), err)
		}
		out.Reset()
		if err := printExecution(&out, result, false); err != nil || !strings.Contains(out.String(), "Result:\n") {
			t.Fatal(out.String(), err)
		}
		var decoded any
		if err := json.Unmarshal([]byte(strings.SplitN(out.String(), "Result:\n", 2)[1]), &decoded); err != nil {
			t.Fatal(err)
		}
	}
	for _, result := range []api.Execution{
		{"status": "failed", "completed": true, "response": nil},
		{"status": "stopped", "completed": true},
		{"status": "running", "completed": false},
		{"status": "success", "completed": true},
	} {
		var out bytes.Buffer
		if err := printAPIResponse(&out, result); err == nil || out.Len() != 0 {
			t.Fatal(result, out.String(), err)
		}
	}
}
