package config

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"strings"

	"vortexify-cli/internal/storage"
)

const ConfigFileName = "vortexify.project.yaml"

type ResourceFolder struct {
	Folder string `json:"folder"`
}
type KnowledgeGraphConfig struct {
	Enabled bool `json:"enabled"`
}
type FileStorageConfig struct {
	Enabled                       bool `json:"enabled"`
	StorageGB                     int  `json:"storage_gb"`
	DevelopmentEnvironmentEnabled bool `json:"development_environment_enabled"`
}
type DatabaseConfig struct {
	Enabled                       bool   `json:"enabled"`
	StorageGB                     int    `json:"storage_gb"`
	BackupDatabaseVersions        bool   `json:"backup_database_versions"`
	MigrationApprovalRequired     bool   `json:"migration_approval_required"`
	DevelopmentEnvironmentEnabled bool   `json:"development_environment_enabled"`
	Folder                        string `json:"folder"`
}
type ProjectConfig struct {
	ProjectID      string               `json:"project_id"`
	Name           string               `json:"name"`
	Description    string               `json:"description"`
	KnowledgeGraph KnowledgeGraphConfig `json:"knowledge_graph"`
	FileStorage    FileStorageConfig    `json:"file_storage"`
	Connectors     *ResourceFolder      `json:"connectors,omitempty"`
	Syncs          *ResourceFolder      `json:"syncs,omitempty"`
	Transforms     *ResourceFolder      `json:"transforms,omitempty"`
	APIs           *ResourceFolder      `json:"apis,omitempty"`
	Agents         *ResourceFolder      `json:"agents,omitempty"`
	Databases      *DatabaseConfig      `json:"databases,omitempty"`
	Apps           *ResourceFolder      `json:"apps,omitempty"`
}

func Default(id, name, description string) *ProjectConfig {
	return &ProjectConfig{ProjectID: id, Name: name, Description: description,
		Connectors: &ResourceFolder{"connectors"},
		Transforms: &ResourceFolder{"transforms"}, APIs: &ResourceFolder{"apis"},
		Agents: &ResourceFolder{"agents"}, Apps: &ResourceFolder{"apps"},
		Databases: &DatabaseConfig{Enabled: true, StorageGB: 1, BackupDatabaseVersions: true, MigrationApprovalRequired: true, Folder: "databases"}}
}
func (c *ProjectConfig) Validate() error {
	if strings.TrimSpace(c.ProjectID) == "" {
		return fmt.Errorf("project_id is required")
	}
	folders := []string{}
	for _, f := range []*ResourceFolder{c.Connectors, c.Syncs, c.Transforms, c.APIs, c.Agents, c.Apps} {
		if f != nil {
			folders = append(folders, f.Folder)
		}
	}
	if c.Databases != nil {
		folders = append(folders, c.Databases.Folder)
	}
	for i, folder := range folders {
		clean := filepath.Clean(folder)
		if folder == "" || filepath.IsAbs(folder) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(folder, "\\") {
			return fmt.Errorf("invalid resource folder %q", folder)
		}
		for _, component := range strings.Split(filepath.ToSlash(clean), "/") {
			if component == ".git" || component == ".vortexify" {
				return fmt.Errorf("reserved resource folder %q", folder)
			}
		}
		for _, other := range folders[:i] {
			other = filepath.Clean(other)
			if clean == other || strings.HasPrefix(clean, other+string(filepath.Separator)) || strings.HasPrefix(other, clean+string(filepath.Separator)) {
				return fmt.Errorf("resource folders overlap: %q and %q", folder, other)
			}
		}
	}
	return nil
}

// ProjectFile is the minimal on-disk workspace contract. Leading slashes in
// paths mean project-root-relative paths, never OS-root absolute paths.
type ProjectFile struct {
	ProjectID string            `yaml:"project_id"`
	Paths     map[string]string `yaml:"paths"`
}

func Load(root string) (*ProjectConfig, error) {
	path := filepath.Join(root, ConfigFileName)
	if err := storage.CheckFilePath(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file ProjectFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err = decoder.Decode(&file); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("configuration must contain exactly one YAML document")
	}
	cfg := &ProjectConfig{ProjectID: file.ProjectID}
	for key, path := range file.Paths {
		if strings.HasPrefix(path, "//") {
			return nil, fmt.Errorf("invalid project path %q", path)
		}
		folder := strings.TrimPrefix(path, "/")
		switch key {
		case "connectors":
			cfg.Connectors = &ResourceFolder{Folder: folder}
		case "syncs":
			cfg.Syncs = &ResourceFolder{Folder: folder} // Legacy location, used only for move/recovery checks.
		case "transforms":
			cfg.Transforms = &ResourceFolder{Folder: folder}
		case "apis":
			cfg.APIs = &ResourceFolder{Folder: folder}
		case "agents":
			cfg.Agents = &ResourceFolder{Folder: folder}
		case "apps":
			cfg.Apps = &ResourceFolder{Folder: folder}
		case "databases":
			cfg.Databases = &DatabaseConfig{Enabled: true, Folder: folder}
		default:
			return nil, fmt.Errorf("unknown resource path %q", key)
		}
	}
	if err = cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
func Save(root string, cfg *ProjectConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	file := ProjectFile{ProjectID: cfg.ProjectID, Paths: cfg.ResourcePaths()}
	data, err := yaml.Marshal(file)
	if err != nil {
		return err
	}
	return storage.WriteFile(filepath.Join(root, ConfigFileName), data, 0644)
}

// ResourcePaths returns the configured project-root-relative resource locations.
func (cfg *ProjectConfig) ResourcePaths() map[string]string {
	paths := map[string]string{}
	for key, folder := range map[string]*ResourceFolder{"connectors": cfg.Connectors, "transforms": cfg.Transforms, "apis": cfg.APIs, "agents": cfg.Agents, "apps": cfg.Apps} {
		if folder != nil {
			paths[key] = "/" + filepath.ToSlash(filepath.Clean(folder.Folder))
		}
	}
	if cfg.Databases != nil && cfg.Databases.Enabled {
		paths["databases"] = "/" + filepath.ToSlash(filepath.Clean(cfg.Databases.Folder))
	}
	return paths
}
