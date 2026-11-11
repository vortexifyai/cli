package api

import (
	"context"
	"net/http"
	"time"

	"vortexify-cli/internal/config"
)

type Project struct {
	OrgID       string                `json:"org_id"`
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Config      *config.ProjectConfig `json:"config,omitempty"`
}

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	resp, err := c.Request(ctx, "POST", "/api/v2/identity/organizations/list_user_orgs", map[string]any{})
	if err != nil {
		return nil, err
	}
	rows, err := decode[[]Organization](resp)
	if err != nil {
		return nil, err
	}
	return *rows, nil
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	resp, err := c.Service(ctx, "platform/projects/list_projects", "", map[string]any{})
	if err != nil {
		return nil, err
	}
	rows, err := decode[[]Project](resp)
	if err != nil {
		return nil, err
	}
	return *rows, nil
}

func (c *Client) SearchProjects(ctx context.Context, query string, conversationID ...string) (map[string]any, error) {
	values := map[string]any{"query": query}
	if len(conversationID) > 0 && conversationID[0] != "" {
		values["conversation_id"] = conversationID[0]
	}
	resp, err := c.DiscoveryService(ctx, "platform/projects/search_projects", "", values)
	if err != nil {
		return nil, err
	}
	result, err := decode[map[string]any](resp)
	if err != nil {
		return nil, err
	}
	return *result, nil
}

func (c *Client) CreateProject(ctx context.Context, name, description string) (*Project, error) {
	resp, err := c.Service(ctx, "platform/projects/create_project", "", map[string]any{"values": map[string]string{"name": name, "description": description}})
	if err != nil {
		return nil, err
	}
	return decode[Project](resp)
}
func (c *Client) GetProject(ctx context.Context, id string) (*Project, error) {
	values := map[string]any{"project_id": id}
	if c.OrgID != "" {
		values["org_id"] = c.OrgID
	}
	resp, err := c.Request(ctx, "POST", "/api/v2/platform/projects/get_project", values)
	if err != nil {
		return nil, err
	}
	return decode[Project](resp)
}

// SetProjectSecret saves a value through the existing project-scoped secret service.
func (c *Client) SetProjectSecret(ctx context.Context, projectID, name, value string) error {
	resp, err := c.Service(ctx, "platform/projects/set_project_secret", projectID, map[string]any{"name": name, "value": value})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// KnowledgeGraphCapacity previews enablement without changing project settings.
type KnowledgeGraphCapacity struct {
	Enabled           bool   `json:"enabled"`
	Datasets          int    `json:"datasets"`
	ProjectPairs      int    `json:"project_pairs"`
	OrganizationPairs int    `json:"organization_pairs"`
	Limit             *int   `json:"limit"`
	Available         bool   `json:"available"`
	Plan              string `json:"plan"`
}

func (c *Client) KnowledgeGraphCapacity(ctx context.Context, projectID string) (*KnowledgeGraphCapacity, error) {
	resp, err := c.Service(ctx, "platform/projects/get_project_knowledge_graph_capacity", projectID, map[string]any{})
	if err != nil {
		return nil, err
	}
	return decode[KnowledgeGraphCapacity](resp)
}

func (c *Client) SetKnowledgeGraph(ctx context.Context, projectID string, enabled bool) error {
	action := "disable_project_knowledge_graph"
	if enabled {
		action = "enable_project_knowledge_graph"
	}
	resp, err := c.Service(ctx, "platform/projects/"+action, projectID, map[string]any{})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Investigations may make several model/tool calls; other API requests retain their normal timeout.
func (c *Client) DiscoveryService(ctx context.Context, endpoint, projectID string, values map[string]any) (*http.Response, error) {
	scoped := *c
	httpClient := *c.HTTPClient
	httpClient.Timeout = 10 * time.Minute
	if transport, ok := httpClient.Transport.(*http.Transport); ok {
		cloned := transport.Clone()
		cloned.ResponseHeaderTimeout = 10 * time.Minute
		httpClient.Transport = cloned
	}
	scoped.HTTPClient = &httpClient
	return scoped.Service(ctx, endpoint, projectID, values)
}
