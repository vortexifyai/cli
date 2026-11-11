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
)

func runOrganizationCommand(input string, args ...string) (string, string, error) {
	command := NewRootCommand()
	var out, prompts bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&prompts)
	command.SetIn(strings.NewReader(input))
	command.SetArgs(args)
	err := command.ExecuteContext(context.Background())
	return out.String(), prompts.String(), err
}

func TestProjectsSelectOrganizationPerInvocation(t *testing.T) {
	t.Setenv("VORTEXIFY_TOKEN", "token")
	t.Setenv("VORTEXIFY_ORG_ID", "")
	var organizations int
	var selected []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" || r.Method != "POST" {
			t.Error("missing authentication or wrong method")
		}
		var values map[string]any
		json.NewDecoder(r.Body).Decode(&values)
		switch r.URL.Path {
		case "/api/v2/identity/organizations/list_user_orgs":
			organizations++
			if _, ok := values["org_id"]; ok {
				t.Error("organization list used a default organization")
			}
			json.NewEncoder(w).Encode([]api.Organization{{ID: "mes", Name: "MES"}, {ID: "wms", Name: "WMS"}})
		case "/api/v2/platform/projects/list_projects":
			selected = append(selected, values["org_id"].(string))
			json.NewEncoder(w).Encode([]api.Project{{ID: "project", Name: "manufacturing"}})
		case "/api/v2/platform/projects/search_projects":
			selected = append(selected, values["org_id"].(string))
			if values["query"] != "inventory tracking" {
				t.Error("search query changed", values)
			}
			json.NewEncoder(w).Encode(map[string]any{"answer": "Found inventory", "projects": []api.Project{{ID: "project", Name: "inventory"}}})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, args := range [][]string{{"projects"}, {"projects", "list"}, {"projects", "search", "inventory", "tracking"}} {
		out, prompts, err := runOrganizationCommand("invalid\n2\n", append([]string{"--api-url", server.URL}, args...)...)
		if err != nil || !strings.Contains(out, "project") || !strings.Contains(prompts, "MES") || !strings.Contains(prompts, "Select an organization from the list") {
			t.Fatal(out, prompts, err)
		}
	}
	if organizations != 3 || strings.Join(selected, ",") != "wms,wms,wms" {
		t.Fatal("selection persisted or ignored", organizations, selected)
	}
	// An explicit flag is per invocation, with no membership prompt.
	out, prompts, err := runOrganizationCommand("", "--api-url", server.URL, "projects", "list", "--org-id", "mes", "--json")
	if err != nil || prompts != "" || !json.Valid([]byte(out)) || organizations != 3 || selected[3] != "mes" {
		t.Fatal(out, prompts, err, selected)
	}
	out, prompts, err = runOrganizationCommand("mes\n", "--api-url", server.URL, "projects", "search", "inventory tracking", "--json")
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(prompts, "Choose an organization") || !strings.Contains(out, "Found inventory") {
		t.Fatal(out, prompts, err)
	}
}

func TestOrganizationSelectionDoesNotSilentlyChoose(t *testing.T) {
	t.Setenv("VORTEXIFY_TOKEN", "token")
	for _, tc := range []struct {
		name, input, message string
		orgs                 []api.Organization
	}{
		{"empty membership", "", "do not belong", nil},
		{"single requires input", "", "--org-id", []api.Organization{{ID: "org", Name: "Only org"}}},
		{"cancel", "q\n", "canceled", []api.Organization{{ID: "org"}}},
		{"invalid then EOF", "99\n", "--org-id", []api.Organization{{ID: "org"}}},
		{"malformed membership", "1\n", "without an ID", []api.Organization{{Name: "Broken"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/identity/organizations/list_user_orgs" {
					t.Error("proceeded without a selection")
				}
				json.NewEncoder(w).Encode(tc.orgs)
			}))
			defer server.Close()
			_, _, err := runOrganizationCommand(tc.input, "--api-url", server.URL, "projects", "list")
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatal(err)
			}
		})
	}
}

func TestInitOrganizationSelection(t *testing.T) {
	t.Setenv("VORTEXIFY_TOKEN", "token")
	t.Setenv("VORTEXIFY_ORG_ID", "")
	for _, attach := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "attach"}[attach], func(t *testing.T) {
			t.Chdir(t.TempDir())
			selected := false
			server := httptest.NewServer(withProjectFiles(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/identity/organizations/list_user_orgs" {
					json.NewEncoder(w).Encode([]api.Organization{{ID: "org", Name: "Manufacturing"}})
					return
				}
				var values map[string]any
				json.NewDecoder(r.Body).Decode(&values)
				if values["org_id"] != "org" {
					t.Error("init did not use selected organization", values)
				}
				selected = true
				json.NewEncoder(w).Encode(api.Project{ID: "project", OrgID: "org", Name: "demo"})
			})))
			defer server.Close()
			args := []string{"--api-url", server.URL, "init", "demo"}
			if attach {
				args = append(args, "--project-id", "project")
			}
			_, prompts, err := runOrganizationCommand("1\n", args...)
			if err != nil || !selected || !strings.Contains(prompts, "Manufacturing") {
				t.Fatal(prompts, err)
			}
			if _, err := config.Load("demo"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(config.ConfigFileName); !os.IsNotExist(err) {
				t.Fatal("initialized the parent directory", err)
			}
		})
	}
}

func TestInitRequiresNewNamedDirectoryBeforeRequests(t *testing.T) {
	t.Chdir(t.TempDir())
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", 500)
	}))
	defer server.Close()
	os.Mkdir("existing", 0755)
	os.WriteFile("file", []byte("preserve"), 0600)
	os.Symlink("existing", "link")
	for _, args := range [][]string{
		{}, {"."}, {".."}, {"../escape"}, {"nested/project"}, {`nested\project`},
		{"/absolute"}, {"existing"}, {"file"}, {"link"}, {" "},
		{"demo", "--name", "other"}, {"--project-id", "id"},
	} {
		base := []string{"--api-url", server.URL, "--org-id", "org", "init"}
		if _, _, err := runOrganizationCommand("", append(base, args...)...); err == nil {
			t.Fatal("accepted unsafe or missing project name", args)
		}
	}
	if requests != 0 {
		t.Fatal("invalid destination reached API", requests)
	}
	if text, _ := os.ReadFile("file"); string(text) != "preserve" {
		t.Fatal("modified existing file")
	}
	config.Save(".", config.Default("id", "parent", ""))
	if _, _, err := runOrganizationCommand("", "--api-url", server.URL, "--org-id", "org", "init", "nested"); err == nil || !strings.Contains(err.Error(), "nested projects") {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("nested project reached API")
	}
}

func TestInitCancellationAndCreationFailureLeaveNoEmptyDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/identity/organizations/list_user_orgs" {
			w.Write([]byte(`[{"id":"org","name":"Manufacturing"}]`))
			return
		}
		creates++
		http.Error(w, "creation failed", 500)
	}))
	defer server.Close()
	if _, _, err := runOrganizationCommand("q\n", "--api-url", server.URL, "init", "canceled"); err == nil {
		t.Fatal("accepted cancellation")
	}
	if creates != 0 {
		t.Fatal("created remote project after cancellation")
	}
	if _, _, err := runOrganizationCommand("1\n", "--api-url", server.URL, "init", "failed"); err == nil {
		t.Fatal("ignored creation failure")
	}
	for _, name := range []string{"canceled", "failed"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatal("left an empty project directory", name, err)
		}
	}
}

func TestInitRejectsWrongOrganizationBeforeWriting(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(api.Project{ID: "project", OrgID: "other", Name: "demo"})
	}))
	defer server.Close()
	_, _, err := runOrganizationCommand("", "--api-url", server.URL, "--org-id", "selected", "init", "demo", "--project-id", "project")
	if err == nil || !strings.Contains(err.Error(), "selected organization") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("demo", config.ConfigFileName)); !os.IsNotExist(err) {
		t.Fatal("wrote configuration for wrong organization", err)
	}
}

func TestKnowledgeGraphEnableShowsCapacityAndRequiresConfirmation(t *testing.T) {
	t.Setenv("VORTEXIFY_TOKEN", "token")
	root := t.TempDir()
	if err := config.Save(root, &config.ProjectConfig{ProjectID: "project", Name: "test"}); err != nil {
		t.Fatal(err)
	}
	previous, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	enables := 0
	available := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/platform/projects/get_project_knowledge_graph_capacity":
			json.NewEncoder(w).Encode(map[string]any{"enabled": false, "datasets": 20, "project_pairs": 190, "organization_pairs": 235, "limit": 250, "available": available, "plan": "starter"})
		case "/api/v2/platform/projects/enable_project_knowledge_graph":
			enables++
			w.Write([]byte(`{"id":"project"}`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org", "knowledge-graph", "enable"}
	out, prompt, err := runOrganizationCommand("n\n", base...)
	if err == nil || enables != 0 || !strings.Contains(out, "235 / 250") || !strings.Contains(prompt, "20 source datasets") {
		t.Fatal(out, prompt, err, enables)
	}
	out, prompt, err = runOrganizationCommand("yes\n", base...)
	if err != nil || enables != 1 || !strings.Contains(prompt, "Transform outputs are excluded") {
		t.Fatal(out, prompt, err, enables)
	}
	available = false
	_, _, err = runOrganizationCommand("", append(base, "--yes")...)
	if err == nil || !strings.Contains(err.Error(), "capacity exceeded") || enables != 1 {
		t.Fatal(err, enables)
	}
}

func TestOrganizationEnvironmentDefaultAndFlagOverride(t *testing.T) {
	t.Setenv("VORTEXIFY_TOKEN", "token")
	t.Setenv("VORTEXIFY_ORG_ID", "environment-org")
	var selected string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v2/platform/projects/list_projects" {
			t.Errorf("unexpected selection prompt: %s", req.URL.Path)
		}
		var payload map[string]any
		json.NewDecoder(req.Body).Decode(&payload)
		selected, _ = payload["org_id"].(string)
		json.NewEncoder(w).Encode([]api.Project{})
	}))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"projects", "list"}, "environment-org"},
		{[]string{"projects", "list", "--org-id", "explicit-org"}, "explicit-org"},
	} {
		_, prompts, err := runOrganizationCommand("", append([]string{"--api-url", server.URL}, tc.args...)...)
		if err != nil || prompts != "" || selected != tc.want {
			t.Fatal(selected, prompts, err)
		}
	}
}

func TestDiscoveryConversationContinuation(t *testing.T) {
	t.Setenv("VORTEXIFY_TOKEN", "token")
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var payload map[string]any
		json.NewDecoder(req.Body).Decode(&payload)
		payloads = append(payloads, payload)
		if payload["org_id"] != "org" {
			t.Error("missing organization scope", payload)
		}
		switch req.URL.Path {
		case "/api/v2/platform/projects/search_projects":
			json.NewEncoder(w).Encode(map[string]any{"conversation_id": "saved-search", "answer": "Use the inventory app.", "projects": []any{}, "apps": []map[string]any{{"id": "app", "name": "Inventory", "deployment_status": "active"}}})
		default:
			t.Error("unexpected endpoint", req.URL.Path)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org"}
	for _, args := range [][]string{{"projects", "search", "inventory"}, {"projects", "search", "Which is deployed?", "--conversation", "saved-search"}} {
		out, prompts, err := runOrganizationCommand("", append(base, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		if args[1] == "search" && (!strings.Contains(out, "Inventory") || !strings.Contains(prompts, "--conversation saved-search")) {
			t.Fatal(out, prompts)
		}
	}
	if _, ok := payloads[0]["conversation_id"]; ok {
		t.Fatal("new searches inherit a conversation")
	}
	if len(payloads) != 2 || payloads[1]["conversation_id"] != "saved-search" {
		t.Fatal(payloads)
	}
}
