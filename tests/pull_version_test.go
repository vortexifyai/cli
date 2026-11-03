package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/commit"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/pull"
	"vortexify-cli/internal/state"
)

func TestPullRefreshesSourceUnchangedVersionAndAllowsLocalCommit(t *testing.T) {
	root, cfg := workspace(t)
	cfg.Connectors = &config.ResourceFolder{Folder: "connectors"}
	local := "from vortexify import Connector\nclass Shop(Connector): pass\n"
	file(t, root, "connectors/shop/connector.py", local)
	baseline := state.ResourceState{ResourceID: "c", Version: 2, CodeHash: "known-source"}
	if err := state.Save(root, &state.State{Resources: map[string]state.ResourceState{"connector:shop": baseline}}); err != nil {
		t.Fatal(err)
	}
	remote := api.Resource{ID: "c", Type: "connector", Name: "shop", Version: 3, CodeHash: baseline.CodeHash, HasCode: true}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer storage.Close()
	commits := 0
	var uploadedHash string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]api.Resource{remote})
		case "/api/v2/execution/code/get_commit_s3_url":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["base_version"] != float64(3) {
				t.Error("commit used stale version", body)
			}
			uploadedHash = body["expected_hash"].(string)
			json.NewEncoder(w).Encode(api.CommitUpload{UploadID: "upload", UploadURL: storage.URL})
		case "/api/v2/execution/code/commit_resource":
			commits++
			remote.Version, remote.CodeHash = 4, uploadedHash
			json.NewEncoder(w).Encode(remote)
		default:
			t.Error("unexpected request; pull must preserve local source", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := client(server)
	if err := (&pull.Service{Client: c}).Run(context.Background(), root, cfg, pull.Filter{Type: "connector"}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "connectors/shop/connector.py"))
	if err != nil || string(contents) != local {
		t.Fatal("pull changed local edits", err)
	}
	st, err := state.Load(root)
	if err != nil || st.Resources["connector:shop"].Version != 3 || st.Resources["connector:shop"].CodeHash != baseline.CodeHash {
		t.Fatal("pull did not refresh server baseline", st, err)
	}
	if err := (&commit.Service{Client: c}).Run(context.Background(), root, cfg, commit.Filter{Type: "connector"}); err != nil {
		t.Fatal(err)
	}
	if commits != 1 {
		t.Fatalf("expected one commit, got %d", commits)
	}
}
