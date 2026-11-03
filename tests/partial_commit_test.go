package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/commit"
	"vortexify-cli/internal/state"
)

func TestCommitSkipsBlockedBranchesAndCommitsIndependentChanges(t *testing.T) {
	for _, failure := range []string{"missing table", "stale baseline", "cycle", "server validation", "server conflict"} {
		t.Run(failure, func(t *testing.T) {
			root, cfg := workspace(t)
			source := "from vortexify import vxfy_transform, Table, TransformOutput\n@vxfy_transform(inputs={%s})\ndef run(**kwargs): return None\n"
			blockedInput := ""
			if failure == "missing table" {
				blockedInput = `"rows":Table("shop.orders")`
			} else if failure == "cycle" {
				blockedInput = `"rows":TransformOutput("b_consumer")`
			}
			file(t, root, "transforms/a_blocked/main.py", strings.Replace(source, "%s", blockedInput, 1))
			file(t, root, "transforms/b_consumer/main.py", strings.Replace(source, "%s", `"rows":TransformOutput("a_blocked")`, 1))
			file(t, root, "transforms/c_ready/main.py", strings.Replace(source, "%s", `"rows":TransformOutput("z_source")`, 1))
			file(t, root, "transforms/z_source/main.py", strings.Replace(source, "%s", "", 1))
			remote := []api.Resource{}
			if failure == "stale baseline" {
				remote = append(remote, api.Resource{ID: "a_blocked", Type: "transform", Name: "a_blocked", HasCode: true, Version: 2, CodeHash: "server"})
				if err := state.Save(root, &state.State{Resources: map[string]state.ResourceState{
					"transform:a_blocked": {ResourceID: "a_blocked", Version: 1, CodeHash: "base"},
				}}); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			created, committed := []string{}, []string{}
			rows := map[string]api.Resource{}
			hashes := map[string]string{}
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
			}))
			defer storage.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/get_resource_graph":
					json.NewEncoder(w).Encode(remote)
				case "/api/v2/artifacts/resources/initialize_resource":
					if !strings.Contains(out.String(), "Committing ") {
						t.Error("mutation preceded plan output")
					}
					name := body["values"].(map[string]any)["name"].(string)
					created = append(created, name)
					if name == "a_blocked" && failure == "server validation" {
						w.WriteHeader(400)
						io.WriteString(w, `{"message":"source input is invalid"}`)
						return
					}
					row := api.Resource{ID: name, Type: "transform", Name: name}
					rows[name] = row
					json.NewEncoder(w).Encode(row)
				case "/api/v2/execution/code/get_commit_s3_url":
					id := body["resource_id"].(string)
					hashes[id] = body["expected_hash"].(string)
					json.NewEncoder(w).Encode(api.CommitUpload{UploadID: id, UploadURL: storage.URL})
				case "/api/v2/execution/code/commit_resource":
					id := body["resource_id"].(string)
					if id == "a_blocked" && failure == "server conflict" {
						w.WriteHeader(409)
						io.WriteString(w, `{"message":"version changed"}`)
						return
					}
					row := rows[id]
					row.HasCode, row.Version, row.CodeHash = true, 1, hashes[id]
					committed = append(committed, id)
					json.NewEncoder(w).Encode(row)
				default:
					t.Error("unexpected request", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			err := (&commit.Service{Client: client(server), Output: &out}).Run(context.Background(), root, cfg, commit.Filter{Type: "transform"})
			var partial *commit.PartialError
			if !errors.As(err, &partial) || partial.Committed != 2 || len(partial.Skipped) != 2 {
				t.Fatalf("result=%v output=%s", err, out.String())
			}
			if strings.Join(committed, ",") != "z_source,c_ready" || strings.Contains(strings.Join(created, ","), "b_consumer") {
				t.Fatal("wrong branch/order", created, committed)
			}
			if !strings.Contains(out.String(), "Skipping 2 resources:") || strings.Contains(out.String(), "No changes to commit.") {
				t.Fatal(out.String())
			}
			if failure != "server validation" && failure != "server conflict" && !strings.HasPrefix(out.String(), "Committing 2 resources.\n") {
				t.Fatal("incorrect ready count", out.String())
			}
			st, err := state.Load(root)
			if err != nil || st.Resources["transform:z_source"].Version != 1 || st.Resources["transform:c_ready"].Version != 1 || st.Resources["transform:b_consumer"].Version != 0 {
				t.Fatal("incorrect state", st, err)
			}
		})
	}
}
