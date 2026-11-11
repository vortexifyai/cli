package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
)

func TestPreviewRunWaitSubmitsOnceAndReturnsStructuredResults(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(root, config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	submitted := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			json.NewEncoder(w).Encode([]resources.Registered{{ID: "resource", Type: "transform", Name: "orders", Version: 3, HasCode: true}})
		case "/api/v2/execution/jobs/start_resource_job":
			submitted++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["action"] != "run" || body["resource_type"] != "transform" || body["version"] != float64(3) || body["rebuild_downstream"] != true || body["org_id"] != "org" {
				t.Error(body)
			}
			json.NewEncoder(w).Encode(map[string]any{"job_id": "job", "status": "pending", "resource_version": 3, "completed": false})
		case "/api/v2/execution/jobs/get_job_results":
			json.NewEncoder(w).Encode(map[string]any{"job_id": "job", "status": "success", "resource_version": 3, "completed": true, "validation": map[string]any{"status": "issues_found"}, "outputs": map[string]any{"table": map[string]any{"data_keys": []string{"part.parquet"}}}})
		default:
			t.Error("Unexpected side effect", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "run", "transform", "orders", "--version", "3", "--rebuild-downstream", "--wait", "--json")
	if err != nil {
		t.Fatal(out, err)
	}
	if submitted != 1 {
		t.Fatal("submitted more than once", submitted)
	}
	var result map[string]any
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(out, err)
	}
	if result["status"] != "success" || result["outputs"] == nil {
		t.Fatal(result)
	}
}
func TestDeployAndUpgradeLeaveLatestSelectionToServer(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(root, config.Default("project", "demo", ""))
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			json.NewEncoder(w).Encode([]resources.Registered{{ID: "app", Type: "app", Name: "dashboard", Version: 4}})
		case "/api/v2/execution/jobs/deploy_resource":
			json.NewDecoder(r.Body).Decode(&received)
			json.NewEncoder(w).Encode(map[string]any{"resource_id": "app", "deployed_version": 4, "status": "deployed"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, action := range []string{"deploy", "upgrade"} {
		received = nil
		if _, err := run(t, "--api-url", server.URL, "--org-id", "org", action, "app", "dashboard"); err != nil {
			t.Fatal(err)
		}
		if received["action"] != action || received["version"] != nil {
			t.Fatal(received)
		}
	}
	received = nil
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "deploy", "app", "dashboard", "--version", "2"); err != nil {
		t.Fatal(err)
	}
	if received["version"] != float64(2) {
		t.Fatal(received)
	}
	received = nil
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "deploy", "app", "dashboard", "--version", "0"); err == nil {
		t.Fatal("accepted zero version")
	}
	if received != nil {
		t.Fatal("invalid version reached deployment API")
	}
}
func TestLogsIncludesAppAndShellBuildChildren(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(root, config.Default("project", "demo", ""))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/execution/jobs/get_job_results" {
			t.Error(r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		id := body["job_id"].(string)
		result := map[string]any{"job_id": id, "logs": id + " log"}
		if id == "deploy" {
			result["build_job_id"] = "app-build"
			result["shell_build_job_id"] = "shell-build"
			result["schema_discovery_job_ids"] = []string{"discovery"}
		}
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "logs", "deploy")
	if err != nil || !strings.Contains(out, "app-build log") || !strings.Contains(out, "shell-build log") || !strings.Contains(out, "discovery log") {
		t.Fatal(out, err)
	}
}

func TestLogsShowsEvictionReasonWhenContainerLogsAreUnavailable(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(root, config.Default("project", "demo", ""))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"job_id": "build", "logs": "", "status": "failed",
			"logs_error":          "Container logs unavailable",
			"failure_reason_code": "POD_EVICTED",
			"failure_message":     "Pod exceeded ephemeral storage limit of 2Gi",
		})
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "logs", "build")
	if err != nil || !strings.Contains(out, "Pod exceeded ephemeral storage limit of 2Gi") || !strings.Contains(out, "Logs unavailable:") {
		t.Fatal(out, err)
	}
}

func TestAppDeploymentSubdomain(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(root, config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			w.Write([]byte(`[{"id":"app-id","name":"orders","type":"app"}]`))
		case "/api/v2/execution/jobs/deploy_resource":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["subdomain"] != "orders" || body["resource_id"] != "app-id" || body["action"] != "upgrade" {
				t.Error(body)
			}
			w.Write([]byte(`{"status":"deployed"}`))
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "upgrade", "app", "orders", "--subdomain", "orders"); err != nil {
		t.Fatal(err)
	}
}

func TestProductionTransformRunSendsExplicitEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(".", config.Default("project", "demo", ""))
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			json.NewEncoder(w).Encode([]resources.Registered{{ID: "transform", Type: "transform", Name: "orders", Version: 3}})
		case "/api/v2/execution/jobs/start_resource_job":
			json.NewDecoder(r.Body).Decode(&received)
			json.NewEncoder(w).Encode(map[string]any{"job_id": "job", "status": "pending"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if out, err := run(t, "--api-url", server.URL, "--org-id", "org", "run", "transform", "orders", "--production"); err != nil {
		t.Fatal(out, err)
	}
	if received["production"] != true || received["action"] != "run" {
		t.Fatal(received)
	}
}

func TestAppDeploymentBuildMemoryUsesExistingSetting(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	config.Save(".", config.Default("project", "demo", ""))
	memory := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/artifacts/resources/list_resources":
			json.NewEncoder(w).Encode([]resources.Registered{{ID: "app", Type: "app", Name: "dashboard", Version: 3}})
		case "/api/v2/artifacts/apps/set_build_memory":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["memory_mib"] != float64(8192) || body["app_id"] != "app" {
				t.Error(body)
			}
			memory = true
			w.Write([]byte(`{}`))
		case "/api/v2/execution/jobs/deploy_resource":
			if !memory {
				t.Error("deployment started before setting memory")
			}
			w.Write([]byte(`{"status":"pending","job_id":"job"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if out, err := run(t, "--api-url", server.URL, "--org-id", "org", "deploy", "app", "dashboard", "--build-memory", "8192"); err != nil {
		t.Fatal(out, err)
	}
	if !memory {
		t.Fatal("memory setting was not applied")
	}
}
