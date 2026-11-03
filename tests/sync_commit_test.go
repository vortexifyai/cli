package tests

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/commit"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/state"
)

func TestCommitAutomaticallyRegistersSyncWithConnector(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new connector", true: "existing connector"}[existing], func(t *testing.T) {
			root, cfg := workspace(t)
			cfg.Connectors = &config.ResourceFolder{Folder: "connectors"}
			file(t, root, "connectors/shop/syncs/orders/sync.py", "from vortexify import Sync\nclass Orders(Sync): pass\n")
			remote := []api.Resource{}
			filter := commit.Filter{Type: "sync"}
			if existing {
				remote = append(remote, api.Resource{ID: "connector-id", Type: "connector", Name: "shop", HasCode: true, Version: 1})
			} else {
				file(t, root, "connectors/shop/connector.py", "from vortexify import Connector\nclass Shop(Connector): pass\n")
				filter = commit.Filter{}
			}
			events := []string{}
			byID := map[string]api.Resource{}
			hashes := map[string]string{}
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusOK)
			}))
			defer storage.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/get_resource_graph":
					json.NewEncoder(w).Encode(remote)
				case "/api/v2/artifacts/resources/initialize_resource":
					kind := body["resource_type"].(string)
					values := body["values"].(map[string]any)
					row := api.Resource{ID: kind + "-id", Type: kind, Name: values["name"].(string)}
					if kind == "sync" {
						if values["name"] != "orders" || values["connector_id"] != "connector-id" {
							t.Error("incorrect sync registration", values)
						}
						row.ConnectorID = "connector-id"
						if !existing && !byID["connector-id"].HasCode {
							t.Error("sync registered before connector commit")
						}
					}
					byID[row.ID] = row
					events = append(events, "register "+kind)
					json.NewEncoder(w).Encode(row)
				case "/api/v2/execution/code/get_commit_s3_url":
					id := body["resource_id"].(string)
					hashes[id] = body["expected_hash"].(string)
					json.NewEncoder(w).Encode(api.CommitUpload{UploadID: id, UploadURL: storage.URL})
				case "/api/v2/execution/code/commit_resource":
					id := body["resource_id"].(string)
					row := byID[id]
					row.HasCode, row.Version, row.CodeHash = true, 1, hashes[id]
					byID[id] = row
					events = append(events, "commit "+row.Type)
					json.NewEncoder(w).Encode(row)
				default:
					t.Error("unexpected route", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			service := commit.Service{Client: client(server), Output: io.Discard}
			if err := service.Run(context.Background(), root, cfg, filter); err != nil {
				t.Fatal(err)
			}
			want := []string{"register sync", "commit sync"}
			if !existing {
				want = append([]string{"register connector", "commit connector"}, want...)
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events %v, want %v", events, want)
			}
			st, err := state.Load(root)
			if err != nil || st.Resources["sync:shop/orders"].ResourceID != "sync-id" || st.Resources["sync:shop/orders"].Version != 1 {
				t.Fatalf("sync baseline not saved: %v %v", st, err)
			}
		})
	}
}

func TestCommitSyncDoesNotImplicitlyCommitUnselectedNewConnector(t *testing.T) {
	root, cfg := workspace(t)
	cfg.Connectors = &config.ResourceFolder{Folder: "connectors"}
	file(t, root, "connectors/shop/connector.py", "from vortexify import Connector\nclass Shop(Connector): pass\n")
	file(t, root, "connectors/shop/syncs/orders/sync.py", "from vortexify import Sync\nclass Orders(Sync): pass\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/get_resource_graph") {
			json.NewEncoder(w).Encode([]api.Resource{})
		} else {
			t.Error("unexpected mutation", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := commit.Service{Client: client(server), Output: io.Discard}
	err := service.Run(context.Background(), root, cfg, commit.Filter{Type: "sync"})
	var partial *commit.PartialError
	if !errors.As(err, &partial) || !strings.Contains(partial.Skipped["sync:shop/orders"], "connector:shop is not committed and is not selected") {
		t.Fatalf("expected missing selected connector error, got %v", err)
	}
}
