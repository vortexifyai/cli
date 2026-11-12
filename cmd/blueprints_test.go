package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vortexify-cli/internal/config"
)

func blueprintWorkspace(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VORTEXIFY_TOKEN", "token")
	if err := config.Save(root, config.Default("project", "demo", "")); err != nil {
		t.Fatal(err)
	}
}

func TestBlueprintSearchAndAttachmentsUseDirectServices(t *testing.T) {
	blueprintWorkspace(t)
	var endpoints []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoints = append(endpoints, r.URL.Path)
		var values map[string]any
		if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
			t.Error(err)
		}
		if values["project_id"] != "project" || values["org_id"] != "org" {
			t.Error("missing project/org scope")
		}
		if strings.HasSuffix(r.URL.Path, "/search") && (values["query"] != "nurse scheduling in healthcare" || values["limit"] != float64(5) || values["include_answer"] != true) {
			t.Error("missing search options")
		}
		if strings.HasSuffix(r.URL.Path, "/attach") && (values["template_id"] != "shift-planner" || values["attached"] != (len(endpoints) == 4)) {
			t.Error("wrong detach request")
		}
		result := map[string]any{"templates": []map[string]any{{"id": "shift-planner"}}}
		if strings.HasSuffix(r.URL.Path, "/search") {
			result["answer"] = "Use the shift planner for nurse scheduling in healthcare."
		}
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	for _, args := range [][]string{{"list"}, {"search", "nurse scheduling in healthcare", "--limit", "5"}, {"metadata", "shift-planner"}, {"attach", "shift-planner"}, {"detach", "shift-planner"}} {
		out, err := run(t, append([]string{"--api-url", server.URL, "--org-id", "org", "blueprints"}, args...)...)
		if err != nil || !strings.Contains(out, "blueprints") || strings.Contains(out, "templates:") {
			t.Fatal(err)
		}
		if args[0] == "search" && !strings.Contains(out, "Use the shift planner for nurse scheduling in healthcare.") {
			t.Fatalf("generated answer missing from output: %s", out)
		}
	}
	want := []string{"attached", "search", "metadata", "attach", "attach"}
	for i, name := range want {
		if endpoints[i] != "/api/v2/artifacts/templates/"+name {
			t.Fatalf("unexpected endpoint %q", endpoints[i])
		}
	}
}

func TestBlueprintHelpExposesSearchAndAttachedList(t *testing.T) {
	out, err := run(t, "blueprints", "--help")
	if err != nil || !strings.Contains(out, "search") || !strings.Contains(out, "attach") || !strings.Contains(out, "List attached project blueprints") || strings.Contains(out, "catalog ") {
		t.Fatal(out, err)
	}
	root := NewRootCommand()
	blueprints, _, err := root.Find([]string{"blueprints"})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range blueprints.Commands() {
		if command.Name() == "catalog" {
			t.Fatal("entire-catalog listing is exposed")
		}
	}
}

func TestBlueprintCodeDecodesSourceAndPreviewWritesNamedEntrypoint(t *testing.T) {
	blueprintWorkspace(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values map[string]any
		json.NewDecoder(r.Body).Decode(&values)
		path, _ := values["path"].(string)
		if path == "" {
			path = "planner.html"
		}
		content := "<html>Planner</html>"
		if path == "README.md" {
			content = "# Planner"
		}
		json.NewEncoder(w).Encode(templateFile{ID: "shift-planner", Path: path, Entrypoint: "planner.html", Release: "v1", Files: []string{"planner.html", "README.md"}, Content: content, Encoding: "utf-8"})
	}))
	defer server.Close()
	output, err := run(t, "--api-url", server.URL, "--org-id", "org", "blueprints", "code", "shift-planner")
	if err != nil || output != "<html>Planner</html>" {
		t.Fatal(output, err)
	}
	output, err = run(t, "--api-url", server.URL, "--org-id", "org", "blueprints", "preview", "shift-planner")
	if err != nil {
		t.Fatal(err)
	}
	entry := strings.TrimSpace(output)
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(entry)) })
	data, err := os.ReadFile(entry)
	if err != nil || string(data) != "<html>Planner</html>" {
		t.Fatal(string(data), err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(entry), "README.md")); err != nil {
		t.Fatal(err)
	}
}

func TestBlueprintPreviewRejectsEscapingPathsAndMixedReleases(t *testing.T) {
	for _, unsafe := range []string{"../private", "/private", "a/../private", "a\\private", ""} {
		if safeTemplatePath(unsafe) {
			t.Errorf("accepted path %q", unsafe)
		}
	}
	blueprintWorkspace(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values map[string]any
		json.NewDecoder(r.Body).Decode(&values)
		release, path := "v1", "planner.html"
		if values["path"] == "README.md" {
			release, path = "v2", "README.md"
		}
		json.NewEncoder(w).Encode(templateFile{Path: path, Release: release, Files: []string{"planner.html", "README.md"}, Content: "text", Encoding: "utf-8"})
	}))
	defer server.Close()
	if _, err := run(t, "--api-url", server.URL, "--org-id", "org", "blueprints", "preview", "shift-planner"); err == nil || !strings.Contains(err.Error(), "release changed") {
		t.Fatal("mixed releases were accepted", err)
	}
}

func TestBlueprintSearchContinuesConversationWithNaturalLanguageCriteria(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["conversation_id"] != "saved" || body["query"] != "quality for automotive MES" || body["include_answer"] != true {
			t.Error(body)
		}
		w.Write([]byte(`{"conversation_id":"saved","answer":"Use the coverage summary. [S1]","templates":[],"sources":[{"source_id":"S1","kind":"template","id":"hospital-scheduling","part_id":"coverage"}],"recommended_parts":[{"template_id":"hospital-scheduling","part_id":"coverage","title":"Coverage summary","reason":"Shows staffing gaps."}]}`))
	}))
	defer server.Close()
	out, err := run(t, "--api-url", server.URL, "--org-id", "org", "blueprints", "search", "quality for automotive MES", "--conversation", "saved")
	if err != nil {
		t.Fatal(out, err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(out, err)
	}
	if _, found := result["recommended_parts"]; found {
		t.Fatal("JSON output includes frontend part cards", out)
	}
	sources, ok := result["sources"].([]any)
	if result["conversation_id"] != "saved" || result["answer"] != "Use the coverage summary. [S1]" || result["blueprints"] == nil || !ok || len(sources) != 1 {
		t.Fatal("search answer or references were lost", out)
	}
	source, ok := sources[0].(map[string]any)
	if !ok || source["part_id"] != "coverage" || source["id"] != "hospital-scheduling" {
		t.Fatal("part citation was lost", out)
	}
}

func TestBlueprintAttachSendsUsageNoteAndSelectedParts(t *testing.T) {
	blueprintWorkspace(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values map[string]any
		json.NewDecoder(r.Body).Decode(&values)
		if values["usage_note"] != "Use the coverage section for nurse assignments" || values["project_id"] != "project" {
			t.Error(values)
		}
		parts, ok := values["part_ids"].([]any)
		if !ok || len(parts) != 2 || parts[0] != "coverage" || parts[1] != "roster" {
			t.Error(values)
		}
		notes, ok := values["part_usage_notes"].(map[string]any)
		if !ok || notes["coverage"] != "Show coverage=staffing gaps" || notes["roster"] != "" {
			t.Error(values)
		}
		w.Write([]byte(`{"designs":[]}`))
	}))
	defer server.Close()
	_, err := run(t, "--api-url", server.URL, "--org-id", "org", "blueprints", "attach", "hospital-scheduling", "--usage-note", "Use the coverage section for nurse assignments", "--part", "coverage", "--part", "roster", "--part-note", "coverage=Show coverage=staffing gaps", "--part-note", "roster=")
	if err != nil {
		t.Fatal(err)
	}
}

func TestBlueprintPartNoteUpdatePreservesOmittedFields(t *testing.T) {
	blueprintWorkspace(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values map[string]any
		json.NewDecoder(r.Body).Decode(&values)
		if _, exists := values["part_ids"]; exists {
			t.Error("note-only update replaced the selection")
		}
		if _, exists := values["usage_note"]; exists {
			t.Error("note-only update replaced the general note")
		}
		if values["part_usage_notes"].(map[string]any)["coverage"] != "Updated intent" {
			t.Error(values)
		}
		w.Write([]byte(`{"designs":[]}`))
	}))
	defer server.Close()
	_, err := run(t, "--api-url", server.URL, "--org-id", "org", "blueprints", "attach", "hospital-scheduling", "--part-note", "coverage=Updated intent")
	if err != nil {
		t.Fatal(err)
	}
}

func TestBlueprintPartNoteRejectsMalformedInput(t *testing.T) {
	blueprintWorkspace(t)
	for _, entry := range []string{"coverage", "=missing id", "  =missing id"} {
		_, err := run(t, "--org-id", "org", "blueprints", "attach", "hospital-scheduling", "--part-note", entry)
		if err == nil || !strings.Contains(err.Error(), "PART_ID=NOTE") {
			t.Fatalf("accepted malformed note %q: %v", entry, err)
		}
	}
}

func TestBlueprintSearchAcceptsExplicitProjectOutsideWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values map[string]any
		json.NewDecoder(r.Body).Decode(&values)
		if values["project_id"] != "selected-project" || values["query"] != "Inspect the code and attach the coverage part" {
			t.Error(values)
		}
		w.Write([]byte(`{"answer":"Attached the coverage reference.","templates":[]}`))
	}))
	defer server.Close()
	_, err := run(t, "--api-url", server.URL, "--org-id", "org", "blueprints", "search", "Inspect the code and attach the coverage part", "--project-id", "selected-project")
	if err != nil {
		t.Fatal(err)
	}
}
