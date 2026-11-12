// Package projectfiles synchronizes only the two explicitly managed .vortexify collections.
package projectfiles

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
	"vortexify-cli/internal/workspace"
)

var Names = []string{"context", "knowledge-base"}
var RequiredDirectories = map[string][]string{
	"context":        {"requirements", "applications", "architecture"},
	"knowledge-base": {"concepts", "entities"},
}

func ensureDirectories(root, name string) error {
	for _, folder := range RequiredDirectories[name] {
		path, err := resources.SafePath(root, filepath.Join(".vortexify", name, folder))
		if err != nil {
			return err
		}
		if err = os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	return nil
}

type Status struct {
	Collection string `json:"collection"`
	State      string `json:"state"`
	Version    int    `json:"version"`
}

func local(root, name string) (string, bool, error) {
	target, err := resources.SafePath(root, filepath.Join(".vortexify", name))
	if err != nil {
		return "", false, err
	}
	if _, err = os.Stat(target); os.IsNotExist(err) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	hash, err := resources.HashDirectory(target, name)
	return hash, true, err
}
func States(ctx context.Context, root string, cfg *config.ProjectConfig, c *api.Client) ([]Status, error) {
	st, err := state.Load(root)
	if err != nil {
		return nil, err
	}
	result := []Status{}
	for _, name := range Names {
		remote, err := c.Collection(ctx, cfg.ProjectID, name, false)
		if err != nil {
			return nil, err
		}
		hash, exists, err := local(root, name)
		if err != nil {
			return nil, err
		}
		status := "clean"
		base := st.Resources[name+":"+name]
		if !exists {
			status = "not pulled"
		} else if hash != remote.Hash {
			if base.CodeHash == remote.Hash {
				status = "modified"
			} else if base.CodeHash == hash {
				status = "server changed"
			} else {
				status = "conflict"
			}
		}
		result = append(result, Status{name, status, remote.Version})
	}
	return result, nil
}

// Sync runs under the caller's existing workspace lock and recovery journal.
func Sync(ctx context.Context, root string, cfg *config.ProjectConfig, c *api.Client, push bool, out io.Writer, backupLocal ...bool) (int, error) {
	if out == nil {
		out = io.Discard
	}
	count := 0
	for _, name := range Names {
		remote, err := c.Collection(ctx, cfg.ProjectID, name, false)
		if err != nil {
			return count, err
		}
		hash, exists, err := local(root, name)
		if err != nil {
			return count, err
		}
		if remote.Version == 0 && !push {
			return count, fmt.Errorf("initialize project collections with vortexify context init before pulling")
		}
		st, err := state.Load(root)
		if err != nil {
			return count, err
		}
		key := name + ":" + name
		base, tracked := st.Resources[key]
		if exists && hash == remote.Hash {
			// Publish custom path mappings even when documentation bytes are unchanged.
			if push && name == "context" {
				snapshot, err := c.Collection(ctx, cfg.ProjectID, name, true)
				if err != nil {
					return count, err
				}
				if snapshot.Hash != remote.Hash || snapshot.Version != remote.Version {
					return count, fmt.Errorf("collection changed; pull and reconcile first")
				}
				if _, err = c.SaveCollection(ctx, cfg.ProjectID, remote, snapshot.Contents, cfg.ResourcePaths()); err != nil {
					return count, err
				}
			}
			if err = ensureDirectories(root, name); err != nil {
				return count, err
			}
			st.Resources[key] = state.ResourceState{ResourceID: name, Version: remote.Version, CodeHash: hash}
			if err = state.Save(root, st); err != nil {
				return count, err
			}
			continue
		}
		if push {
			if !exists {
				continue
			}
			if !tracked || base.Version != remote.Version || base.CodeHash != remote.Hash {
				return count, fmt.Errorf("%s changed on server or is untracked; pull and reconcile first", name)
			}
			paths, err := resources.Files(filepath.Join(root, ".vortexify", name), name)
			if err != nil {
				return count, err
			}
			files := map[string]string{}
			for _, path := range paths {
				body, err := os.ReadFile(filepath.Join(root, ".vortexify", name, filepath.FromSlash(path)))
				if err != nil {
					return count, err
				}
				if filepath.Ext(path) == ".md" {
					warnSourceLinks(out, name, path, string(body))
				}
				files[path] = base64.StdEncoding.EncodeToString(body)
			}
			// Detect edits made while reading the snapshot.
			current, _, err := local(root, name)
			if err != nil {
				return count, err
			}
			if current != hash {
				return count, fmt.Errorf("%s changed during commit; retry", name)
			}
			saved, err := c.SaveCollection(ctx, cfg.ProjectID, remote, files, cfg.ResourcePaths())
			if err != nil {
				return count, err
			}
			if saved.Hash != hash {
				return count, fmt.Errorf("collection commit hash mismatch")
			}
			st.Resources[key] = state.ResourceState{ResourceID: name, Version: saved.Version, CodeHash: saved.Hash}
			if err = state.Save(root, st); err != nil {
				return count, err
			}
			count++
			fmt.Fprintf(out, "Committed %s (version %d).\n", name, saved.Version)
			continue
		}
		if exists && (!tracked || hash != base.CodeHash) {
			if tracked && remote.Hash == base.CodeHash {
				fmt.Fprintf(out, "Kept local changes in %s.\n", name)
				continue
			}
			if len(backupLocal) == 0 || !backupLocal[0] {
				return count, fmt.Errorf("conflict in %s; files unchanged. Use vortexify context pull --backup-local to save local files and pull the server version", name)
			}
			// Copy first; the existing recovery journal atomically installs the remote snapshot.
			backupRoot, err := resources.SafePath(root, filepath.Join(".vortexify", "backups"))
			if err != nil {
				return count, err
			}
			if err = os.MkdirAll(backupRoot, 0700); err != nil {
				return count, err
			}
			backup, err := os.CreateTemp(backupRoot, name+"-*.tar.gz")
			if err != nil {
				return count, err
			}
			backupHash, archiveErr := resources.CreateArchiveWithHash(filepath.Join(root, ".vortexify", name), backup, name)
			closeErr := backup.Close()
			if archiveErr != nil {
				return count, archiveErr
			}
			if closeErr != nil {
				return count, closeErr
			}
			if backupHash != hash {
				return count, fmt.Errorf("%s changed while backing up; files unchanged", name)
			}
			fmt.Fprintf(out, "Saved local %s to %s. Reapply your edits after pulling, then run vortexify context push.\n", name, backup.Name())
		}
		target, err := resources.SafePath(root, filepath.Join(".vortexify", name))
		if err != nil {
			return count, err
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return count, err
		}
		stage, err := os.MkdirTemp(filepath.Dir(target), "."+name+".tmp-")
		if err != nil {
			return count, err
		}
		var previous *state.ResourceState
		if tracked {
			copy := base
			previous = &copy
		}
		journal, err := workspace.Begin(root, cfg, key, target, stage, exists, hash, previous)
		if err != nil {
			os.RemoveAll(stage)
			return count, err
		}
		downloaded, err := c.Collection(ctx, cfg.ProjectID, name, true)
		if err != nil {
			return count, err
		}
		if downloaded.Version != remote.Version || downloaded.Hash != remote.Hash {
			return count, fmt.Errorf("collection changed during pull; retry after vortexify recover")
		}
		for path, encoded := range downloaded.Contents {
			destination, err := resources.SafePath(stage, path)
			if err != nil {
				return count, err
			}
			body, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return count, err
			}
			if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
				return count, err
			}
			if err = os.WriteFile(destination, body, 0644); err != nil {
				return count, err
			}
		}
		for _, folder := range RequiredDirectories[name] {
			path, err := resources.SafePath(stage, folder)
			if err != nil {
				return count, err
			}
			if err = os.MkdirAll(path, 0755); err != nil {
				return count, err
			}
		}
		stagedHash, err := resources.HashDirectory(stage, name)
		if err != nil {
			return count, err
		}
		if stagedHash != remote.Hash {
			return count, fmt.Errorf("collection download hash mismatch")
		}
		next := state.ResourceState{ResourceID: name, Version: remote.Version, CodeHash: remote.Hash}
		if err = workspace.Prepare(root, cfg, journal, next); err != nil {
			return count, err
		}
		if err = workspace.Recover(root, cfg, out); err != nil {
			return count, err
		}
		count++
		fmt.Fprintf(out, "Pulled %s (version %d).\n", name, remote.Version)
	}
	return count, nil
}
