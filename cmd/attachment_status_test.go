package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
)

func TestStatusShowsWorkingAttachments(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	cfg := config.Default("project", "demo", "")
	if err := config.Save(root, cfg); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "agents", "support")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "main.py"), []byte("from vortexify import Agent\nagent = Agent()\n"), 0644)
	local, err := resources.Scan(root, cfg)
	if err != nil || len(local) != 1 {
		t.Fatalf("scan %v %v", local, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]resources.Registered{{ID: "agent", Type: "agent", Name: "support", Version: 1, HasCode: true, CodeHash: local[0].Hash, Files: &resources.FileStatus{Changed: true, Total: 1, Added: []resources.AttachedFile{{ID: "doc", Name: "shipping-policy.md"}}}}})
		case "/api/v2/execution/jobs/get_preview_status":
			json.NewEncoder(w).Encode([]any{})
		default:
			t.Error("unexpected route", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, jsonFlag := range []bool{false, true} {
		args := []string{"--api-url", server.URL, "--org-id", "org", "status", "--type", "agent", "--resource", "support"}
		if jsonFlag {
			args = append(args, "--json")
		}
		out, err := run(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		if jsonFlag {
			if !strings.Contains(out, `"files":{"changed":true`) {
				t.Fatal(out)
			}
		} else {
			for _, want := range []string{"agent:support", "Code: clean", "Attachments: 1 added — uncommitted", "    + shipping-policy.md"} {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q in %s", want, out)
				}
			}
		}
	}
}
