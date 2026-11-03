package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"vortexify-cli/internal/config"
)

func TestFilePreviewCommands(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(root, config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["project_id"] != "project" || body["org_id"] != "org" {
			t.Error(body)
		}
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			w.Write([]byte(`[{"id":"agent-id","name":"support","type":"agent"}]`))
		case "/api/v2/artifacts/resources/get_resource_file_previews":
			if body["resource_id"] != "agent-id" || body["version"] != float64(3) || body["chars"] != float64(1000) {
				t.Error(body)
			}
			w.Write([]byte(`{"files":[{"file_id":"file","name":"shipping-policy.md","text":"Ships in two days","truncated":false}],"omitted":0}`))
		case "/api/v2/artifacts/resources/get_file_preview":
			if body["file_id"] != "file" || body["chars"] != float64(2000) {
				t.Error(body)
			}
			w.Write([]byte(`{"file_id":"file","name":"shipping-policy.md","text":"Ships in two days","truncated":true}`))
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org"}
	out, err := run(t, append(base, "files", "agent", "support", "--version", "3")...)
	if err != nil || !strings.Contains(out, "shipping-policy.md") || strings.Contains(out, "[file]") || !strings.Contains(out, "Ships in two days") {
		t.Fatal(out, err)
	}
	out, err = run(t, append(base, "files", "preview", "file", "--chars", "2000", "--json")...)
	if err != nil || !strings.Contains(out, `"truncated":true`) {
		t.Fatal(out, err)
	}
	before := requests
	if _, err = run(t, append(base, "files", "preview", "file", "--chars", "999999")...); err == nil {
		t.Fatal("unbounded preview accepted")
	}
	if requests != before {
		t.Fatal("invalid request reached API")
	}
}

func TestFilesSearchUsesOwnerScopeAndUploadedNames(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(".", config.Default("project", "demo", ""))
	searches, lookups := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["org_id"] != "org" || body["project_id"] != "project" {
			t.Error("missing scope", body)
		}
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			w.Write([]byte(`[{"id":"agent-id","name":"support","type":"agent"},{"id":"connector-id","name":"mes","type":"connector"}]`))
		case "/api/v2/artifacts/agents/search_files", "/api/v2/data/connectors/search_files":
			searches++
			owner, id := "agent_id", "agent-id"
			if strings.Contains(r.URL.Path, "connectors") {
				owner, id = "connector_id", "connector-id"
			}
			if body[owner] != id || body["query"] != "shipping rules" || body["version"] != float64(3) || body["limit"] != float64(5) {
				t.Error("wrong search payload", body)
			}
			if owner == "agent_id" {
				w.Write([]byte(`[{"file_id":"internal-file-id","name":"shipping-policy.md","text":"Ships in two days","page":2,"section":"Shipping"}]`))
			} else {
				// Legacy search results need one filename lookup per distinct file.
				w.Write([]byte(`[{"file_id":"internal-file-id","text":"Ships in two days"},{"file_id":"internal-file-id","text":"No weekend shipping"}]`))
			}
		case "/api/v2/artifacts/resources/get_file_preview":
			lookups++
			if body["file_id"] != "internal-file-id" || body["chars"] != float64(1) {
				t.Error(body)
			}
			w.Write([]byte(`{"file_id":"internal-file-id","name":"shipping-policy.md","text":"S"}`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, owner := range [][]string{{"agent", "support"}, {"connector", "mes"}} {
		args := []string{"--api-url", server.URL, "--org-id", "org", "files", owner[0], owner[1], "--search", "shipping rules", "--version", "3", "--limit", "5"}
		out, err := run(t, args...)
		if err != nil || !strings.Contains(out, "shipping-policy.md") || !strings.Contains(out, "Ships in two days") || strings.Contains(out, "internal-file-id") {
			t.Fatal(out, err)
		}
		out, err = run(t, append(args, "--json")...)
		if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, `"name":"shipping-policy.md"`) {
			t.Fatal(out, err)
		}
	}
	if searches != 4 || lookups != 2 {
		t.Fatal(searches, lookups)
	}
}

func TestFilesSearchValidatesBeforeRequests(t *testing.T) {
	for _, flags := range [][]string{{"--search", " "}, {"--search", "query", "--limit", "101"}, {"--limit", "5"}} {
		if _, err := run(t, append([]string{"files", "agent", "support"}, flags...)...); err == nil {
			t.Fatal("accepted invalid search", flags)
		}
	}
}
