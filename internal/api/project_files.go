package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type CollectionFile struct {
	Path        string `json:"path"`
	Size        int    `json:"size"`
	Hash        string `json:"hash"`
	ContentType string `json:"content_type"`
}
type FileCollection struct {
	Collection  string            `json:"collection"`
	Version     int               `json:"version"`
	Hash        string            `json:"hash"`
	Directories []string          `json:"directories"`
	Files       []CollectionFile  `json:"files"`
	Contents    map[string]string `json:"contents,omitempty"`
}

func (c *Client) ProjectOrg(ctx context.Context, project string) error {
	if c.OrgID != "" {
		return nil
	}
	p, err := c.GetProject(ctx, project)
	if err != nil {
		return err
	}
	if p.ID != project || strings.TrimSpace(p.OrgID) == "" {
		return fmt.Errorf("project lookup returned missing or inconsistent organization context")
	}
	c.OrgID = p.OrgID
	return nil
}
func (c *Client) Collection(ctx context.Context, project, name string, content bool) (*FileCollection, error) {
	if err := c.ProjectOrg(ctx, project); err != nil {
		return nil, err
	}
	resp, err := c.Service(ctx, "documents/files/get_collection", project, map[string]any{"collection": name, "include_content": content})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result FileCollection
	err = json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&result)
	return &result, err
}
func (c *Client) InitializeCollection(ctx context.Context, project, name string) error {
	if err := c.ProjectOrg(ctx, project); err != nil {
		return err
	}
	resp, err := c.Service(ctx, "documents/files/initialize", project, map[string]any{"collection": name})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
func (c *Client) SaveCollection(ctx context.Context, project string, previous *FileCollection, files map[string]string, paths ...map[string]string) (*FileCollection, error) {
	payload := map[string]any{"collection": previous.Collection, "base_version": previous.Version, "expected_hash": previous.Hash, "files": files}
	if len(paths) > 0 {
		payload["project_paths"] = paths[0]
	}
	resp, err := c.Service(ctx, "documents/files/save_collection", project, payload)
	if err != nil {
		return nil, err
	}
	return decode[FileCollection](resp)
}
