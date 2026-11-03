package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/commit"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
)

func TestCommitAttachmentOnlyChanges(t *testing.T) {
	root, cfg := workspace(t)
	cfg.Agents = &config.ResourceFolder{Folder: "agents"}
	file(t, root, "agents/support/main.py", "from vortexify import Agent\nagent = Agent(name='support')\n")
	local, err := resources.Scan(root, cfg)
	if err != nil || len(local) != 1 {
		t.Fatalf("scan: %v %v", local, err)
	}
	remote := api.Resource{ID: "agent", Type: "agent", Name: "support", HasCode: true, CodeHash: local[0].Hash, Version: 1, Files: &resources.FileStatus{Changed: true, Total: 1, Added: []resources.AttachedFile{{ID: "doc", Name: "shipping-policy.md"}}}}
	uploads, commits := 0, 0
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads++
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer storage.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]api.Resource{remote})
		case "/api/v2/execution/code/get_commit_s3_url":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["base_version"] != float64(1) {
				t.Error(body)
			}
			json.NewEncoder(w).Encode(api.CommitUpload{UploadID: "upload", UploadURL: storage.URL})
		case "/api/v2/execution/code/commit_resource":
			commits++
			remote.Version++
			remote.Files.Changed = false
			remote.Files.Added = nil
			json.NewEncoder(w).Encode(remote)
		default:
			t.Error("unexpected route", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := commit.Service{Client: client(server), Output: io.Discard}
	for i := 0; i < 2; i++ {
		if err := service.Run(context.Background(), root, cfg, commit.Filter{Type: "agent", Resource: "support"}); err != nil {
			t.Fatal(err)
		}
	}
	if uploads != 1 || commits != 1 {
		t.Fatalf("uploads=%d commits=%d", uploads, commits)
	}
}
