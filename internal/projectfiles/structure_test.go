package projectfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredEmptyDirectoriesAndFileCollision(t *testing.T) {
	root := t.TempDir()
	for name, folders := range RequiredDirectories {
		if err := ensureDirectories(root, name); err != nil {
			t.Fatal(err)
		}
		for _, folder := range folders {
			path := filepath.Join(root, ".vortexify", name, folder)
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				t.Fatalf("missing directory %s: %v", path, err)
			}
		}
	}
	path := filepath.Join(root, ".vortexify", "context", "architecture")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("file collision"), 0644); err != nil {
		t.Fatal(err)
	}
	if ensureDirectories(root, "context") == nil {
		t.Fatal("accepted a file in place of a required directory")
	}
}

func TestManagedGuidanceRefreshPreservesUserInstructions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	original := "User instructions before\n<!-- vortexify-project-context -->\nOld guidance\n<!-- /vortexify-project-context -->\nUser instructions after\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteInstructions(root); err != nil {
		t.Fatal(err)
	}
	once, _ := os.ReadFile(path)
	if !strings.Contains(string(once), "User instructions before") || !strings.Contains(string(once), "User instructions after") || strings.Contains(string(once), "Old guidance") {
		t.Fatal(string(once))
	}
	if err := WriteInstructions(root); err != nil {
		t.Fatal(err)
	}
	twice, _ := os.ReadFile(path)
	if string(once) != string(twice) {
		t.Fatal("guidance update is not idempotent")
	}
}
