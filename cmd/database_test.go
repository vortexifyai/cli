package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"vortexify-cli/internal/config"
)

func TestDatabaseStatusAndApprovalDecisionsUseScopedServices(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	decisions := []string{}
	backupStatus := `{"latest_backup_jobs":[{"id":"origin-backup","environment":"origin","status":"success","resource_version":1},{"id":"preview-backup","environment":"preview","status":"running","resource_version":2}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.Method != "POST" || body["org_id"] != "org" || body["project_id"] != "project" {
			t.Error("missing scope", body)
		}
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			w.Write([]byte(`[{"id":"db","type":"database","name":"demo_store","version":2}]`))
		case "/api/v2/artifacts/resources/get_resource":
			if body["resource_type"] != "database" || body["resource_id"] != "db" {
				t.Error(body)
			}
			w.Write([]byte(`{"id":"db","name":"demo_store","version":2,"migration_approval_required":true}`))
		case "/api/v2/data/databases/get_pending_database_migrations":
			if body["database_id"] != "db" {
				t.Error(body)
			}
			w.Write([]byte(`[{"version":2,"environment":"origin","migration_paths":["migrations/001_initial.sql"]}]`))
		case "/api/v2/execution/jobs/list_jobs":
			if body["resource_type"] != "database" || body["resource_id"] != "db" || body["type"] != "database-migrate" {
				t.Error(body)
			}
			w.Write([]byte(`[{"id":"old-job","status":"success","resource_version":1,"config":{"private":"hidden"}}]`))
		case "/api/v2/data/databases/get_database_status":
			if body["database_id"] != "db" {
				t.Error(body)
			}
			w.Write([]byte(backupStatus))
		case "/api/v2/data/databases/approve_database_migration", "/api/v2/data/databases/reject_database_migration":
			if body["database_id"] != "db" || body["version"] != float64(2) || body["environment"] != "origin" {
				t.Error(body)
			}
			decisions = append(decisions, r.URL.Path)
			if strings.Contains(r.URL.Path, "/approve_") {
				w.Write([]byte(`{"approved":true,"version":2,"environment":"origin","migration_result":{"id":"new-job","status":"running","resource_version":2}}`))
			} else {
				w.Write([]byte(`{"approved":false,"version":2,"environment":"origin"}`))
			}
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := []string{"--api-url", server.URL, "--org-id", "org", "database", "demo_store"}
	out, err := run(t, base...)
	if err != nil {
		t.Fatal(out, err)
	}
	for _, expected := range []string{"Migration approval required: true", "Pending approval: version 2 (origin)", "migrations/001_initial.sql", "Migration job: old-job", "origin  success", "Backup job: origin-backup", "preview  running", "Backup job: preview-backup"} {
		if !strings.Contains(out, expected) {
			t.Fatal(expected, out)
		}
	}
	if strings.Contains(out, "hidden") {
		t.Fatal("raw job config leaked", out)
	}
	out, err = run(t, append(base, "--json")...)
	var structured map[string]any
	if err != nil || json.Unmarshal([]byte(out), &structured) != nil || len(structured["latest_backups"].([]any)) != 2 {
		t.Fatal(out, err)
	}
	backupStatus = `{"latest_backup_jobs":[]}`
	out, err = run(t, base...)
	if err != nil || !strings.Contains(out, "Backups: no jobs yet") {
		t.Fatal(out, err)
	}
	out, err = run(t, append(base, "--approve", "2")...)
	if err != nil || !strings.Contains(out, "Approved demo_store version 2") || !strings.Contains(out, "Migration job: new-job") {
		t.Fatal(out, err)
	}
	out, err = run(t, append(base, "--reject", "2", "--json")...)
	if err != nil || !strings.Contains(out, `"approved":false`) {
		t.Fatal(out, err)
	}
	if len(decisions) != 2 {
		t.Fatal(decisions)
	}
	for _, flags := range [][]string{{"--approve", "0"}, {"--approve", "-1"}, {"--environment", "bad"}, {"--approve", "2", "--reject", "2"}} {
		if _, err = run(t, append(base, flags...)...); err == nil {
			t.Fatal("invalid flags accepted", flags)
		}
	}
	if len(decisions) != 2 {
		t.Fatal("invalid input changed approval", decisions)
	}
}

func TestDatabaseFailedPreviewMigrationHasLogsAndScopedRetry(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/get_resource_graph":
			w.Write([]byte(`[{"id":"db","type":"database","name":"floor_ops","version":2}]`))
		case "/api/v2/artifacts/resources/get_resource":
			w.Write([]byte(`{"id":"db","name":"floor_ops","version":2}`))
		case "/api/v2/data/databases/get_pending_database_migrations":
			w.Write([]byte(`[]`))
		case "/api/v2/execution/jobs/list_jobs":
			w.Write([]byte(`[{"id":"origin-success","status":"success","resource_version":2,"config":{"environment":"origin"}},{"id":"preview-failed","status":"failed","resource_version":2,"config":{"environment":"preview","watcher_error":"guardian unavailable","secret":"never display"}},{"id":"older-preview","status":"failed","resource_version":1,"config":{"environment":"preview"}}]`))
		case "/api/v2/data/databases/get_database_status":
			w.Write([]byte(`{"environments":[{"environment":"origin","version":2},{"environment":"preview","version":0}],"latest_backup_jobs":[]}`))
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "database", "floor_ops")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, expected := range []string{"Migration job: preview-failed", "vortexify logs preview-failed", "--migrate 2 --environment preview", "guardian unavailable", "Migration job: origin-success"} {
		if !strings.Contains(out, expected) {
			t.Fatal(expected, out)
		}
	}
	if strings.Contains(out, "older-preview") || strings.Contains(out, "never display") {
		t.Fatal(out)
	}
}
