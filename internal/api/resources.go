package api

import (
	"context"
	"fmt"
	"io"
	"vortexify-cli/internal/resources"
)

type Environment struct {
	Status         string         `json:"status"`
	Ready          bool           `json:"ready"`
	Version        int            `json:"version"`
	Image          string         `json:"image"`
	ImageKind      string         `json:"image_kind"`
	ImageAvailable bool           `json:"image_available"`
	LambdaName     string         `json:"lambda_name,omitempty"`
	Requirements   []string       `json:"requirements"`
	Compute        map[string]any `json:"compute"`
	Error          string         `json:"error,omitempty"`
}

func (c *Client) GetFunctionEnvironment(ctx context.Context, projectID, functionID string, version int) (*Environment, error) {
	values := map[string]any{"function_id": functionID}
	if version > 0 {
		values["version"] = version
	}
	response, err := c.Service(ctx, "artifacts/functions/get_function_environment", projectID, values)
	if err != nil {
		return nil, err
	}
	return decode[Environment](response)
}

type Resource struct {
	Files            *resources.FileStatus `json:"files,omitempty"`
	Environment      *Environment          `json:"environment,omitempty"`
	ID               string                `json:"id"`
	Type             string                `json:"type"`
	Name             string                `json:"name"`
	Version          int                   `json:"version"`
	CodeHash         string                `json:"code_hash"`
	HasCode          bool                  `json:"has_code"`
	DeploymentStatus string                `json:"deployment_status"`
	ConnectorID      string                `json:"connector_id"`
	HealthStatus     string                `json:"health_status"`
	PlanStatus       string                `json:"plan_status"`
}

func (c *Client) ListResources(ctx context.Context, projectID string) ([]Resource, error) {
	rows, err := c.GetResourceGraph(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return CodeResources(rows), nil
}

// Expand the server's authoritative graph through upstream/downstream edges,
// including intermediate table nodes, and return only code resources to pull.
func (c *Client) ResolvePullResources(ctx context.Context, projectID, resourceType, resource string, upstream, downstream bool) ([]Resource, error) {
	rows, err := c.GetResourceGraph(ctx, projectID)
	if err != nil {
		return nil, err
	}
	entries := []resources.RegistryEntry{}
	for _, row := range rows {
		entries = append(entries, resources.RegistryEntry{ID: row.ID, Type: row.Type, Name: row.Name})
	}
	if err := (resources.Filter{Type: resourceType, Resource: resource}).CheckSelection(entries); err != nil {
		return nil, err
	}
	roots := []string{}
	parents, children := map[string][]string{}, map[string][]string{}
	for _, row := range rows {
		if row.Type != "table" && row.Type != "secret" && (resourceType == "" || row.Type == resourceType) && (resources.Filter{Type: resourceType, Resource: resource}).Match(row.Type, row.Name, row.ID) {
			roots = append(roots, row.ID)
		}
		parents[row.ID] = row.UpstreamIDs
		for _, id := range row.UpstreamIDs {
			children[id] = append(children[id], row.ID)
		}
	}
	if len(roots) != 1 {
		return nil, fmt.Errorf("must resolve exactly one root resource; use --type or a resource ID (found %d)", len(roots))
	}
	selected := map[string]bool{roots[0]: true}
	visit := func(edges map[string][]string) {
		seen := map[string]bool{}
		var walk func(string)
		walk = func(id string) {
			if seen[id] {
				return
			}
			seen[id], selected[id] = true, true
			for _, next := range edges[id] {
				walk(next)
			}
		}
		walk(roots[0])
	}
	if upstream {
		visit(parents)
	}
	if downstream {
		visit(children)
	}
	filtered := []resources.Registered{}
	for _, row := range rows {
		if selected[row.ID] {
			filtered = append(filtered, row)
		}
	}
	return CodeResources(filtered), nil
}

type CommitResourceRequest struct {
	ResourceID  string    `json:"resource_id,omitempty"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	CodeHash    string    `json:"code_hash"`
	BaseVersion int       `json:"base_version"`
	Archive     io.Reader `json:"-"`
	ArchiveSize int64     `json:"archive_size"`
}

// ListRegisteredResources resolves execution names without fetching source/graph details.
func (c *Client) ListRegisteredResources(ctx context.Context, projectID string) ([]resources.Registered, error) {
	response, err := c.Service(ctx, "artifacts/resources/list_resources", projectID, map[string]any{})
	if err != nil {
		return nil, err
	}
	rows, err := decode[[]resources.Registered](response)
	if err != nil {
		return nil, err
	}
	return resources.QualifySyncNames(*rows)
}

func (c *Client) GetResourceGraph(ctx context.Context, projectID string) ([]resources.Registered, error) {
	response, err := c.Service(ctx, "artifacts/resources/get_resource_graph", projectID, map[string]any{})
	if err != nil {
		return nil, err
	}
	rows, err := decode[[]resources.Registered](response)
	if err != nil {
		return nil, err
	}
	return resources.QualifySyncNames(*rows)
}
func CodeResources(rows []resources.Registered) []Resource {
	result := []Resource{}
	for _, r := range rows {
		if r.Type == "table" || r.Type == "secret" {
			continue
		}
		result = append(result, Resource{ID: r.ID, Type: r.Type, Name: r.Name, Version: r.Version, CodeHash: r.CodeHash, HasCode: r.HasCode, ConnectorID: r.ConnectorID, Files: r.Files})
	}
	return result
}
func (c *Client) CreateResource(ctx context.Context, projectID, kind, name, connectorID string) (*Resource, error) {
	values := map[string]any{"name": name}
	if kind == "sync" {
		values["name"] = resources.SyncBaseName(name)
	}
	if kind == "sync" {
		if connectorID == "" {
			return nil, fmt.Errorf("sync creation requires --connector")
		}
		values["connector_id"] = connectorID
	}
	response, err := c.Service(ctx, "artifacts/resources/initialize_resource", projectID, map[string]any{"resource_type": kind, "values": values})
	if err != nil {
		return nil, err
	}
	row, err := decode[Resource](response)
	if err == nil && kind == "sync" && row.Name == resources.SyncBaseName(name) {
		row.Name = name
	}
	return row, err
}
func sourceKind(kind string) string {
	if kind == "api" || kind == "transform" {
		return "function"
	}
	return kind
}
func (c *Client) PullSource(ctx context.Context, projectID string, resource Resource) (io.ReadCloser, error) {
	values := map[string]any{"resource_type": sourceKind(resource.Type), "resource_id": resource.ID}
	if resource.HasCode && resource.Version > 0 {
		values["version"] = resource.Version
	}
	response, err := c.Service(ctx, "execution/code/pull_resource", projectID, values)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}
