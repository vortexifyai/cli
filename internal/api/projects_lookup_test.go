package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetProjectWithoutOrganization(t *testing.T) {
	t.Setenv("VORTEXIFY_ORG_ID", "ignored-default")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/api/v2/platform/projects/get_project" || body["project_id"] != "project" || body["org_id"] != nil || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("incorrect project lookup")
		}
		w.Write([]byte(`{"id":"project","org_id":"org","name":"demo"}`))
	}))
	defer server.Close()
	client := NewClient(server.URL)
	client.Token = func(context.Context) (string, error) { return "token", nil }
	project, err := client.GetProject(context.Background(), "project")
	if err != nil {
		t.Fatal(err)
	}
	if project.ID != "project" || project.OrgID != "org" {
		t.Fatal("missing project context", project)
	}
}

func TestProjectServiceDerivesOrganizationAndReusesIt(t *testing.T) {
	t.Setenv("VORTEXIFY_ORG_ID", "ignored-default")
	lookups, requests := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/api/v2/platform/projects/get_project" {
			lookups++
			if body["org_id"] != nil || body["project_id"] != "project" {
				t.Error("lookup inherited organization", body)
			}
			w.Write([]byte(`{"id":"project","org_id":"actual-org"}`))
			return
		}
		requests++
		if body["org_id"] != "actual-org" || body["project_id"] != "project" {
			t.Error("wrong derived scope", body)
		}
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := NewClient(server.URL)
	client.Token = func(context.Context) (string, error) { return "token", nil }
	for range 2 {
		resp, err := client.Service(context.Background(), "artifacts/resources/get_resource_graph", "project", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if lookups != 1 || requests != 2 {
		t.Fatal(lookups, requests)
	}
}

func TestProjectServiceRejectsInconsistentLookup(t *testing.T) {
	for _, result := range []string{`{"id":"wrong","org_id":"org"}`, `{"id":"project"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v2/platform/projects/get_project" {
				t.Error("sent service request after invalid lookup")
			}
			w.Write([]byte(result))
		}))
		client := NewClient(server.URL)
		client.Token = func(context.Context) (string, error) { return "token", nil }
		if _, err := client.Service(context.Background(), "artifacts/resources/get_resource_graph", "project", map[string]any{}); err == nil {
			t.Fatal("accepted inconsistent lookup", result)
		}
		server.Close()
	}
}
