package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"vortexify-cli/internal/config"
)

func TestDeleteResourceSelectionAndScope(t *testing.T) {
	for _, kind := range []string{"api", "transform", "app", "agent", "connector", "sync", "database", "table"} {
		for _, selector := range []string{"name", "id", "reference"} {
			t.Run(kind+"/"+selector, func(t *testing.T) {
				t.Chdir(t.TempDir())
				t.Setenv("VORTEXIFY_TOKEN", "token")
				if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile("source.py", []byte("keep local edits"), 0600); err != nil {
					t.Fatal(err)
				}
				name := "orders"
				if kind == "sync" {
					name = "shop/orders"
				}
				ref := "vortexify://" + kind + "/" + name
				selected := map[string]string{"name": name, "id": "target", "reference": ref}[selector]
				deleted := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if r.Method != "POST" || body["project_id"] != "project" || body["org_id"] != "org" || r.Header.Get("Authorization") != "Bearer token" {
						t.Error("missing scope", body)
					}
					switch r.URL.Path {
					case "/api/v2/artifacts/resources/list_resources":
						json.NewEncoder(w).Encode([]map[string]any{
							{"id": "parent", "type": "connector", "name": "shop"},
							{"id": "target", "type": kind, "name": "orders", "connector_id": "parent", "resource_reference": ref},
							{"id": "other", "type": "api", "name": "unrelated"},
						})
					case "/api/v2/data/tables/list_tables":
						json.NewEncoder(w).Encode([]map[string]any{{"id": "target", "type": "upload", "name": "orders", "status": "ready", "resource_reference": ref}})
					case "/api/v2/artifacts/resources/delete_resource":
						deleted++
						if body["resource_id"] != "target" || body["resource_type"] != kind {
							t.Error("wrong deletion", body)
						}
						json.NewEncoder(w).Encode(map[string]any{"resource_id": "target", "type": kind, "deleted": true})
					default:
						t.Error("unexpected endpoint", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				out, err := run(t, "--api-url", server.URL, "--org-id", "org", "delete", kind, selected, "--yes", "--json")
				if err != nil {
					t.Fatal(out, err)
				}
				var result map[string]any
				if err := json.Unmarshal([]byte(out), &result); err != nil || result["deleted"] != true || deleted != 1 {
					t.Fatal(out, err, deleted)
				}
				contents, err := os.ReadFile("source.py")
				if err != nil || string(contents) != "keep local edits" {
					t.Fatal("local source changed", err)
				}
			})
		}
	}
}

func TestDeleteConfirmationAndServerFailures(t *testing.T) {
	for _, tc := range []struct {
		name, input, response, want string
		status                      int
		calls                       int
	}{
		{name: "confirmed", input: "yes\n", response: `{"deleted":true}`, calls: 1},
		{name: "short confirmation", input: "Y\n", response: `{"deleted":true}`, calls: 1},
		{name: "declined", input: "n\n", want: "deletion canceled"},
		{name: "default no", input: "\n", want: "deletion canceled"},
		{name: "no stdin", want: "confirmation required"},
		{name: "deployed", input: "y\n", status: 400, response: `{"error":"Resource is deployed; undeploy before deleting"}`, want: "undeploy", calls: 1},
		{name: "not deleted", input: "y\n", response: `{"deleted":false}`, want: "did not confirm", calls: 1},
		{name: "invalid result", input: "y\n", response: `invalid`, want: "invalid", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("VORTEXIFY_TOKEN", "token")
			if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/list_resources":
					w.Write([]byte(`[{"id":"target","type":"api","name":"orders"}]`))
				case "/api/v2/artifacts/resources/delete_resource":
					calls++
					if tc.status != 0 {
						w.WriteHeader(tc.status)
					}
					w.Write([]byte(tc.response))
				default:
					t.Error("unexpected call (must never undeploy)", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			command := NewRootCommand()
			var out, stderr bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&stderr)
			command.SetIn(strings.NewReader(tc.input))
			command.SetArgs([]string{"--api-url", server.URL, "--org-id", "org", "delete", "api", "orders", "--json"})
			err := command.ExecuteContext(context.Background())
			if tc.want == "" {
				if err != nil || !strings.Contains(out.String(), `"deleted":true`) {
					t.Fatal(out.String(), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal("expected", tc.want, "got", err)
			}
			if calls != tc.calls {
				t.Fatal("deletion calls", calls)
			}
			if !strings.Contains(stderr.String(), "[y/N]") {
				t.Fatal("missing confirmation", stderr.String())
			}
		})
	}
}

func TestDeleteRejectsUnsafeSelections(t *testing.T) {
	for _, tc := range []struct{ name, kind, reference, want string }{
		{"ambiguous sync", "sync", "orders", "ambiguous sync"},
		{"wrong type", "api", "db", "not registered"},
		{"missing", "api", "missing", "not registered"},
		{"managed table", "table", "managed", "only uploaded tables"},
		{"unsupported", "secret", "orders", "unsupported resource type"},
		{"empty", "api", "", "required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("VORTEXIFY_TOKEN", "token")
			if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/list_resources":
					w.Write([]byte(`[{"id":"a","type":"connector","name":"shop"},{"id":"b","type":"connector","name":"other"},{"id":"s1","type":"sync","name":"orders","connector_id":"a"},{"id":"s2","type":"sync","name":"orders","connector_id":"b"},{"id":"db","type":"database","name":"orders"}]`))
				case "/api/v2/data/tables/list_tables":
					w.Write([]byte(`[{"id":"managed","type":"database","name":"managed","status":"ready"}]`))
				default:
					t.Error("unexpected mutation", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			out, err := run(t, "--api-url", server.URL, "--org-id", "org", "delete", tc.kind, tc.reference, "--yes")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(out, err)
			}
		})
	}
}

func TestTablesDeleteRemoved(t *testing.T) {
	_, err := run(t, "tables", "delete", "orders")
	if err == nil {
		t.Fatal("tables delete should be rejected")
	}
}
