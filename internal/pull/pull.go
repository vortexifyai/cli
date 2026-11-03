package pull

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/projectfiles"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
	"vortexify-cli/internal/workspace"
)

type Filter struct {
	Type                string
	Resource            string
	IncludeDependencies bool
	IncludeDependents   bool
}

func (f Filter) expanded() bool { return f.IncludeDependencies || f.IncludeDependents }

func (f Filter) Validate() error {
	if err := (resources.Filter{Type: f.Type, Resource: f.Resource}).Validate(); err != nil {
		return err
	}
	if f.expanded() && strings.TrimSpace(f.Resource) == "" {
		return fmt.Errorf("--include-dependencies and --include-dependents require --resource")
	}
	return nil
}

func (f Filter) Match(t, name, id string) bool {
	return (resources.Filter{Type: f.Type, Resource: f.Resource}).Match(t, name, id)
}

type Service struct {
	Client *api.Client
	Output io.Writer
}

func (s *Service) Run(ctx context.Context, root string, cfg *config.ProjectConfig, filter Filter) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := filter.Validate(); err != nil {
		return err
	}
	lock, err := workspace.Acquire(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = workspace.Recover(root, cfg, s.Output); err != nil {
		return err
	}
	var remote []api.Resource
	if filter.expanded() {
		remote, err = s.Client.ResolvePullResources(ctx, cfg.ProjectID, filter.Type, filter.Resource, filter.IncludeDependencies, filter.IncludeDependents)
	} else {
		remote, err = s.Client.ListResources(ctx, cfg.ProjectID)
	}
	if err != nil {
		return err
	}
	if filter.expanded() {
		roots := 0
		for _, r := range remote {
			if filter.Match(r.Type, r.Name, r.ID) {
				roots++
			}
		}
		if roots != 1 {
			return fmt.Errorf("server must resolve exactly one root resource; use --type or a resource ID to disambiguate (found %d)", roots)
		}
	}
	entries := []resources.RegistryEntry{}
	for _, r := range remote {
		entries = append(entries, resources.RegistryEntry{ID: r.ID, Type: r.Type, Name: r.Name})
	}
	if err = (resources.Filter{Type: filter.Type, Resource: filter.Resource}).CheckSelection(entries); err != nil {
		return err
	}
	if err = resources.ValidateRegistry(entries); err != nil {
		return err
	}
	st, err := state.Load(root)
	if err != nil {
		return err
	}
	for _, row := range remote {
		if row.Type == "sync" {
			state.QualifySync(st, row.ID, row.Name)
		}
	}
	conflicts := []string{}
	for _, r := range remote {
		_, managed := resources.FolderForType(cfg, r.Type)
		if !managed || (!filter.expanded() && !filter.Match(r.Type, r.Name, r.ID)) {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		relative, err := resources.ResourceRelativePath(cfg, r.Type, r.Name)
		if err != nil {
			return err
		}
		target, err := resources.SafePath(root, relative)
		if err != nil {
			return err
		}
		key := state.ResourceKey(r.Type, r.Name)
		localHash := ""
		exists := false
		info, err := os.Lstat(target)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("resource path is not a directory: %s", target)
			}
			exists = true
			localHash, err = resources.HashDirectory(target, r.Type)
			if err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		base, tracked := st.Resources[key]
		if tracked && base.ResourceID != r.ID {
			conflicts = append(conflicts, key)
			continue
		}
		if exists {
			if !r.HasCode {
				if s.Output != nil {
					fmt.Fprintf(s.Output, "Kept local %s (server has no committed code).\n", key)
				}
				continue
			}
			switch DetermineStatus(localHash, base.CodeHash, r.CodeHash) {
			case StatusClean:
				st.Resources[key] = state.ResourceState{ResourceID: r.ID, Version: r.Version, CodeHash: localHash}
				if err = state.Save(root, st); err != nil {
					return err
				}
				continue
			case StatusLocalAhead:
				// Configuration-only versions can advance while source stays at
				// the known baseline. Keep local edits, but refresh the version
				// needed by the next commit's concurrency check.
				st.Resources[key] = state.ResourceState{ResourceID: r.ID, Version: r.Version, CodeHash: r.CodeHash}
				if err = state.Save(root, st); err != nil {
					return err
				}
				if s.Output != nil {
					fmt.Fprintf(s.Output, "Kept local changes in %s.\n", key)
				}
				continue
			case StatusConflict:
				conflicts = append(conflicts, key)
				if s.Output != nil {
					fmt.Fprintf(s.Output, "Conflict: %s; files unchanged.\n", key)
				}
				continue
			}
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		staged, err := os.MkdirTemp(filepath.Dir(target), "."+filepath.Base(r.Name)+".tmp-*")
		if err != nil {
			return err
		}
		err = func() (resultErr error) {
			var previous *state.ResourceState
			if tracked {
				copy := base
				previous = &copy
			}
			transaction, err := workspace.Begin(root, cfg, key, target, staged, exists, localHash, previous)
			if err != nil {
				os.RemoveAll(staged)
				return err
			}
			defer func() {
				if resultErr != nil && transaction.Phase == "downloading" {
					if cleanupErr := workspace.Recover(root, cfg, s.Output); cleanupErr != nil {
						resultErr = fmt.Errorf("%w; download cleanup: %v", resultErr, cleanupErr)
					}
				}
			}()
			body, err := s.Client.PullSource(ctx, cfg.ProjectID, r)
			if err != nil {
				return err
			}
			err = resources.ExtractArchive(body, staged)
			body.Close()
			if err != nil {
				return err
			}
			if r.Type == "connector" {
				if _, err := os.Lstat(filepath.Join(staged, "syncs")); err == nil {
					return fmt.Errorf("connector source archive includes nested syncs; pull sync resources separately")
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			hash, err := resources.HashDirectory(staged, r.Type)
			if err != nil {
				return err
			}
			if r.HasCode && (r.CodeHash == "" || hash != r.CodeHash) {
				return fmt.Errorf("download hash mismatch for %s", key)
			}
			if exists {
				current, err := resources.HashDirectory(target, r.Type)
				if err != nil {
					return err
				}
				if current != localHash {
					return fmt.Errorf("%s changed during download; files unchanged", key)
				}
				if err = resources.PreserveIgnored(target, staged, r.Type); err != nil {
					return err
				}
			} else if _, err = os.Lstat(target); !os.IsNotExist(err) {
				return fmt.Errorf("%s appeared during download; files unchanged", key)
			}
			next := state.ResourceState{ResourceID: r.ID, Version: r.Version, CodeHash: hash}
			if err = workspace.Prepare(root, cfg, transaction, next); err != nil {
				return err
			}
			if err = workspace.Recover(root, cfg, s.Output); err != nil {
				return err
			}
			st.Resources[key] = next
			if s.Output != nil {
				fmt.Fprintf(s.Output, "Pulled %s (version %d).\n", key, r.Version)
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	if filter.Type == "" && filter.Resource == "" {
		if _, err := projectfiles.Sync(ctx, root, cfg, s.Client, false, s.Output); err != nil {
			return err
		}
		if err := projectfiles.WriteInstructions(root); err != nil {
			return err
		}
	}
	if len(conflicts) > 0 {
		return &ConflictError{Resources: conflicts}
	}
	return nil
}
