package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := NewRootCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	command.SetArgs(args)
	err := command.ExecuteContext(context.Background())
	return out.String(), err
}
func TestRootHelpVersionAndRequiredFlags(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"login", "init", "registry", "context", "commit", "pull", "analyze", "blueprints", "dev", "recover", "update"} {
		if !strings.Contains(out, name) {
			t.Fatal("missing command", name)
		}
	}
	out, err = run(t, "--version")
	if err != nil || !strings.Contains(out, version) {
		t.Fatal(out, err)
	}
	if _, err = run(t, "document"); err == nil {
		t.Fatal("missing required type accepted")
	}
	if _, err = run(t, "pull", "unexpected"); err == nil {
		t.Fatal("accepted unexpected argument")
	}
}
func TestInitRegistryAndDocumentFromNestedDirectory(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	t.Chdir(parent)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	creates := 0
	server := httptest.NewServer(withProjectFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/platform/projects/create_project":
			creates++
			var payload struct {
				OrgID  string            `json:"org_id"`
				Values map[string]string `json:"values"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			if payload.OrgID != "org" || payload.Values["name"] != "demo" || r.Method != "POST" {
				t.Error("wrong project name")
			}
			json.NewEncoder(w).Encode(api.Project{ID: "project", Name: "demo"})
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]api.Resource{{ID: "1", Type: "transform", Name: "orders"}, {ID: "2", Type: "api", Name: "save"}})
		case "/api/v2/documents/documents/get_project_documents":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["document_type"] != "function_plan" || body["function_id"] != "1" || body["org_id"] != "org" {
				t.Error("document filter missing")
			}
			json.NewEncoder(w).Encode([]api.Document{{ID: "doc", Type: "function_plan", Content: "# Exact document\n"}})
		default:
			http.NotFound(w, r)
		}
	})))
	defer server.Close()
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "init", "demo"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 4 {
		t.Fatal("init created extra folders")
	}
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "init", "demo"); err == nil {
		t.Fatal("overwrote existing config")
	}
	if creates != 1 {
		t.Fatal("created project before checking config")
	}
	nested := filepath.Join(root, "transforms", "orders")
	os.MkdirAll(nested, 0755)
	t.Chdir(nested)
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "registry", "--type", "transform")
	if err != nil || !strings.Contains(out, "orders") || strings.Contains(out, "save") {
		t.Fatal(out, err)
	}
	out, err = run(t, "--api-url", server.URL, "--org-id", "org", "context", "read", "context", "index.md")
	if err != nil || !strings.Contains(out, "# Context") {
		t.Fatalf("document %q %v", out, err)
	}
}
func TestInitPrefersServerConfig(t *testing.T) {
	parent := t.TempDir()
	t.Chdir(parent)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	server := httptest.NewServer(withProjectFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.Project{ID: "id", Name: "existing", Config: &config.ProjectConfig{ProjectID: "id", Transforms: &config.ResourceFolder{Folder: "custom"}}})
	})))
	defer server.Close()
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "init", "existing", "--project-id", "id"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(parent, "existing"))
	if err != nil || cfg.Transforms.Folder != "custom" || cfg.APIs != nil {
		t.Fatal("did not preserve server config", err)
	}
}

func TestRecoverNeedsNoLoginOrAPI(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := config.Save(root, config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VORTEXIFY_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	out, err := run(t, "--api-url", "https://unreachable.invalid", "recover")
	if err != nil || !strings.Contains(out, "recovery complete") {
		t.Fatal("offline recovery failed", out, err)
	}
}

func TestPullInclusionFlagsRequireResource(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, flag := range []string{"--include-dependencies", "--include-dependents"} {
		if _, err := run(t, "pull", flag); err == nil || !strings.Contains(err.Error(), "require --resource") {
			t.Fatalf("missing resource accepted for %s: %v", flag, err)
		}
	}
	out, err := run(t, "pull", "--help")
	if err != nil || !strings.Contains(out, "--include-dependencies") || !strings.Contains(out, "--include-dependents") {
		t.Fatal("missing inclusion flags", out, err)
	}
}

func TestPullCreatesYAMLAndPreservesExistingPaths(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	source := t.TempDir()
	os.WriteFile(filepath.Join(source, "main.py"), []byte("code"), 0644)
	hash, err := resources.HashDirectory(source)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := resources.CreateArchive(source, &archive); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(withProjectFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/api/v2/platform/projects/get_project":
			json.NewEncoder(w).Encode(api.Project{ID: "id", OrgID: "org", Name: "project"})
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]api.Resource{{ID: "r", Type: "transform", Name: "orders", Version: 1, HasCode: true, CodeHash: hash}})
		case "/api/v2/execution/code/pull_resource":
			w.Write(archive.Bytes())
		default:
			t.Error("unexpected request", r.URL.Path)
			w.WriteHeader(404)
		}
	})))
	defer server.Close()
	if _, err := run(t, "pull", "--project-id", "id", "--api-url", server.URL); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil || cfg.ProjectID != "id" || cfg.Transforms.Folder != "transforms" || cfg.Apps.Folder != "apps" {
		t.Fatal("wrong generated project", cfg, err)
	}
	if _, err := os.Stat(filepath.Join(root, "transforms/orders/main.py")); err != nil {
		t.Fatal(err)
	}
	custom := []byte("# Keep this configuration exactly\nproject_id: id\npaths:\n  transforms: /custom/transforms\n")
	os.WriteFile(filepath.Join(root, config.ConfigFileName), custom, 0644)
	if _, err := run(t, "pull", "--api-url", server.URL); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, config.ConfigFileName))
	if !bytes.Equal(got, custom) {
		t.Fatal("rewrote existing YAML")
	}
	if _, err := os.Stat(filepath.Join(root, "custom/transforms/orders/main.py")); err != nil {
		t.Fatal("did not use existing paths", err)
	}
	nested := filepath.Join(root, "custom/transforms/orders")
	t.Chdir(nested)
	if _, err := run(t, "pull", "--api-url", server.URL); err != nil {
		t.Fatal("nested pull failed", err)
	}
	if _, err := run(t, "pull", "--project-id", "other", "--api-url", server.URL); err == nil {
		t.Fatal("accepted different project")
	}
}

func TestFirstPullRequiresProjectID(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := run(t, "pull"); err == nil || !strings.Contains(err.Error(), "--project-id") {
		t.Fatal("missing project ID accepted", err)
	}
}
