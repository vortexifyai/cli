package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"vortexify-cli/internal/api"
)

func TestJobWaitUsesStreamAndVerifiesFinalResult(t *testing.T) {
	var finished atomic.Bool
	reads, streams := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/execution/jobs/get_job_results":
			reads++
			json.NewEncoder(w).Encode(api.Execution{"job_id": "job", "status": "running", "completed": finished.Load()})
		case "/api/v2/execution/jobs/stream_job_status":
			streams++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["org_id"] != "org" || body["last_event_id"] != "0-0" {
				t.Error(body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id: 1-0\nevent: progress\ndata: {\"stage\":\"publishing-shell\",\"resource_name\":\"orders_demo\"}\n\n")
			finished.Store(true)
			fmt.Fprint(w, "id: 2-0\nevent: done\ndata: {\"status\":\"success\"}\n\n")
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := api.NewClient(server.URL)
	client.OrgID = "org"
	client.Token = func(context.Context) (string, error) { return "token", nil }
	stages := []string{}
	result, err := waitForJob(context.Background(), client, "project", "job", time.Second, func(row api.Execution) {
		if row["stage"] != nil {
			stages = append(stages, textValue(row["stage"]))
		}
	})
	if err != nil || result["completed"] != true || reads != 2 || streams != 1 || len(stages) != 1 {
		t.Fatal(result, err, reads, streams, stages)
	}
}

func TestJobWaitCancellationClosesStreamWithoutCancellingWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "get_job_results") {
			json.NewEncoder(w).Encode(api.Execution{"completed": false})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "stream_job_status") {
			t.Error("unexpected mutation", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": heartbeat\n\n")
		w.(http.Flusher).Flush()
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := api.NewClient(server.URL)
	client.OrgID = "org"
	client.Token = func(context.Context) (string, error) { return "token", nil }
	_, err := waitForJob(ctx, client, "project", "job", time.Second)
	if err != context.Canceled {
		t.Fatal(err)
	}
}

func TestJobSummaryShowsNamesAndRelevantDetailsInsteadOfManifests(t *testing.T) {
	var out bytes.Buffer
	result := api.Execution{"job_id": "job", "type": "app-deploy", "resource_name": "orders_demo", "status": "success", "completed": true,
		"steps":                    []any{map[string]any{"stage": "building-app", "label": "Build application", "status": "success"}},
		"schema_discovery_job_ids": []any{}, "result": map[string]any{"artifact_prefix": "private/path"}}
	if err := printExecution(&out, result, false); err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"Resource: orders_demo", "✓ Build application"} {
		if !strings.Contains(out.String(), wanted) {
			t.Fatal(out.String())
		}
	}
	for _, unwanted := range []string{"schema_discovery", "artifact_prefix", "private/path"} {
		if strings.Contains(out.String(), unwanted) {
			t.Fatal(out.String())
		}
	}
	out.Reset()
	printExecution(&out, result, true)
	var raw map[string]any
	if json.Unmarshal(out.Bytes(), &raw) != nil || raw["result"] == nil {
		t.Fatal(out.String())
	}
}

func TestSSEResumeIncludesLastEventID(t *testing.T) {
	// A clean disconnect must reconnect instead of treating EOF as completion.
	var finished atomic.Bool
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "get_job_results") {
			json.NewEncoder(w).Encode(api.Execution{"completed": finished.Load()})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		if connections.Add(1) == 1 {
			fmt.Fprint(w, "id: 15-0\nevent: progress\ndata: {\"stage\":\"building-shell\"}\n\n")
			return
		}
		if body["last_event_id"] != "15-0" {
			t.Error(body)
		}
		finished.Store(true)
		fmt.Fprint(w, "id: 16-0\nevent: done\ndata: {\"completed\":true}\n\n")
	}))
	defer server.Close()
	client := api.NewClient(server.URL)
	client.OrgID = "org"
	client.Token = func(context.Context) (string, error) { return "token", nil }
	_, err := waitForJob(context.Background(), client, "project", "job", 5*time.Second)
	if err != nil || connections.Load() != 2 {
		t.Fatal(err, connections.Load())
	}
}

func TestJobWaitFallsBackWhenStreamingIsUnavailable(t *testing.T) {
	var reads, connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "get_job_results") {
			json.NewEncoder(w).Encode(api.Execution{"completed": reads.Add(1) >= 5})
			return
		}
		connections.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := api.NewClient(server.URL)
	client.OrgID = "org"
	client.Token = func(context.Context) (string, error) { return "token", nil }
	result, err := waitForJob(context.Background(), client, "project", "job", 10*time.Second)
	if err != nil || result["completed"] != true || connections.Load() != 3 {
		t.Fatal(result, err, connections.Load())
	}
}

func TestAPIOutputOmitsValidationWhileTransformsKeepIt(t *testing.T) {
	for _, kind := range []string{"function-run", "function-test", "transform-run"} {
		var out bytes.Buffer
		result := api.Execution{"type": kind, "status": "success", "completed": true, "validation": map[string]any{"status": "skipped", "reason": "deployed_api"}}
		if err := printExecution(&out, result, false); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Validation:") != (kind == "transform-run") {
			t.Fatalf("%s: %s", kind, out.String())
		}
	}
}
