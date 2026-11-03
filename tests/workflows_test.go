package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vortexify-cli/internal/agent"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/commit"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/pull"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
)

func file(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func code(t *testing.T, content string) ([]byte, string) {
	t.Helper()
	root := t.TempDir()
	file(t, root, "main.py", content)
	var archive bytes.Buffer
	if err := resources.CreateArchive(root, &archive); err != nil {
		t.Fatal(err)
	}
	hash, err := resources.HashDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	return archive.Bytes(), hash
}

type collectionTransport struct{ next http.RoundTripper }

func (transport collectionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/api/v2/documents/files/get_collection" {
		var values map[string]any
		json.NewDecoder(request.Body).Decode(&values)
		payload := map[string]any{}
		json.Unmarshal([]byte(`{"version": 1, "hash": "1bba874814253f87448fb78a451c3e983f13e135d72a6c37197961a97d19f7af", "files": [{"path": "index.md", "size": 10}], "contents": {"index.md": "IyBDb250ZXh0Cg=="}}`), &payload)
		payload["collection"] = values["collection"]
		data, _ := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	}
	return transport.next.RoundTrip(request)
}
func client(server *httptest.Server) *api.Client {
	c := api.NewClient(server.URL)
	c.HTTPClient.Transport = collectionTransport{c.HTTPClient.Transport}
	c.OrgID = "org"
	c.Token = func(context.Context) (string, error) { return "token", nil }
	return c
}
func workspace(t *testing.T) (string, *config.ProjectConfig) {
	t.Helper()
	root := t.TempDir()
	return root, &config.ProjectConfig{ProjectID: "project", Transforms: &config.ResourceFolder{Folder: "transforms"}}
}

func TestCommitSignedUploadStateAndNoop(t *testing.T) {
	root, cfg := workspace(t)
	file(t, root, "transforms/orders/main.py", "from vortexify import vxfy_transform\n@vxfy_transform()\ndef run(): return 'initial'\n")
	file(t, root, "transforms/orders/.env", "secret")
	remote := []api.Resource{}
	uploads := 0
	var metadata api.CommitResourceRequest
	var uploadedHash string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads++
		if r.Method != "PUT" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("invalid storage authentication/method")
		}
		if r.ContentLength <= 0 {
			t.Error("incorrect archive length")
		}
		target := t.TempDir()
		if err := resources.ExtractArchive(r.Body, target); err != nil {
			t.Error(err)
		}
		hash, err := resources.HashDirectory(target)
		if err != nil || hash != metadata.CodeHash {
			t.Errorf("archive hash %s %v", hash, err)
		}
		if _, err = os.Stat(filepath.Join(target, ".env")); !os.IsNotExist(err) {
			t.Error("secret in upload")
		}
		uploadedHash = hash
		w.WriteHeader(200)
	}))
	defer storage.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode(remote)
		case "/api/v2/artifacts/resources/initialize_resource":
			json.NewEncoder(w).Encode(api.Resource{ID: "resource", Type: "transform", Name: "orders", Version: 0})
		case "/api/v2/execution/code/get_commit_s3_url":
			var payload struct {
				ExpectedHash string `json:"expected_hash"`
				BaseVersion  int    `json:"base_version"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			metadata = api.CommitResourceRequest{Type: "transform", Name: "orders", CodeHash: payload.ExpectedHash, BaseVersion: payload.BaseVersion}
			if metadata.BaseVersion != uploads {
				t.Error("incorrect base version")
			}
			json.NewEncoder(w).Encode(api.CommitUpload{UploadID: "upload", UploadURL: storage.URL + "/archive?signature=secret"})
		case "/api/v2/execution/code/commit_resource":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["upload_id"] != "upload" || uploadedHash != metadata.CodeHash {
				t.Error("finalize before upload")
			}
			remote = []api.Resource{{ID: "resource", Type: metadata.Type, Name: metadata.Name, CodeHash: uploadedHash, Version: uploads, HasCode: true}}
			json.NewEncoder(w).Encode(remote[0])
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := commit.Service{Client: client(server), Output: io.Discard}
	if err := service.Run(context.Background(), root, cfg, commit.Filter{}); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(root)
	if err != nil || st.Resources["transform:orders"].Version != 1 {
		t.Fatal("state update", err)
	}
	if err = service.Run(context.Background(), root, cfg, commit.Filter{}); err != nil {
		t.Fatal(err)
	}
	if uploads != 1 {
		t.Fatal("uploaded unchanged resource")
	}
	file(t, root, "transforms/orders/main.py", "from vortexify import vxfy_transform\n@vxfy_transform()\ndef run(): return 'changed'\n")
	if err = service.Run(context.Background(), root, cfg, commit.Filter{}); err != nil {
		t.Fatal(err)
	}
	if uploads != 2 {
		t.Fatal("changed resource not uploaded")
	}
}

func TestPullNewServerAheadConflictAndHashMismatch(t *testing.T) {
	root, cfg := workspace(t)
	archive, hash := code(t, "v1")
	remote := api.Resource{ID: "r", Type: "transform", Name: "orders", Version: 1, CodeHash: hash, HasCode: true}
	downloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]api.Resource{remote})
		case "/api/v2/execution/code/pull_resource":
			downloads++
			var body struct {
				Version      int    `json:"version"`
				ResourceType string `json:"resource_type"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Version != remote.Version || body.ResourceType != "function" {
				t.Error("missing pinned version")
			}
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := pull.Service{Client: client(server), Output: io.Discard}
	if err := service.Run(context.Background(), root, cfg, pull.Filter{}); err != nil {
		t.Fatal(err)
	}
	file(t, root, "transforms/orders/.env", "secret")
	archive, hash = code(t, "v2")
	remote.Version = 2
	remote.CodeHash = hash
	if err := service.Run(context.Background(), root, cfg, pull.Filter{}); err != nil {
		t.Fatal(err)
	}
	contents, _ := os.ReadFile(filepath.Join(root, "transforms/orders/.env"))
	if string(contents) != "secret" {
		t.Fatal("lost ignored local file")
	}
	file(t, root, "transforms/orders/main.py", "local edit")
	archive, hash = code(t, "v3")
	remote.Version = 3
	remote.CodeHash = hash
	err := service.Run(context.Background(), root, cfg, pull.Filter{})
	var conflict *pull.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if downloads != 2 {
		t.Fatal("downloaded conflict")
	}
	contents, _ = os.ReadFile(filepath.Join(root, "transforms/orders/main.py"))
	if string(contents) != "local edit" {
		t.Fatal("overwrote conflict")
	}
	// Restore the baseline, then ensure a corrupt archive cannot replace it or advance state.
	file(t, root, "transforms/orders/main.py", "v2")
	remote.CodeHash = "wrong-hash"
	if err = service.Run(context.Background(), root, cfg, pull.Filter{}); err == nil {
		t.Fatal("accepted hash mismatch")
	}
	contents, _ = os.ReadFile(filepath.Join(root, "transforms/orders/main.py"))
	if string(contents) != "v2" {
		t.Fatal("partially overwrote resource")
	}
	st, _ := state.Load(root)
	if st.Resources["transform:orders"].Version != 2 {
		t.Fatal("advanced failed pull state")
	}
}

func TestPullTemplateAndUnmanagedTypes(t *testing.T) {
	root, cfg := workspace(t)
	archive, _ := code(t, "template")
	templateRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]api.Resource{{ID: "r", Type: "transform", Name: "orders"}, {ID: "app", Type: "app", Name: "web"}})
		case "/api/v2/execution/code/pull_resource":
			templateRequests++
			w.Write(archive)
		default:
			t.Error("unexpected request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := pull.Service{Client: client(server), Output: io.Discard}
	if err := service.Run(context.Background(), root, cfg, pull.Filter{}); err != nil {
		t.Fatal(err)
	}
	if templateRequests != 1 {
		t.Fatal("missing template")
	}
	if _, err := os.Stat(filepath.Join(root, "apps")); !os.IsNotExist(err) {
		t.Fatal("materialized unconfigured folder")
	}
	file(t, root, "transforms/orders/main.py", "edited")
	if err := service.Run(context.Background(), root, cfg, pull.Filter{}); err != nil {
		t.Fatal(err)
	}
	if templateRequests != 1 {
		t.Fatal("replaced local template")
	}
}

func TestCommitRejectsServerAdvance(t *testing.T) {
	root, cfg := workspace(t)
	file(t, root, "transforms/orders/main.py", "from vortexify import vxfy_transform\n@vxfy_transform()\ndef run(): return 'local'\n")
	st := &state.State{Resources: map[string]state.ResourceState{"transform:orders": {ResourceID: "r", Version: 1, CodeHash: "base"}}}
	state.Save(root, st)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/artifacts/resources/get_resource_graph" {
			t.Error("uploaded stale edit")
		}
		json.NewEncoder(w).Encode([]api.Resource{{ID: "r", Type: "transform", Name: "orders", Version: 2, CodeHash: "newer", HasCode: true}})
	}))
	defer server.Close()
	if err := (&commit.Service{Client: client(server)}).Run(context.Background(), root, cfg, commit.Filter{}); err == nil {
		t.Fatal("accepted stale commit")
	}
}

func TestAgentStreamingAndErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/execution/manager/query_manager_agent" {
					json.NewEncoder(w).Encode(api.AgentRun{ConversationID: "session", RunID: "run"})
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if fail {
					fmt.Fprint(w, "event: error\ndata: {\"message\":\"failed\"}\n\n")
					return
				}
				fmt.Fprint(w, "event: token\ndata: {\"text\":\"hello \"}\n\nevent: token\ndata: \"world\"\n\nevent: done\ndata: {}\n\n")
			}))
			defer server.Close()
			var out bytes.Buffer
			s, err := agent.NewSession(context.Background(), client(server), "project", &out)
			if err != nil {
				t.Fatal(err)
			}
			err = s.Send(context.Background(), "hi")
			if fail {
				if err == nil {
					t.Fatal("ignored stream error")
				}
			} else if err != nil || out.String() != "hello world\n" {
				t.Fatalf("stream %q %v", out.String(), err)
			}
		})
	}
}

func TestCommitAdoptsMatchingUntrackedCode(t *testing.T) {
	root, cfg := workspace(t)
	file(t, root, "transforms/orders/main.py", "from vortexify import vxfy_transform\n@vxfy_transform()\ndef run(): return 'matching'\n")
	hash, err := resources.HashDirectory(filepath.Join(root, "transforms/orders"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/artifacts/resources/get_resource_graph" {
			t.Error("uploaded matching resource")
		}
		json.NewEncoder(w).Encode([]api.Resource{{ID: "r", Type: "transform", Name: "orders", Version: 3, CodeHash: hash, HasCode: true}})
	}))
	defer server.Close()
	if err = (&commit.Service{Client: client(server)}).Run(context.Background(), root, cfg, commit.Filter{}); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(root)
	if err != nil || st.Resources["transform:orders"].Version != 3 {
		t.Fatal("did not adopt matching baseline", err)
	}
}

func TestPullRelatedResourcesAcrossTypes(t *testing.T) {
	for _, flags := range []struct {
		name                     string
		dependencies, dependents bool
	}{{"dependencies", true, false}, {"dependents", false, true}, {"both", true, true}} {
		t.Run(flags.name, func(t *testing.T) {
			root, cfg := workspace(t)
			cfg.Apps = &config.ResourceFolder{Folder: "apps"}
			cfg.APIs = &config.ResourceFolder{Folder: "apis"}
			archive, hash := code(t, "server")
			remote := []resources.Registered{
				{ID: "app", Type: "app", Name: "dashboard", Version: 2, HasCode: true, CodeHash: hash, UpstreamIDs: []string{"api"}},
				{ID: "api", Type: "api", Name: "orders", Version: 3, HasCode: true, CodeHash: hash},
				{ID: "transform", Type: "transform", Name: "refresh", Version: 4, HasCode: true, CodeHash: hash, UpstreamIDs: []string{"app"}},
			}
			selected := []resources.Registered{remote[0]}
			if flags.dependencies {
				selected = append(selected, remote[1])
			}
			if flags.dependents {
				selected = append(selected, remote[2])
			}

			downloads := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing auth")
				}
				if r.URL.Path == "/api/v2/artifacts/resources/get_resource_graph" {
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if r.Method != "POST" || body["project_id"] != "project" || body["org_id"] != "org" {
						t.Error("wrong graph scope")
					}
					json.NewEncoder(w).Encode(remote)
					return
				}
				if r.URL.Path == "/api/v2/execution/code/pull_resource" {
					var body struct {
						ResourceID string `json:"resource_id"`
						Version    int    `json:"version"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					for _, resource := range selected {
						if body.ResourceID == resource.ID {
							if body.Version != resource.Version {
								t.Error("version not pinned")
							}
							downloads[resource.ID]++
							w.Write(archive)
							return
						}
					}
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			service := pull.Service{Client: client(server), Output: io.Discard}
			filter := pull.Filter{Type: "app", Resource: "dashboard", IncludeDependencies: flags.dependencies, IncludeDependents: flags.dependents}
			if err := service.Run(context.Background(), root, cfg, filter); err != nil {
				t.Fatal(err)
			}
			st, err := state.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, resource := range selected {
				folder, _ := resources.FolderForType(cfg, resource.Type)
				data, err := os.ReadFile(filepath.Join(root, folder, resource.Name, "main.py"))
				if err != nil || string(data) != "server" || downloads[resource.ID] != 1 {
					t.Fatalf("related resource %s missing or duplicated: %v", resource.ID, err)
				}
				if st.Resources[state.ResourceKey(resource.Type, resource.Name)].Version != resource.Version {
					t.Fatal("missing baseline")
				}
			}
			// A dependency with local and remote edits still conflicts; expansion must not overwrite it.
			edited := selected[1]
			folder, _ := resources.FolderForType(cfg, edited.Type)
			editedPath := filepath.Join(folder, edited.Name, "main.py")
			file(t, root, editedPath, "local edit")
			for i := range remote {
				if remote[i].ID == edited.ID {
					remote[i].Version++
					remote[i].CodeHash = "different-server-hash"
				}
			}
			err = service.Run(context.Background(), root, cfg, filter)
			var conflict *pull.ConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("expected dependency conflict: %v", err)
			}
			data, _ := os.ReadFile(filepath.Join(root, editedPath))
			if string(data) != "local edit" || downloads[edited.ID] != 1 {
				t.Fatal("dependency edits overwritten")
			}
		})
	}
}

func TestExpandedPullRejectsMissingOrAmbiguousRoot(t *testing.T) {
	for _, remote := range [][]api.Resource{
		{{ID: "other", Type: "api", Name: "other"}},
		{{ID: "a", Type: "app", Name: "orders"}, {ID: "b", Type: "api", Name: "orders"}},
	} {
		root, cfg := workspace(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v2/artifacts/resources/get_resource_graph" {
				t.Error("download attempted for invalid root")
			}
			json.NewEncoder(w).Encode(remote)
		}))
		err := (&pull.Service{Client: client(server)}).Run(context.Background(), root, cfg, pull.Filter{Resource: "orders", IncludeDependencies: true})
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "exactly one root") {
			t.Fatalf("invalid root accepted: %v", err)
		}
	}
}

func TestCommitFailureDoesNotAdvanceLocalState(t *testing.T) {
	for _, stage := range []string{"prepare", "upload", "finalize"} {
		t.Run(stage, func(t *testing.T) {
			root, cfg := workspace(t)
			file(t, root, "transforms/orders/main.py", "from vortexify import vxfy_transform\n@vxfy_transform()\ndef run(): return 'local'\n")
			finalized := false
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "upload" {
					w.WriteHeader(403)
					return
				}
				io.Copy(io.Discard, r.Body)
			}))
			defer storage.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/get_resource_graph":
					json.NewEncoder(w).Encode([]api.Resource{})
				case "/api/v2/artifacts/resources/initialize_resource":
					json.NewEncoder(w).Encode(api.Resource{ID: "resource", Type: "transform", Name: "orders", Version: 0})
				case "/api/v2/execution/code/get_commit_s3_url":
					if stage == "prepare" {
						w.WriteHeader(403)
						return
					}
					json.NewEncoder(w).Encode(api.CommitUpload{UploadID: "id", UploadURL: storage.URL})
				case "/api/v2/execution/code/commit_resource":
					finalized = true
					w.WriteHeader(409)
				default:
					t.Error("unexpected route")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			err := (&commit.Service{Client: client(server), Output: &out}).Run(context.Background(), root, cfg, commit.Filter{})
			if err == nil {
				t.Fatal("commit reported success")
			}
			st, err := state.Load(root)
			if err != nil || st.Resources["transform:orders"].CodeHash != "" || st.Resources["transform:orders"].Version != 0 || strings.Contains(out.String(), "Committed") {
				t.Fatal("failed commit advanced state", st, err)
			}
			if finalized != (stage == "finalize") {
				t.Fatal("finalized after earlier failure")
			}
		})
	}
}

func TestCommitPublishesNewResourcesUpstreamFirst(t *testing.T) {
	root, cfg := workspace(t)
	file(t, root, "transforms/a_final/main.py", "from vortexify import vxfy_transform, TransformOutput\n@vxfy_transform(inputs={\"x\":TransformOutput(\"z_source\")})\ndef run(x): return x\n")
	file(t, root, "transforms/z_source/main.py", "from vortexify import vxfy_transform\n@vxfy_transform()\ndef run(): return None\n")
	var uploaded []byte
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { uploaded, _ = io.ReadAll(r.Body) }))
	defer storage.Close()
	committed := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]resources.Registered{})
		case "/api/v2/artifacts/resources/initialize_resource":
			values := payload["values"].(map[string]any)
			name := values["name"].(string)
			json.NewEncoder(w).Encode(api.Resource{ID: name, Type: "transform", Name: name, Version: 1, HasCode: true, CodeHash: "starter"})
		case "/api/v2/execution/code/get_commit_s3_url":
			json.NewEncoder(w).Encode(api.CommitUpload{UploadID: payload["resource_id"].(string), UploadURL: storage.URL})
		case "/api/v2/execution/code/commit_resource":
			name := payload["resource_id"].(string)
			if name == "a_final" && (len(committed) != 1 || committed[0] != "z_source") {
				t.Error("downstream submitted first")
			}
			target := t.TempDir()
			if err := resources.ExtractArchive(bytes.NewReader(uploaded), target); err != nil {
				t.Error(err)
			}
			hash, _ := resources.HashDirectory(target)
			committed = append(committed, name)
			json.NewEncoder(w).Encode(api.Resource{ID: name, Type: "transform", Name: name, Version: 2, HasCode: true, CodeHash: hash})
		default:
			t.Error("unexpected route", r.URL.Path)
		}
	}))
	defer server.Close()
	if err := (&commit.Service{Client: client(server)}).Run(context.Background(), root, cfg, commit.Filter{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(committed, ",") != "z_source,a_final" {
		t.Fatal(committed)
	}
	st, err := state.Load(root)
	if err != nil || st.Resources["transform:a_final"].Version != 2 {
		t.Fatal(st, err)
	}
}

func TestCommitPreflightDoesNotRegisterMissingUpstreamOrCycles(t *testing.T) {
	for _, upstream := range []string{"missing", "final"} {
		t.Run(upstream, func(t *testing.T) {
			root, cfg := workspace(t)
			file(t, root, "transforms/final/main.py", "from vortexify import vxfy_transform, TransformOutput\n@vxfy_transform(inputs={\"x\":TransformOutput(\""+upstream+"\")})\ndef run(x): return x\n")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/artifacts/resources/get_resource_graph" {
					t.Error("mutated server during invalid preflight")
				}
				json.NewEncoder(w).Encode([]resources.Registered{})
			}))
			defer server.Close()
			var out bytes.Buffer
			if err := (&commit.Service{Client: client(server), Output: &out}).Run(context.Background(), root, cfg, commit.Filter{}); err == nil {
				t.Fatal("invalid commit accepted")
			}
			if !strings.HasPrefix(out.String(), "Committing 0 resources.\nSkipping 1 resource:\n") || strings.Contains(out.String(), "No changes to commit.") {
				t.Fatal("blocked changes reported as a no-op", out.String())
			}
		})
	}
}

func TestConnectorPullDoesNotReplaceNestedSyncEdits(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("project", "demo", "")
	file(t, root, "connectors/shop/connector.py", "old connector")
	file(t, root, "connectors/shop/syncs/customers/sync.py", "local sync edits")
	original, _ := resources.HashDirectory(filepath.Join(root, "connectors/shop"), "connector")
	baseline := &state.State{Resources: map[string]state.ResourceState{"connector:shop": {ResourceID: "c", Version: 1, CodeHash: original}, "sync:shop/customers": {ResourceID: "s", Version: 1, CodeHash: "old-sync-hash"}}}
	if err := state.Save(root, baseline); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	file(t, source, "connector.py", "new connector")
	hash, _ := resources.HashDirectory(source, "connector")
	var archive bytes.Buffer
	resources.CreateArchive(source, &archive, "connector")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			json.NewEncoder(w).Encode([]api.Resource{{ID: "c", Type: "connector", Name: "shop", Version: 2, HasCode: true, CodeHash: hash}, {ID: "s", Type: "sync", Name: "customers", ConnectorID: "c", Version: 1, HasCode: true, CodeHash: "old-sync-hash"}})
		case "/api/v2/execution/code/pull_resource":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["resource_id"] != "c" {
				t.Error("unexpected sync download")
			}
			w.Write(archive.Bytes())
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	if err := (&pull.Service{Client: client(server)}).Run(context.Background(), root, cfg, pull.Filter{Type: "connector"}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "connectors/shop/syncs/customers/sync.py"))
	if err != nil || string(content) != "local sync edits" {
		t.Fatal("sync edits lost", err)
	}
	st, err := state.Load(root)
	if err != nil || st.Resources["sync:shop/customers"] != baseline.Resources["sync:shop/customers"] {
		t.Fatal("changed child baseline", err)
	}
}
