package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vortexify-cli/internal/config"
)

func TestConnectorChecksAndDiscoveryUseSavedVersions(t *testing.T) {
	for _, action := range []string{"check", "discover"} {
		for _, wait := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "-async", true: "-wait"}[wait], func(t *testing.T) {
				t.Chdir(t.TempDir())
				t.Setenv("VORTEXIFY_TOKEN", "token")
				config.Save(".", config.Default("project", "demo", ""))
				starts, polls := 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["org_id"] != "org" || body["project_id"] != "project" || r.Header.Get("Authorization") != "Bearer token" {
						t.Error("missing scope/authentication", body)
					}
					switch r.URL.Path {
					case "/api/v2/artifacts/resources/get_resource_graph":
						w.Write([]byte(`[{"id":"connector-id","type":"connector","name":"postgres"}]`))
					case "/api/v2/data/connectors/check_connection", "/api/v2/data/connectors/discover":
						starts++
						expected := "/api/v2/data/connectors/discover"
						if action == "check" {
							expected = "/api/v2/data/connectors/check_connection"
						}
						if r.URL.Path != expected || body["connector_id"] != "connector-id" {
							t.Error("wrong connector operation", r.URL.Path, body)
						}
						if wait && body["version"] != float64(3) {
							t.Error("missing explicit version", body)
						}
						if !wait && body["version"] != nil {
							t.Error("latest version should be selected by server", body)
						}
						if body["configuration"] != nil {
							t.Error("configuration should stay on server")
						}
						w.Write([]byte(`{"id":"job","status":"pending"}`))
					case "/api/v2/execution/jobs/get_job_results":
						polls++
						if body["job_id"] != "job" {
							t.Error(body)
						}
						if action == "check" {
							w.Write([]byte(`{"job_id":"job","type":"connector-check","status":"success","completed":true,"result":{"connected":true}}`))
						} else {
							w.Write([]byte(`{"job_id":"job","type":"connector-discovery","status":"success","completed":true,"result":{"tables":[{"name":"mes.orders","schema":[{"name":"id","type":"bigint","nullable":false}]},{"name":"wms.inventory","schema":[{"name":"lot","type":"text","nullable":true}]}]}}`))
						}
					default:
						t.Error("unexpected side effect", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				args := []string{"--api-url", server.URL, "--org-id", "org", "connector", "postgres", "--" + action}
				if wait {
					args = append(args, "--wait", "--version", "3", "--timeout", "1s")
				}
				out, err := run(t, args...)
				if err != nil || starts != 1 {
					t.Fatal(out, err, starts)
				}
				if !wait && (polls != 0 || !strings.Contains(out, "Job: job")) {
					t.Fatal(out, polls)
				}
				if wait && action == "check" && !strings.Contains(out, "Connection: successful") {
					t.Fatal(out)
				}
				if wait && action == "discover" {
					for _, value := range []string{"Discovered tables: 2", "mes.orders", "wms.inventory", "id: bigint NOT NULL", "lot: text"} {
						if !strings.Contains(out, value) {
							t.Fatal(out, value)
						}
					}
				}
			})
		}
	}
}

func TestConnectorOperationFailedJobAndJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(".", config.Default("project", "demo", ""))
	starts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			w.Write([]byte(`[{"id":"connector-id","type":"connector","name":"postgres"}]`))
		case "/api/v2/data/connectors/check_connection":
			starts++
			w.Write([]byte(`{"job_id":"job","status":"pending"}`))
		case "/api/v2/execution/jobs/get_job_results":
			w.Write([]byte(`{"job_id":"job","type":"connector-check","status":"failed","completed":true,"error":"Connection failed"}`))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "connector", "postgres", "--check", "--wait", "--json")
	if err == nil || starts != 1 || !json.Valid([]byte(out)) || !strings.Contains(out, `"status":"failed"`) {
		t.Fatal(out, err, starts)
	}
}

func TestConnectorOperationFlagsRejectInvalidCombinations(t *testing.T) {
	for _, flags := range [][]string{
		{}, {"--check", "--discover"}, {"--configure", "--check"},
		{"--configure", "--wait"}, {"--configure", "--version", "3"},
		{"--check", "--version", "0"}, {"--discover", "--version", "-1"},
		{"--check", "--timeout", "0s"},
	} {
		out, err := run(t, append([]string{"connector", "postgres"}, flags...)...)
		if err == nil || strings.Contains(err.Error(), "vortexify.project.yaml") {
			t.Fatal("invalid flags were not rejected before workspace/API access", flags, out, err)
		}
	}
}
