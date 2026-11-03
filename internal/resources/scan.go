package resources

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"vortexify-cli/internal/config"
)

func Scan(root string, cfg *config.ProjectConfig) ([]Resource, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Syncs != nil {
		legacy, err := SafePath(root, cfg.Syncs.Folder)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(legacy)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if len(entries) > 0 {
			return nil, fmt.Errorf("move legacy sync folders from %s to connectors/<connector>/syncs/<sync>, then remove paths.syncs from vortexify.project.yaml; recover unfinished pulls before moving", cfg.Syncs.Folder)
		}
	}
	result := []Resource{}
	for t, folder := range ConfiguredFolders(cfg) {
		path, err := SafePath(root, folder)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if ShouldIgnore(path, filepath.Join(path, entry.Name()), entry.IsDir()) || entry.Name()[0] == '.' {
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				_, err = SafePath(path, entry.Name())
				return nil, err
			}
			if !entry.IsDir() {
				continue
			}
			if err = ValidateName(entry.Name()); err != nil {
				return nil, err
			}
			p := filepath.Join(path, entry.Name())
			hash, err := HashDirectory(p, t)
			if err != nil {
				return nil, err
			}
			if t == "connector" {
				nested, err := scanSyncs(root, cfg, entry.Name())
				if err != nil {
					return nil, err
				}
				result = append(result, nested...)
				files, err := Files(p, "connector")
				if err != nil {
					return nil, err
				}
				if len(files) == 0 {
					continue
				}
			}
			result = append(result, Resource{Type: t, Name: entry.Name(), Path: p, Hash: hash})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type == result[j].Type {
			return result[i].Name < result[j].Name
		}
		return result[i].Type < result[j].Type
	})
	return result, nil
}

func scanSyncs(root string, cfg *config.ProjectConfig, owner string) ([]Resource, error) {
	relative, _ := ResourceRelativePath(cfg, "sync", owner+"/placeholder")
	path, err := SafePath(root, filepath.Dir(relative))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := []Resource{}
	for _, entry := range entries {
		if len(entry.Name()) == 0 || entry.Name()[0] == '.' || ShouldIgnore(path, filepath.Join(path, entry.Name()), entry.IsDir()) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("sync folders cannot be symlinks")
		}
		if !entry.IsDir() {
			continue
		}
		name := owner + "/" + entry.Name()
		if err := ValidateResourceName("sync", name); err != nil {
			return nil, err
		}
		directory, err := SafePath(root, filepath.Join(filepath.Dir(relative), entry.Name()))
		if err != nil {
			return nil, err
		}
		hash, err := HashDirectory(directory, "sync")
		if err != nil {
			return nil, err
		}
		result = append(result, Resource{Type: "sync", Name: name, Path: directory, Hash: hash})
	}
	return result, nil
}
