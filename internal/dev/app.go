package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
)

// FindApp selects only immediate children of the configured app folder.
func FindApp(root string, cfg *config.ProjectConfig, selection, cwd string) (string, error) {
	if cfg.Apps == nil {
		return "", fmt.Errorf("this project does not configure an apps folder")
	}
	folder, err := resources.SafePath(root, cfg.Apps.Folder)
	if err != nil {
		return "", err
	}
	if selection != "" {
		// Accept an app name or an app directory relative to the current directory.
		candidate := filepath.Join(folder, selection)
		if strings.ContainsAny(selection, "/\\") || filepath.IsAbs(selection) {
			candidate = selection
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(cwd, candidate)
			}
		}
		candidate, err = filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(candidate))
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(parent, filepath.Base(candidate))
		if filepath.Dir(candidate) != folder {
			return "", fmt.Errorf("app must be an immediate subdirectory of %s", folder)
		}
		path, err := resources.SafePath(folder, filepath.Base(candidate))
		if err != nil {
			return "", err
		}
		if err = checkApp(path); err != nil {
			return "", err
		}
		return path, nil
	}
	current, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(folder, current); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		name := strings.Split(rel, string(filepath.Separator))[0]
		path, err := resources.SafePath(folder, name)
		if err != nil {
			return "", err
		}
		if err = checkApp(path); err != nil {
			return "", err
		}
		return path, nil
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return "", fmt.Errorf("find apps: %w", err)
	}
	apps := []string{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path, err := resources.SafePath(folder, entry.Name())
		if err != nil {
			return "", err
		}
		if checkApp(path) == nil {
			apps = append(apps, path)
		}
	}
	if len(apps) == 0 {
		return "", fmt.Errorf("no app with a dev script found in %s", folder)
	}
	if len(apps) > 1 {
		return "", fmt.Errorf("multiple apps found; select one with vortexify dev <app>")
	}
	return apps[0], nil
}
func checkApp(path string) error {
	data, err := os.ReadFile(filepath.Join(path, "package.json"))
	if err != nil {
		return fmt.Errorf("app package.json: %w", err)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err = json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("app package.json: %w", err)
	}
	if strings.TrimSpace(pkg.Scripts["dev"]) == "" {
		return fmt.Errorf("app needs a Vite-compatible package.json dev script")
	}
	return nil
}
