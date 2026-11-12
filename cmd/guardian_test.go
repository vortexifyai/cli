package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"vortexify-cli/internal/config"
)

func TestGuardianStatusUsesProjectWithoutTaskID(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(".", config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/platform/projects/get_guardian_status" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		w.Write([]byte(`{"guardian_status":"ready","guardian_error":null}`))
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "guardian", "--wait")
	if err != nil || !strings.Contains(out, "Project guardian: ready") {
		t.Fatal(out, err)
	}
}
