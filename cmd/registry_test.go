package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"vortexify-cli/internal/config"
)

func TestRegistryEnvironmentDetailsAreScopedAndDoNotStartJobs(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	lookups := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			w.Write([]byte(`[{"id":"function","type":"api","name":"notes","version":2},{"id":"app","type":"app","name":"demo","version":1}]`))
		case "/api/v2/artifacts/functions/get_function_environment":
			lookups++
			if body["org_id"] != "org" || body["project_id"] != "project" || body["function_id"] != "function" || body["version"] != float64(2) {
				t.Error(body)
			}
			w.Write([]byte(`{"version":2,"image":"base-api","image_kind":"base","image_available":true,"status":"ready","ready":true,"lambda_name":"api-shared","compute":{"memory":512},"requirements":["pandas"]}`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org", "registry"}
	out, err := run(t, base...)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"ENVIRONMENT", "IMAGE", "base-api", "ready", "notes", "demo"} {
		if !strings.Contains(out, value) {
			t.Fatal(value, out)
		}
	}
	out, err = run(t, append(base, "--name", "notes")...)
	if err != nil || !strings.Contains(out, "Lambda: api-shared") || !strings.Contains(out, "Requirements: pandas") {
		t.Fatal(out, err)
	}
	out, err = run(t, append(base, "--json")...)
	if err != nil || !strings.Contains(out, `"environment":{"status":"ready"`) {
		t.Fatal(out, err)
	}
	if lookups != 3 {
		t.Fatal("non-functions were checked", lookups)
	}
}
