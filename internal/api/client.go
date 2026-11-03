package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vortexify-cli/internal/auth"
)

type APIError struct {
	StatusCode int
	Message    string
	Details    map[string]any
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (HTTP %d): %s", e.StatusCode, e.Message)
}

type Client struct {
	OrgID        string
	BaseURL      string
	HTTPClient   *http.Client
	StreamClient *http.Client
	Token        func(context.Context) (string, error)
}

func NewClient(baseURL string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	redirect := func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		previous := via[0].URL
		if previous.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing insecure redirect")
		}
		if req.URL.Scheme != previous.Scheme || req.URL.Host != previous.Host {
			req.Header.Del("Authorization")
		}
		return nil
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: &http.Client{Timeout: 30 * time.Second, Transport: transport, CheckRedirect: redirect}, StreamClient: &http.Client{Transport: transport, CheckRedirect: redirect}, Token: auth.GetAccessTokenContext}
}
func (c *Client) Request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	return c.request(ctx, method, path, r, "application/json", false)
}
func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType string, stream bool, accept ...string) (*http.Response, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return nil, fmt.Errorf("API URL must use HTTPS (HTTP allowed on localhost)")
	}
	base := c.BaseURL
	if strings.HasPrefix(path, "/api/v2/") {
		base = strings.TrimSuffix(base, "/api/v2")
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	if stream {
		req.Header.Set("Accept", "*/*")
		if len(accept) > 0 {
			req.Header.Set("Accept", accept[0])
		}
	} else {
		req.Header.Set("Accept", "application/json")
	}
	client := c.HTTPClient
	if stream {
		client = c.StreamClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		message := strings.TrimSpace(string(data))
		var body struct {
			Message string         `json:"message"`
			Details map[string]any `json:"error"`
		}
		if json.Unmarshal(data, &body) == nil && body.Message != "" {
			message = body.Message
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: message, Details: body.Details}
	}
	return resp, nil
}
func decode[T any](resp *http.Response) (*T, error) {
	defer resp.Body.Close()
	var value T
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&value); err != nil {
		return nil, err
	}
	return &value, nil
}

// Service uses the current server contract; organization membership is checked there.
func (c *Client) Service(ctx context.Context, endpoint, projectID string, values map[string]any) (*http.Response, error) {
	if c.OrgID == "" && projectID != "" {
		if err := c.ProjectOrg(ctx, projectID); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(c.OrgID) == "" {
		return nil, fmt.Errorf("organization context required: select an organization or use --org-id")
	}
	values["org_id"] = c.OrgID
	if projectID != "" {
		values["project_id"] = projectID
	}
	return c.Request(ctx, http.MethodPost, "/api/v2/"+endpoint, values)
}
