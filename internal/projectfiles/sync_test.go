package projectfiles

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/workspace"
)

func snapshot(name string, version int, files map[string]string) *api.FileCollection {
	names := []string{}
	for path := range files {
		names = append(names, path)
	}
	sort.Strings(names)
	h := sha256.New()
	encoded := map[string]string{}
	manifest := []api.CollectionFile{}
	for _, path := range names {
		body := []byte(files[path])
		binary.Write(h, binary.BigEndian, uint64(len(path)))
		h.Write([]byte(path))
		binary.Write(h, binary.BigEndian, uint64(len(body)))
		h.Write(body)
		encoded[path] = base64.StdEncoding.EncodeToString(body)
		manifest = append(manifest, api.CollectionFile{Path: path, Size: len(body)})
	}
	return &api.FileCollection{Collection: name, Version: version, Hash: hex.EncodeToString(h.Sum(nil)), Files: manifest, Contents: encoded}
}
func TestRoundtripConflictAndCollectionBoundary(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("project", "demo", "")
	data := map[string]map[string]string{"context": {"index.md": "hello", "images/p.png": string([]byte{0, 255})}, "knowledge-base": {"index.md": "business"}}
	remote := map[string]*api.FileCollection{}
	for name, files := range data {
		remote[name] = snapshot(name, 1, files)
	}
	if remote["context"].Hash != "10c99b5b2678b234d5f8a639ecc2c064105089ba753c540f2e22ad7b19139b63" {
		t.Fatal("Python/Go hash contract differs", remote["context"].Hash)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Collection   string            `json:"collection"`
			BaseVersion  int               `json:"base_version"`
			ExpectedHash string            `json:"expected_hash"`
			Files        map[string]string `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		current := remote[body.Collection]
		if r.URL.Path == "/api/v2/documents/files/save_collection" {
			if body.BaseVersion != current.Version || body.ExpectedHash != current.Hash {
				w.WriteHeader(409)
				return
			}
			next := map[string]string{}
			for path, encoded := range body.Files {
				bytes, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					t.Error(err)
				}
				next[path] = string(bytes)
			}
			data[body.Collection] = next
			current = snapshot(body.Collection, current.Version+1, next)
			remote[body.Collection] = current
		}
		json.NewEncoder(w).Encode(current)
	}))
	defer server.Close()
	client := api.NewClient(server.URL)
	client.OrgID = "org"
	client.Token = func(context.Context) (string, error) { return "token", nil }
	lock, err := workspace.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if count, err := Sync(context.Background(), root, cfg, client, false, io.Discard); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	body, err := os.ReadFile(filepath.Join(root, ".vortexify/context/images/p.png"))
	if err != nil || string(body) != string([]byte{0, 255}) {
		t.Fatal(body, err)
	}
	if err = os.WriteFile(filepath.Join(root, ".vortexify/context/notes.md"), []byte("local"), 0644); err != nil {
		t.Fatal(err)
	}
	states, err := States(context.Background(), root, cfg, client)
	if err != nil || states[0].State != "modified" {
		t.Fatal(states, err)
	}
	if _, err = Sync(context.Background(), root, cfg, client, true, nil); err != nil {
		t.Fatal(err)
	}
	if data["context"]["notes.md"] != "local" {
		t.Fatal("not pushed")
	}
	os.WriteFile(filepath.Join(root, ".vortexify/context/notes.md"), []byte("keep me"), 0644)
	data["context"]["notes.md"] = "remote"
	remote["context"] = snapshot("context", 3, data["context"])
	if _, err = Sync(context.Background(), root, cfg, client, false, io.Discard); err == nil {
		t.Fatal("accepted concurrent edits")
	}
	body, _ = os.ReadFile(filepath.Join(root, ".vortexify/context/notes.md"))
	if string(body) != "keep me" {
		t.Fatal("lost local edit")
	}
	if _, err = Sync(context.Background(), root, cfg, client, true, io.Discard); err == nil {
		t.Fatal("overwrote server edit")
	}
	if _, err = resources.ResourceRelativePath(cfg, "context", "../outside"); err == nil {
		t.Fatal("collection path escaped")
	}
}
