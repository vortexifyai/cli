package resources

import (
	"fmt"
	"path/filepath"
	"strings"
	"vortexify-cli/internal/config"
)

func ConfiguredFolders(c *config.ProjectConfig) map[string]string {
	result := map[string]string{}
	for t, f := range map[string]*config.ResourceFolder{"transform": c.Transforms, "api": c.APIs, "agent": c.Agents, "connector": c.Connectors, "app": c.Apps} {
		if f != nil {
			result[t] = f.Folder
		}
	}
	if c.Databases != nil && c.Databases.Enabled {
		result["database"] = c.Databases.Folder
	}
	return result
}
func FolderForType(c *config.ProjectConfig, t string) (string, bool) {
	if t == "sync" {
		if c.Connectors == nil {
			return "", false
		}
		return c.Connectors.Folder, true
	}
	f, ok := ConfiguredFolders(c)[t]
	return f, ok
}

// ResourceRelativePath keeps logical sync identity separate from its directory.
func ResourceRelativePath(c *config.ProjectConfig, kind, name string) (string, error) {
	if (kind == "context" || kind == "knowledge-base") && name == kind {
		return filepath.Join(".vortexify", kind), nil
	}
	if err := ValidateResourceName(kind, name); err != nil {
		return "", err
	}
	folder, ok := FolderForType(c, kind)
	if !ok {
		return "", fmt.Errorf("%s is not managed in this workspace", kind)
	}
	if kind == "sync" {
		owner, base, _ := strings.Cut(name, "/")
		return filepath.Join(folder, owner, "syncs", base), nil
	}
	return filepath.Join(folder, name), nil
}
