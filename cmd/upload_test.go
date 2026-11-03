package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
)

func TestDataUploadFlow(t *testing.T) {
	for _, mode := range []string{"table", "file", "selection", "failed-put"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("VORTEXIFY_TOKEN", "cli-token")
			if err := config.Save(root, config.Default("project", "demo", "")); err != nil {
				t.Fatal(err)
			}
			filename := "sample.csv"
			kind := "table"
			if mode == "file" || mode == "selection" {
				filename, kind = "sample.md", "file"
			}
			const contents = "id,name\n1,test\n"
			if err := os.WriteFile(filename, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			puts, starts, polls := 0, 0, 0
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				puts++
				data, _ := io.ReadAll(r.Body)
				if r.Method != "PUT" || string(data) != contents || r.ContentLength != int64(len(contents)) || r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/octet-stream" {
					t.Error("incorrect storage request", r.Header, string(data))
				}
				if mode == "failed-put" {
					w.WriteHeader(403)
					w.Write([]byte("private-signature"))
				}
			}))
			defer storage.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || body["org_id"] != "org" || body["project_id"] != "project" || r.Header.Get("Authorization") != "Bearer cli-token" {
					t.Error("missing API scope/auth", body)
				}
				switch r.URL.Path {
				case "/api/v2/artifacts/resources/get_resource_graph":
					json.NewEncoder(w).Encode([]resources.Registered{{ID: "owner", Type: "connector", Name: "shop"}})
				case "/api/v2/data/uploads/get_upload_s3_url":
					if body["filename"] != filename || body["content_type"] != "application/octet-stream" {
						t.Error(body)
					}
					json.NewEncoder(w).Encode(map[string]any{"upload_id": "upload", "upload_url": storage.URL + "?signature=private-signature"})
				case "/api/v2/data/uploads/upload_table", "/api/v2/data/uploads/upload_resource_file":
					starts++
					if puts != 1 || body["upload_id"] != "upload" || body["name"] != "Imported" {
						t.Error("submission before upload", body)
					}
					if kind == "file" {
						if r.URL.Path != "/api/v2/data/uploads/upload_resource_file" || body["resource_type"] != "connector" || body["resource_id"] != "owner" {
							t.Error(body)
						}
					} else if body["resource_id"] != nil || r.URL.Path != "/api/v2/data/uploads/upload_table" {
						t.Error(body)
					}
					json.NewEncoder(w).Encode(map[string]any{"id": "job", "status": "running", "config": map[string]any{"internal": "do-not-print"}})
				case "/api/v2/execution/jobs/get_job_results":
					polls++
					if body["job_id"] != "job" || starts != 1 {
						t.Error(body)
					}
					json.NewEncoder(w).Encode(map[string]any{"job_id": "job", "status": "success", "completed": true, "result": map[string]any{"table_id": "published"}})
				default:
					t.Error("unexpected route", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			args := []string{"--api-url", server.URL, "--org-id", "org", "upload", kind, filename, "--name", "Imported"}
			if mode == "table" {
				args = append(args, "--wait", "--json")
			}
			if mode == "file" {
				args = append(args, "--resource-type", "connector", "--resource", "shop", "--json")
			}
			command := NewRootCommand()
			command.SetArgs(args)
			command.SetIn(strings.NewReader("1\n"))
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			err := command.Execute()
			if mode == "failed-put" {
				if err == nil || starts != 0 || strings.Contains(err.Error(), "private-signature") {
					t.Fatal(err, starts)
				}
			} else if err != nil || starts != 1 || !strings.Contains(output.String(), "job") {
				t.Fatal(err, starts, output.String())
			}
			if mode == "table" && polls != 1 {
				t.Fatal("wait did not poll", polls)
			}
			if strings.Contains(output.String(), "do-not-print") || strings.Contains(output.String(), "private-signature") {
				t.Fatal("leaked internal upload data")
			}
		})
	}
}

func TestDataUploadRejectsInvalidLocalFileBeforeNetwork(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	os.WriteFile("empty.csv", nil, 0600)
	os.WriteFile("bad.exe", []byte("data"), 0600)
	for _, path := range []string{"empty.csv", "bad.exe", "missing.csv"} {
		command := NewRootCommand()
		command.SetArgs([]string{"upload", "table", path})
		if err := command.Execute(); err == nil {
			t.Fatal("accepted", path)
		}
	}
}
