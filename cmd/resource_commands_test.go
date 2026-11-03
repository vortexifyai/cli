package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
)

func TestCreatePreservesExistingImplementationAndServerBaseline(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	cfg := config.Default("project", "demo", "")
	if err := config.Save(root, cfg); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, "transforms", "orders")
	os.MkdirAll(local, 0755)
	os.WriteFile(filepath.Join(local, "main.py"), []byte("existing code"), 0644)
	server := httptest.NewServer(withProjectFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]resources.Registered{})
		case "/api/v2/artifacts/resources/initialize_resource":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["org_id"] != "org" || body["resource_type"] != "transform" {
				t.Error(body)
			}
			json.NewEncoder(w).Encode(api.Resource{ID: "new-id", Type: "transform", Name: "orders", Version: 1, CodeHash: "server-starter", HasCode: true})
		default:
			t.Error("unexpected download/route", r.URL.Path)
			http.NotFound(w, r)
		}
	})))
	defer server.Close()
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "create", "transform", "orders"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(local, "main.py"))
	if string(data) != "existing code" {
		t.Fatal("replaced implementation")
	}
	st, err := state.Load(root)
	if err != nil || st.Resources["transform:orders"].CodeHash != "server-starter" {
		t.Fatal(st, err)
	}
}
func TestCreateDownloadsStarterWhenFolderIsMissing(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(root, config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	starter := t.TempDir()
	os.WriteFile(filepath.Join(starter, "main.py"), []byte("starter"), 0644)
	hash, _ := resources.HashDirectory(starter)
	var archive bytes.Buffer
	resources.CreateArchive(starter, &archive)
	server := httptest.NewServer(withProjectFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]resources.Registered{})
		case "/api/v2/artifacts/resources/initialize_resource":
			json.NewEncoder(w).Encode(api.Resource{ID: "id", Name: "orders", Type: "transform", Version: 1, HasCode: true, CodeHash: hash})
		case "/api/v2/execution/code/pull_resource":
			w.Write(archive.Bytes())
		default:
			t.Error("unexpected route", r.URL.Path)
		}
	})))
	defer server.Close()
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "create", "transform", "orders"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "transforms/orders/main.py"))
	if err != nil || string(data) != "starter" {
		t.Fatal(string(data), err)
	}
}
func TestStatusShowsUnregisteredUpstreamAndServerOnlyDownstream(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(root, config.Default("project", "demo", ""))
	for name, source := range map[string]string{
		"source": "from vortexify import vxfy_transform\n@vxfy_transform()\ndef run(): return None\n",
		"final":  "from vortexify import vxfy_transform, TransformOutput\n@vxfy_transform(inputs={\"x\":TransformOutput(\"source\")})\ndef run(x): return x\n",
	} {
		folder := filepath.Join(root, "transforms", name)
		os.MkdirAll(folder, 0755)
		os.WriteFile(filepath.Join(folder, "main.py"), []byte(source), 0644)
	}
	server := httptest.NewServer(withProjectFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]resources.Registered{
			{ID: "source-id", Name: "source", Type: "transform", HasCode: true, CodeHash: "old"},
			{ID: "agent-id", Name: "sales", Type: "agent", HasCode: true, UpstreamIDs: []string{"source-id"}},
		})
	})))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "status")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, expected := range []string{"transform:final\n  Source: not registered", "Upstream: transform:source", "Downstream: agent:sales (server-only)"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("missing %q in %s", expected, out)
		}
	}
}

func TestCreateSyncUsesOwningConnectorFolderAndBasenamePayload(t *testing.T) {
	for _, qualified := range []bool{false, true} {
		t.Run(fmt.Sprint(qualified), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("VORTEXIFY_TOKEN", "token")
			cfg := config.Default("project", "demo", "")
			if err := config.Save(root, cfg); err != nil {
				t.Fatal(err)
			}
			yaml, _ := os.ReadFile(filepath.Join(root, config.ConfigFileName))
			if strings.Contains(string(yaml), "  syncs:") {
				t.Fatal("generated standalone sync path")
			}
			starter := t.TempDir()
			os.WriteFile(filepath.Join(starter, "sync.py"), []byte("# sync"), 0644)
			hash, _ := resources.HashDirectory(starter)
			var archive bytes.Buffer
			resources.CreateArchive(starter, &archive)
			server := httptest.NewServer(withProjectFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/get_resource_graph":
					io.WriteString(w, `[{"id":"c","name":"shop","type":"connector"}]`)
				case "/api/v2/artifacts/resources/initialize_resource":
					var body struct {
						Type   string         `json:"resource_type"`
						Values map[string]any `json:"values"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					if body.Type != "sync" || body.Values["name"] != "customers" || body.Values["connector_id"] != "c" {
						t.Error("incorrect sync registration", body)
					}
					json.NewEncoder(w).Encode(api.Resource{ID: "s", Type: "sync", Name: "customers", ConnectorID: "c", Version: 1, CodeHash: hash, HasCode: true})
				case "/api/v2/execution/code/pull_resource":
					w.Write(archive.Bytes())
				default:
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(404)
				}
			})))
			defer server.Close()
			args := []string{"--api-url", server.URL, "--org-id", "org", "create", "sync", "customers", "--connector", "shop"}
			if qualified {
				args = []string{"--api-url", server.URL, "--org-id", "org", "create", "sync", "shop/customers"}
			}
			if _, err := run(t, args...); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "connectors/shop/syncs/customers/sync.py")); err != nil {
				t.Fatal(err)
			}
			st, err := state.Load(root)
			if err != nil || st.Resources["sync:shop/customers"].ResourceID != "s" {
				t.Fatal(err)
			}
		})
	}
}
