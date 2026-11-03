package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTripAndDiscovery(t *testing.T) {
	root := t.TempDir()
	cfg := Default("project-1", "demo", "description")
	if err := Save(root, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded.ProjectID != cfg.ProjectID {
		t.Fatalf("load: %v %#v", err, loaded)
	}
	nested := filepath.Join(root, "transforms", "orders")
	os.MkdirAll(nested, 0755)
	found, err := FindProjectRootFrom(nested)
	if err != nil || found != root {
		t.Fatalf("discovery %q %v", found, err)
	}
}
func TestInvalidFolders(t *testing.T) {
	for _, folder := range []string{"", "..", "../escape", "/absolute", ".", ".vortexify/data", "x\\y"} {
		t.Run(folder, func(t *testing.T) {
			cfg := Default("id", "name", "")
			cfg.Transforms.Folder = folder
			if cfg.Validate() == nil {
				t.Fatal("accepted invalid folder")
			}
		})
	}
	cfg := Default("id", "name", "")
	cfg.APIs.Folder = "transforms/apis"
	if cfg.Validate() == nil {
		t.Fatal("accepted overlap")
	}
}
func TestDiscoveryMissing(t *testing.T) {
	if _, err := FindProjectRootFrom(t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}

func TestUnknownConfigFieldsRejected(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ConfigFileName), []byte(`{"project_id":"id","backend":{"folder":"backend"}}`), 0644)
	if _, err := Load(root); err == nil {
		t.Fatal("silently accepted legacy config")
	}
}

func TestYAMLProjectPaths(t *testing.T) {
	root := t.TempDir()
	data := []byte("project_id: id\npaths:\n  transforms: /source/transforms\n  apps: apps\n  databases: /databases\n")
	os.WriteFile(filepath.Join(root, ConfigFileName), data, 0644)
	cfg, err := Load(root)
	if err != nil || cfg.Transforms.Folder != "source/transforms" || cfg.Apps.Folder != "apps" || cfg.Databases.Folder != "databases" || !cfg.Databases.Enabled || cfg.APIs != nil {
		t.Fatalf("invalid YAML paths: %#v %v", cfg, err)
	}
}

func TestYAMLRejectsInvalidPathsAndDocuments(t *testing.T) {
	for _, data := range []string{
		"project_id: id\npaths:\n  transforms: /../escape\n",
		"project_id: id\npaths:\n  transforms: /\n",
		"project_id: id\npaths:\n  transforms: //absolute\n",
		"project_id: id\npaths:\n  transforms: /.vortexify/data\n",
		"project_id: id\npaths:\n  transforms: /src\n  apps: /src/apps\n",
		"project_id: id\npaths:\n  unknown: /other\n",
		"project_id: id\npaths:\n  apps: /a\n  apps: /b\n",
		"project_id: id\npaths: {}\n---\nproject_id: other\n",
	} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, ConfigFileName), []byte(data), 0644)
		if _, err := Load(root); err == nil {
			t.Fatalf("invalid YAML accepted: %s", data)
		}
	}
}
