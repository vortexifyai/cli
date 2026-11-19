package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"vortexify-cli/internal/resources"
)

func TestClientAuthAndError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		http.Error(w, "denied", 403)
	}))
	defer server.Close()
	c := NewClient(server.URL)
	c.OrgID = "org"
	c.Token = func(context.Context) (string, error) { return "token", nil }
	_, err := c.ListResources(context.Background(), "project")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 403 {
		t.Fatalf("error %v", err)
	}
}
func TestClientContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewClient("http://localhost")
	c.Token = func(context.Context) (string, error) { return "token", nil }
	if _, err := c.Request(ctx, "GET", "/", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestRedirectStripsCredentials(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("leaked bearer token")
		}
		w.Write([]byte("[]"))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer source.Close()
	c := NewClient(source.URL)
	c.OrgID = "org"
	c.Token = func(context.Context) (string, error) { return "token", nil }
	if _, err := c.ListResources(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePullResourcesQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/artifacts/resources/get_resource_graph" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("incorrect graph request")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["org_id"] != "org" || body["project_id"] != "project" {
			t.Error("missing project scope")
		}
		json.NewEncoder(w).Encode([]resources.Registered{
			{ID: "root", Type: "app", Name: "name & id", UpstreamIDs: []string{"table"}},
			{ID: "table", Type: "table", Name: "data", UpstreamIDs: []string{"source"}},
			{ID: "source", Type: "transform", Name: "source"},
			{ID: "other", Type: "app", Name: "unrelated"},
		})
	}))
	defer server.Close()
	c := NewClient(server.URL)
	c.OrgID = "org"
	c.Token = func(context.Context) (string, error) { return "token", nil }
	entries, err := c.ResolvePullResources(context.Background(), "project", "app", "name & id", true, true)
	if err != nil || len(entries) != 2 || entries[0].ID != "root" {
		t.Fatalf("graph response: %v %v", entries, err)
	}
}

func TestProjectServiceContractWithPrefixedBaseURL(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" {
			t.Error("expected POST")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["org_id"] != "org" {
			t.Error("missing organization")
		}
		switch r.URL.Path {
		case "/api/v2/platform/projects/create_project":
			values, ok := body["values"].(map[string]any)
			if !ok || values["name"] != "demo" || values["description"] != "test project" {
				t.Error("wrong create payload", body)
			}
			if _, exists := body["project_id"]; exists {
				t.Error("new project must not have an existing ID")
			}
		case "/api/v2/platform/projects/get_project":
			if body["project_id"] != "project" {
				t.Error("missing project ID")
			}
		default:
			t.Error("wrong service path", r.URL.Path)
		}
		w.Write([]byte(`{"id":"project","name":"demo"}`))
	}))
	defer server.Close()
	c := NewClient(server.URL + "/api/v2")
	c.Token = func(context.Context) (string, error) { return "token", nil }
	c.OrgID = ""
	if _, err := c.CreateProject(context.Background(), "demo", "test project"); err == nil {
		t.Fatal("missing organization accepted")
	}
	if requests != 0 {
		t.Fatal("sent a request without organization")
	}
	c.OrgID = "org"
	if _, err := c.CreateProject(context.Background(), "demo", "test project"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetProject(context.Background(), "project"); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal(requests)
	}
}

type deadlineTransport func(*http.Request) (*http.Response, error)

func (f deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPIJobStartAllowsLongExecutionAndPreservesCancellationDeadline(t *testing.T) {
	for _, tc := range []struct {
		path     string
		explicit time.Duration
		want     time.Duration
	}{
		{"/api/v2/execution/jobs/start_resource_job", 0, 16 * time.Minute},
		{"/api/v2/execution/jobs/get_job_results", 0, 30 * time.Second},
		{"/api/v2/execution/jobs/start_resource_job", 2 * time.Second, 2 * time.Second},
	} {
		t.Run(tc.path+tc.explicit.String(), func(t *testing.T) {
			client := NewClient("https://example.com")
			client.Token = func(context.Context) (string, error) { return "token", nil }
			client.HTTPClient.Transport = deadlineTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining > tc.want || remaining < tc.want-time.Second {
					t.Errorf("unexpected deadline: %v", remaining)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
			})
			ctx := context.Background()
			if tc.explicit > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.explicit)
				defer cancel()
			}
			response, err := client.Request(ctx, "POST", tc.path, map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if client.HTTPClient.Timeout != 30*time.Second {
				t.Fatal("ordinary request timeout changed")
			}
		})
	}
}
