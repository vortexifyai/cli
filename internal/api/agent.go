package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type AgentRun struct {
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
}

func (c *Client) QueryManagerAgent(ctx context.Context, projectID, conversationID, message string) (*AgentRun, error) {
	values := map[string]any{"message": message}
	if conversationID != "" {
		values["conversation_id"] = conversationID
	}
	resp, err := c.Service(ctx, "execution/manager/query_manager_agent", projectID, values)
	if err != nil {
		return nil, err
	}
	run, err := decode[AgentRun](resp)
	if err != nil {
		return nil, err
	}
	if run.ConversationID == "" || run.RunID == "" {
		return nil, fmt.Errorf("server returned an empty manager conversation or run ID")
	}
	return run, nil
}

func (c *Client) StreamManagerAgent(ctx context.Context, projectID string, run *AgentRun) (*http.Response, error) {
	if err := c.ProjectOrg(ctx, projectID); err != nil {
		return nil, err
	}
	data, err := json.Marshal(map[string]string{"org_id": c.OrgID, "project_id": projectID,
		"conversation_id": run.ConversationID, "run_id": run.RunID})
	if err != nil {
		return nil, err
	}
	return c.request(ctx, http.MethodPost, "/api/v2/execution/manager/stream_manager_agent", bytes.NewReader(data), "application/json", true, "text/event-stream")
}
