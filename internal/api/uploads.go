package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"vortexify-cli/internal/resources"
)

// CommitUpload is an expiring, server-owned staging upload, not a published version.
type CommitUpload struct {
	UploadID  string            `json:"upload_id"`
	UploadURL string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
}

func (c *Client) CommitResource(ctx context.Context, projectID string, request CommitResourceRequest) (*Resource, error) {
	if request.Archive == nil || request.ArchiveSize <= 0 {
		return nil, fmt.Errorf("commit requires an archive with a known positive size")
	}
	if request.ResourceID == "" {
		return nil, fmt.Errorf("resource must be registered before uploading source")
	}
	resp, err := c.Service(ctx, "execution/code/get_commit_s3_url", projectID, map[string]any{
		"resource_type": sourceKind(request.Type), "resource_id": request.ResourceID,
		"base_version": request.BaseVersion, "expected_hash": request.CodeHash})
	if err != nil {
		return nil, fmt.Errorf("prepare commit: %w", err)
	}
	upload, err := decode[CommitUpload](resp)
	if err != nil {
		return nil, fmt.Errorf("prepare commit: %w", err)
	}
	if strings.TrimSpace(upload.UploadID) == "" {
		return nil, fmt.Errorf("prepare commit returned an empty upload ID")
	}
	if err = c.uploadArchive(ctx, upload, request.Archive, request.ArchiveSize); err != nil {
		return nil, err
	}
	return c.finalizeCommit(ctx, projectID, upload.UploadID, request)
}

// A failed finalize is reconciled through status/pull before uploading again.
func (c *Client) finalizeCommit(ctx context.Context, projectID, uploadID string, request CommitResourceRequest) (*Resource, error) {
	resp, err := c.Service(ctx, "execution/code/commit_resource", projectID, map[string]any{
		"resource_type": sourceKind(request.Type), "resource_id": request.ResourceID, "upload_id": uploadID, "base_version": request.BaseVersion})
	if err != nil {
		return nil, fmt.Errorf("finalize commit: %w", err)
	}
	saved, err := decode[Resource](resp)
	if err == nil && request.Type == "sync" && saved.Name == resources.SyncBaseName(request.Name) {
		saved.Name = request.Name
	}
	return saved, err
}

func (c *Client) uploadArchive(ctx context.Context, upload *CommitUpload, archive io.Reader, size int64) error {
	return c.uploadSigned(ctx, upload.UploadURL, upload.Headers, archive, size, "application/gzip")
}

func (c *Client) uploadSigned(ctx context.Context, signedURL string, headers map[string]string, data io.Reader, size int64, contentType string) error {
	u, err := url.Parse(signedURL)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return fmt.Errorf("signed upload URL must use HTTPS (HTTP allowed on localhost)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signedURL, io.LimitReader(data, size))
	if err != nil {
		return fmt.Errorf("invalid signed upload request")
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	seen := map[string]bool{}
	for key, value := range headers {
		lower := strings.ToLower(key)
		if seen[lower] || (lower != "content-type" && lower != "content-md5" && !strings.HasPrefix(lower, "x-amz-")) {
			return fmt.Errorf("unsupported or duplicate signed upload header")
		}
		seen[lower] = true
		req.Header.Set(key, value)
	}
	// Use the streaming transport, without API auth, cookie jar, or redirect replay.
	client := &http.Client{Transport: c.StreamClient.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Transport errors can include the signed URL; never print its credentials.
		return fmt.Errorf("signed upload failed; processing was not started")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("signed upload failed (HTTP %d); processing was not started", resp.StatusCode)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
	if err != nil {
		return fmt.Errorf("signed upload response failed; processing was not started")
	}
	return nil
}

// UploadData streams a local file to the signed staging URL, then starts ingestion.
// The raw ingestion job contains internal configuration; only return its public IDs.
func (c *Client) UploadData(ctx context.Context, projectID, filename string, data io.Reader, size int64, kind, name, resourceType, resourceID string) (Execution, error) {
	if data == nil || size <= 0 {
		return nil, fmt.Errorf("upload requires a nonempty file with a known size")
	}
	endpoint := "data/uploads/upload_table"
	values := map[string]any{}
	switch kind {
	case "table":
		if resourceType != "" || resourceID != "" {
			return nil, fmt.Errorf("table uploads do not take an attachment owner")
		}
	case "file":
		if (resourceType != "connector" && resourceType != "agent") || resourceID == "" {
			return nil, fmt.Errorf("file uploads require a connector or agent")
		}
		endpoint = "data/uploads/upload_resource_file"
		values["resource_type"], values["resource_id"] = resourceType, resourceID
	default:
		return nil, fmt.Errorf("unsupported upload kind")
	}
	const contentType = "application/octet-stream"
	response, err := c.Service(ctx, "data/uploads/get_upload_s3_url", projectID, map[string]any{"filename": filename, "content_type": contentType})
	if err != nil {
		return nil, fmt.Errorf("prepare upload: %w", err)
	}
	upload, err := decode[struct {
		ID  string `json:"upload_id"`
		URL string `json:"upload_url"`
	}](response)
	if err != nil {
		return nil, fmt.Errorf("prepare upload: %w", err)
	}
	if strings.TrimSpace(upload.ID) == "" {
		return nil, fmt.Errorf("prepare upload returned an empty upload ID")
	}
	if err = c.uploadSigned(ctx, upload.URL, nil, data, size, contentType); err != nil {
		return nil, err
	}
	values["upload_id"] = upload.ID
	if name != "" {
		values["name"] = name
	}
	response, err = c.Service(ctx, endpoint, projectID, values)
	if err != nil {
		return nil, fmt.Errorf("upload %s was stored but ingestion submission failed: %w", upload.ID, err)
	}
	job, err := decode[struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Name   string `json:"resource_name"`
		Stage  string `json:"stage"`
	}](response)
	if err != nil || job.ID == "" {
		return nil, fmt.Errorf("upload %s was stored, but the ingestion response did not contain a job ID; check server status before uploading again", upload.ID)
	}
	label := job.Name
	if label == "" {
		label = name
	}
	if label == "" {
		label = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	result := Execution{"upload_id": upload.ID, "job_id": job.ID, "status": job.Status, "type": "upload", "resource_name": label}
	if job.Stage != "" {
		result["stage"] = job.Stage
	}
	return result, nil
}
