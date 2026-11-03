package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"vortexify-cli/internal/config"
)

func TestSetProjectSecretHiddenPromptAndScopedRequest(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			if err := config.Save(root, &config.ProjectConfig{ProjectID: "project"}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("VORTEXIFY_TOKEN", "token")
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if r.URL.Path != "/api/v2/platform/projects/set_project_secret" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer token" || body["org_id"] != "org" || body["project_id"] != "project" || body["name"] != "demo_signing_key" || body["value"] != "hidden-value" {
					t.Error("incorrect secret request")
				}
				w.WriteHeader(status)
				fmt.Fprint(w, `{"value":"hidden-value"}`)
			}))
			defer server.Close()
			hidden := false
			cmd := newSetSecretCommand(&options{apiURL: server.URL, orgID: "org"}, func(*cobra.Command) func(bool) (string, error) {
				return func(secret bool) (string, error) { hidden = secret; return "hidden-value", nil }
			})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"demo_signing_key"})
			err := cmd.Execute()
			if !hidden || requests != 1 {
				t.Fatal("missing hidden prompt or request")
			}
			if status == 200 && (err != nil || !strings.Contains(out.String(), "Saved project secret")) {
				t.Fatal(out.String(), err)
			}
			if status == 403 && (err == nil || !strings.Contains(err.Error(), "HTTP 403")) {
				t.Fatal("expected failure", err)
			}
			if strings.Contains(out.String(), "hidden-value") || (err != nil && strings.Contains(err.Error(), "hidden-value")) {
				t.Fatal("secret leaked")
			}
		})
	}
}
func TestSetProjectSecretRequiresTerminal(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := config.Save(root, &config.ProjectConfig{ProjectID: "project"}); err != nil {
		t.Fatal(err)
	}
	cmd := newSecretCommand(&options{apiURL: "https://api.vortexify.dev", orgID: "org"})
	cmd.SetIn(strings.NewReader("hidden-value\n"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"set", "demo_signing_key"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatal("expected terminal requirement", err)
	}
}
