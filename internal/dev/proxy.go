package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const ProxyPath = "/__vortexify"

type TokenSource func(context.Context) (string, error)
type tokenKey struct{}

// NewHandler serves Vite and the authenticated API proxy on one browser origin.
func NewHandler(apiURL, viteURL, origin, projectID string, token TokenSource) (http.Handler, error) {
	if strings.TrimSpace(projectID) == "" || strings.ContainsAny(projectID, "/\\\x00") {
		return nil, fmt.Errorf("project ID is required for the development proxy")
	}
	if token == nil {
		return nil, fmt.Errorf("development token source is required")
	}
	upstream, err := parseAPIURL(apiURL)
	if err != nil {
		return nil, err
	}
	// SDK service paths are absolute. Do not prepend APIURL's path twice.
	upstream.Path, upstream.RawPath = "", ""
	vite, err := url.Parse(viteURL)
	if err != nil {
		return nil, err
	}
	local, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	if vite.Scheme != "http" || vite.Hostname() != "127.0.0.1" || local.Scheme != "http" || local.Hostname() != "127.0.0.1" || local.Port() == "" {
		return nil, fmt.Errorf("development servers must use loopback HTTP addresses")
	}
	hosts := map[string]bool{local.Host: true, "localhost:" + local.Port(): true}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	proxyError := func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "Development upstream is unavailable.", http.StatusBadGateway)
	}
	frontend := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, ErrorHandler: proxyError, Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(vite)
		r.Out.Header.Del("Authorization")
		r.Out.Header.Del("Proxy-Authorization")
		r.SetXForwarded()
	}}
	backend := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, ErrorHandler: proxyError, Rewrite: func(r *httputil.ProxyRequest) {
		r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, ProxyPath)
		r.Out.URL.RawPath = strings.TrimPrefix(r.In.URL.EscapedPath(), ProxyPath)
		if r.Out.URL.Path == "" {
			r.Out.URL.Path = "/"
			r.Out.URL.RawPath = ""
		}
		r.SetURL(upstream)
		r.Out.Header.Set("Authorization", "Bearer "+r.In.Context().Value(tokenKey{}).(string))
		r.Out.Header.Del("Cookie")
		r.Out.Header.Del("Proxy-Authorization")
		r.SetXForwarded()
	}}
	var tokenMutex sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[r.Host] {
			http.Error(w, "Unexpected development host.", http.StatusForbidden)
			return
		}
		if requestOrigin := r.Header.Get("Origin"); requestOrigin != "" {
			parsed, err := url.Parse(requestOrigin)
			if err != nil || parsed.Scheme != "http" || !hosts[parsed.Host] || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
				http.Error(w, "Cross-origin development requests are not allowed.", http.StatusForbidden)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "Cross-site development requests are not allowed.", http.StatusForbidden)
			return
		}
		if r.URL.Path == ProxyPath || strings.HasPrefix(r.URL.Path, ProxyPath+"/") {
			if !allowedRoute(r) {
				http.Error(w, "Only SDK service routes are available through this proxy.", http.StatusNotFound)
				return
			}
			if err := validateProject(w, r, projectID); err != nil {
				http.Error(w, "A valid request for the selected project is required.", http.StatusBadRequest)
				return
			}
			// Serializing refresh prevents concurrent requests from rotating the same token twice.
			tokenMutex.Lock()
			accessToken, err := token(r.Context())
			tokenMutex.Unlock()
			if err != nil || strings.TrimSpace(accessToken) == "" {
				http.Error(w, "Development authentication failed. Run vortexify login.", http.StatusUnauthorized)
				return
			}
			backend.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, accessToken)))
			return
		}
		frontend.ServeHTTP(w, r)
	}), nil
}
func parseAPIURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopback)) {
		return nil, fmt.Errorf("API URL must use HTTPS (HTTP allowed on localhost)")
	}
	return u, nil
}

// Keep this list aligned with react-sdk/src/config.ts DEFAULT_ENDPOINTS.
func allowedRoute(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := strings.TrimPrefix(r.URL.EscapedPath(), ProxyPath)
	parts := strings.Split(path, "/")
	if len(parts) >= 7 && strings.Join(parts[:4], "/") == "/api/v2/projects" && parts[4] != "" && parts[5] == "apis" && strings.Join(parts[6:], "/") != "" {
		return true
	}
	switch path {
	case "/api/v2/data/tables/get_analysis",
		"/api/v2/artifacts/resources/list_resources",
		"/api/v2/artifacts/functions/start_transform_function",
		"/api/v2/execution/jobs/get_job",
		"/api/v2/artifacts/agents/run_agent",
		"/api/v2/artifacts/agents/respond_to_tool_call",
		"/api/v2/artifacts/agents/list_conversations",
		"/api/v2/artifacts/agents/get_conversation",
		"/api/v2/artifacts/agents/list_messages",
		"/api/v2/artifacts/agents/create_conversation",
		"/api/v2/artifacts/agents/edit_conversation":
		return true
	}
	return false
}

// Inspect scope before loading credentials, then forward the original bytes.
func validateProject(w http.ResponseWriter, r *http.Request, projectID string) error {
	if strings.HasPrefix(strings.TrimPrefix(r.URL.Path, ProxyPath), "/api/v2/projects/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, ProxyPath), "/")
		if len(parts) < 7 || parts[4] != projectID || parts[5] != "apis" {
			return fmt.Errorf("URL project does not match")
		}
	}
	const maxRequestBytes = 8 << 20
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	var project string
	switch mediaType {
	case "application/json":
		var values map[string]json.RawMessage
		if err := json.Unmarshal(body, &values); err != nil {
			return err
		}
		if err := json.Unmarshal(values["project_id"], &project); err != nil {
			return err
		}
	case "multipart/form-data":
		if params["boundary"] == "" {
			return fmt.Errorf("missing multipart boundary")
		}
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		found := false
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if part.Header.Get("Content-Transfer-Encoding") != "" {
				return fmt.Errorf("unsupported multipart transfer encoding")
			}
			if part.FormName() == "project_id" {
				if found || part.FileName() != "" {
					return fmt.Errorf("ambiguous project")
				}
				value, err := io.ReadAll(part)
				if err != nil {
					return err
				}
				project, found = string(value), true
			}
			if err := part.Close(); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported content type")
	}
	if project != projectID {
		return fmt.Errorf("project does not match")
	}
	if values, ok := r.URL.Query()["project_id"]; ok && (len(values) != 1 || values[0] != projectID) {
		return fmt.Errorf("query project does not match")
	}
	return nil
}
