package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignedCommitStages(t *testing.T) {
	for _, mode := range []string{"success", "prepare-failure", "upload-failure", "transport-failure", "redirect", "bad-header", "insecure-url", "missing-id", "finalize-conflict", "invalid-finalize-json"} {
		t.Run(mode, func(t *testing.T) {
			uploads, finalizes, tokens := 0, 0, 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed upload redirect") }))
			defer destination.Close()
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				uploads++
				if r.Method != "PUT" || r.ContentLength != 7 || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("bad storage request")
				}
				if r.URL.RawQuery != "X-Amz-Signature=private" || r.Header.Get("Content-Type") != "application/gzip" || r.Header.Get("X-Amz-Meta-Upload") != "id" {
					t.Error("signed URL/header changed")
				}
				if mode == "redirect" {
					http.Redirect(w, r, destination.URL, 307)
					return
				}
				if mode == "upload-failure" {
					w.WriteHeader(403)
					w.Write([]byte("X-Amz-Signature=private"))
					return
				}
				w.WriteHeader(200)
			}))
			defer storage.Close()
			if mode == "transport-failure" {
				storage.Close()
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer cli-token" || r.Method != "POST" {
					t.Error("missing API auth")
				}
				switch r.URL.Path {
				case "/api/v2/execution/code/get_commit_s3_url":
					if mode == "prepare-failure" {
						w.WriteHeader(403)
						return
					}
					var metadata map[string]any
					if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil || metadata["expected_hash"] != "hash" || metadata["resource_id"] != "resource" || metadata["resource_type"] != "function" || metadata["org_id"] != "org" {
						t.Error("invalid preparation metadata")
					}
					upload := CommitUpload{UploadID: "id", UploadURL: storage.URL + "?X-Amz-Signature=private", Headers: map[string]string{"Content-Type": "application/gzip", "x-amz-meta-upload": "id"}}
					if mode == "bad-header" {
						upload.Headers["Authorization"] = "Bearer secret"
					}
					if mode == "insecure-url" {
						upload.UploadURL = "http://example.com/?X-Amz-Signature=private"
					}
					if mode == "missing-id" {
						upload.UploadID = ""
					}
					json.NewEncoder(w).Encode(upload)
				case "/api/v2/execution/code/commit_resource":
					finalizes++
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if body["upload_id"] != "id" || uploads != 1 {
						t.Error("finalization before upload")
					}
					if mode == "finalize-conflict" {
						w.WriteHeader(409)
						return
					}
					if mode == "invalid-finalize-json" {
						w.Write([]byte("invalid"))
						return
					}
					json.NewEncoder(w).Encode(Resource{ID: "resource", Type: "transform", Name: "orders", Version: 1, CodeHash: "hash", HasCode: true})
				default:
					t.Error("unexpected route", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c := NewClient(server.URL)
			c.OrgID = "org"
			c.Token = func(context.Context) (string, error) { tokens++; return "cli-token", nil }
			saved, err := c.CommitResource(context.Background(), "p", CommitResourceRequest{ResourceID: "resource", Type: "transform", Name: "orders", CodeHash: "hash", Archive: strings.NewReader("archive"), ArchiveSize: 7})
			if mode == "success" {
				if err != nil || saved.ID != "resource" {
					t.Fatal(saved, err)
				}
			} else {
				if err == nil {
					t.Fatal("failure ignored")
				}
				if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "Signature") {
					t.Fatal("leaked signed URL", err)
				}
			}
			expectedFinal := 0
			if mode == "success" || mode == "finalize-conflict" || mode == "invalid-finalize-json" {
				expectedFinal = 1
			}
			if finalizes != expectedFinal || tokens != 1+expectedFinal {
				t.Fatalf("wrong stage progression: finalizes=%d token lookups=%d", finalizes, tokens)
			}
		})
	}
}
