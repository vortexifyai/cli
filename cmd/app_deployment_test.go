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

func TestAppSubdomainHelpUsesHostingDomain(t *testing.T) {
	for _, action := range []string{"deploy", "upgrade"} {
		out, err := run(t, action, "--help")
		if err != nil {
			t.Fatal(err)
		}
		for _, domain := range []string{"orders.apps.vortexify.dev", "orders.apps.vortexify.ai"} {
			if !strings.Contains(out, domain) {
				t.Fatalf("missing %s in help: %s", domain, out)
			}
		}
	}
}

func TestAppDeploymentPrintsPublishedURL(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(root, config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"deploy", "upgrade"} {
		for _, domain := range []string{"orders.apps.vortexify.dev", "orders.apps.vortexify.ai"} {
			for _, wait := range []bool{false, true} {
				t.Run(action+"/"+domain+"/wait="+map[bool]string{false: "false", true: "true"}[wait], func(t *testing.T) {
					polled := false
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/api/v2/artifacts/resources/list_resources":
							json.NewEncoder(w).Encode([]resources.Registered{{ID: "app", Type: "app", Name: "orders", Version: 2}})
						case "/api/v2/execution/jobs/deploy_resource":
							json.NewEncoder(w).Encode(map[string]any{"job_id": "deployment", "type": "app-deploy", "status": "pending", "completed": false, "url": "https://" + domain + "/"})
						case "/api/v2/execution/jobs/get_job_results":
							polled = true
							json.NewEncoder(w).Encode(map[string]any{"job_id": "deployment", "type": "app-deploy", "status": "success", "completed": true, "url": "https://" + domain + "/"})
						default:
							t.Errorf("unexpected request: %s", r.URL.Path)
							http.NotFound(w, r)
						}
					}))
					defer server.Close()
					args := []string{"--api-url", server.URL, "--org-id", "org", action, "app", "orders", "--subdomain", "orders"}
					if wait {
						args = append(args, "--wait")
					}
					out, err := run(t, args...)
					if err != nil {
						t.Fatal(out, err)
					}
					if !strings.Contains(out, "URL: https://"+domain+"/") || polled != wait {
						t.Fatalf("wait=%v polled=%v output: %s", wait, polled, out)
					}
				})
			}
		}
	}
}
