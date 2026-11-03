package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

type Execution map[string]any

func (c *Client) StartResourceJob(ctx context.Context, projectID string, values map[string]any) (Execution, error) {
	response, err := c.Service(ctx, "execution/jobs/start_resource_job", projectID, values)
	if err != nil {
		return nil, err
	}
	result, err := decode[Execution](response)
	if err != nil {
		return nil, err
	}
	return *result, nil
}
func (c *Client) DeployResource(ctx context.Context, projectID string, values map[string]any) (Execution, error) {
	response, err := c.Service(ctx, "execution/jobs/deploy_resource", projectID, values)
	if err != nil {
		return nil, err
	}
	result, err := decode[Execution](response)
	if err != nil {
		return nil, err
	}
	return *result, nil
}
func (c *Client) GetJobResults(ctx context.Context, projectID, jobID string) (Execution, error) {
	response, err := c.Service(ctx, "execution/jobs/get_job_results", projectID, map[string]any{"job_id": jobID})
	if err != nil {
		return nil, err
	}
	result, err := decode[Execution](response)
	if err != nil {
		return nil, err
	}
	return *result, nil
}
func (c *Client) PreviewStatus(ctx context.Context, projectID string) ([]Execution, error) {
	response, err := c.Service(ctx, "execution/jobs/get_preview_status", projectID, map[string]any{})
	if err != nil {
		return nil, err
	}
	result, err := decode[[]Execution](response)
	if err != nil {
		return nil, err
	}
	return *result, nil
}

// StreamJobStatus reads the API's local SSE bridge, never the watcher Redis.
func (c *Client) StreamJobStatus(ctx context.Context, projectID, jobID, after string) (*http.Response, error) {
	if err := c.ProjectOrg(ctx, projectID); err != nil {
		return nil, err
	}
	data, err := json.Marshal(map[string]string{"org_id": c.OrgID, "project_id": projectID, "job_id": jobID, "last_event_id": after})
	if err != nil {
		return nil, err
	}
	return c.request(ctx, http.MethodPost, "/api/v2/execution/jobs/stream_job_status", bytes.NewReader(data), "application/json", true, "text/event-stream")
}
