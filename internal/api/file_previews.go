package api

import (
	"context"
	"fmt"
)

type FilePreview struct {
	FileID     string `json:"file_id"`
	Name       string `json:"name"`
	Text       string `json:"text"`
	Truncated  bool   `json:"truncated"`
	TotalChars *int   `json:"total_chars"`
}

type FilePreviews struct {
	Files   []FilePreview `json:"files"`
	Omitted int           `json:"omitted"`
}

type FileSearchMatch struct {
	FileID     string  `json:"file_id"`
	Name       string  `json:"name"`
	Text       string  `json:"text"`
	Page       *int    `json:"page,omitempty"`
	Section    string  `json:"section,omitempty"`
	Score      float64 `json:"score"`
	ChunkID    string  `json:"chunk_id,omitempty"`
	Generation string  `json:"generation,omitempty"`
	ScoreKind  string  `json:"score_kind,omitempty"`
}

func (c *Client) SearchResourceFiles(ctx context.Context, projectID, kind, id, query string, limit, version int) ([]FileSearchMatch, error) {
	endpoint, ownerKey := "", ""
	switch kind {
	case "agent":
		endpoint, ownerKey = "artifacts/agents/search_files", "agent_id"
	case "connector":
		endpoint, ownerKey = "data/connectors/search_files", "connector_id"
	default:
		return nil, fmt.Errorf("file owner must be agent or connector")
	}
	values := map[string]any{ownerKey: id, "query": query, "limit": limit}
	if version > 0 {
		values["version"] = version
	}
	resp, err := c.Service(ctx, endpoint, projectID, values)
	if err != nil {
		return nil, err
	}
	result, err := decode[[]FileSearchMatch](resp)
	if err != nil {
		return nil, err
	}
	// Older servers return passage IDs without uploaded filenames.
	names := map[string]string{}
	for i := range *result {
		match := &(*result)[i]
		if match.Name != "" {
			names[match.FileID] = match.Name
		}
	}
	for i := range *result {
		match := &(*result)[i]
		if match.Name != "" {
			continue
		}
		if name, ok := names[match.FileID]; ok {
			match.Name = name
			continue
		}
		preview, err := c.GetFilePreview(ctx, projectID, match.FileID, 1)
		if err != nil {
			return nil, fmt.Errorf("resolve uploaded filename: %w", err)
		}
		match.Name = preview.Name
		names[match.FileID] = preview.Name
	}
	return *result, nil
}

func (c *Client) GetFilePreview(ctx context.Context, projectID, fileID string, chars int) (*FilePreview, error) {
	resp, err := c.Service(ctx, "artifacts/resources/get_file_preview", projectID, map[string]any{"file_id": fileID, "chars": chars})
	if err != nil {
		return nil, err
	}
	return decode[FilePreview](resp)
}

func (c *Client) GetResourceFilePreviews(ctx context.Context, projectID, kind, id string, chars, version int) (*FilePreviews, error) {
	values := map[string]any{"resource_type": kind, "resource_id": id, "chars": chars}
	if version > 0 {
		values["version"] = version
	}
	resp, err := c.Service(ctx, "artifacts/resources/get_resource_file_previews", projectID, values)
	if err != nil {
		return nil, err
	}
	return decode[FilePreviews](resp)
}
