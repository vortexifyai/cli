package commit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/config"
	"vortexify-cli/internal/projectfiles"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
	"vortexify-cli/internal/workspace"
)

type Filter = resources.Filter
type Service struct {
	Client *api.Client
	Output io.Writer
}

// PartialError preserves a nonzero exit status when selected changes were skipped.
// Individual reasons have already been printed alongside the commit plan.
type PartialError struct {
	Committed int
	Skipped   map[string]string
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("Committed %s; skipped %s. Resolve the skipped resources and commit again.", resourceCount(e.Committed), resourceCount(len(e.Skipped)))
}

func resourceCount(count int) string {
	if count == 1 {
		return "1 resource"
	}
	return fmt.Sprintf("%d resources", count)
}

func (s *Service) printSkipped(skipped map[string]string) {
	if s.Output == nil || len(skipped) == 0 {
		return
	}
	keys := []string{}
	for key := range skipped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintf(s.Output, "Skipping %s:\n", resourceCount(len(keys)))
	for _, key := range keys {
		fmt.Fprintf(s.Output, "  %s — %s\n", key, skipped[key])
	}
}

func (s *Service) Run(ctx context.Context, root string, cfg *config.ProjectConfig, filter Filter) error {
	if err := cfg.Validate(); err != nil {
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
	if err := filter.Validate(); err != nil {
		return err
	}
	local, err := resources.Scan(root, cfg)
	if err != nil {
		return err
	}
	registry, err := s.Client.GetResourceGraph(ctx, cfg.ProjectID)
	if err != nil {
		return err
	}
	remote := api.CodeResources(registry)
	if err = resources.ValidateRegistry(remoteNames(remote)); err != nil {
		return err
	}
	st, err := state.Load(root)
	if err != nil {
		return err
	}
	for _, row := range registry {
		if row.Type == "sync" {
			state.QualifySync(st, row.ID, row.Name)
		}
	}
	selections := remoteNames(remote)
	for _, row := range local {
		selections = append(selections, resources.RegistryEntry{Type: row.Type, Name: row.Name})
	}
	if err := filter.CheckSelection(selections); err != nil {
		return err
	}
	for _, r := range local {
		for _, server := range remote {
			if server.Type == r.Type && server.Name == r.Name && server.CodeHash == r.Hash && filter.Match(r.Type, r.Name, server.ID) {
				st.Resources[state.ResourceKey(r.Type, r.Name)] = state.ResourceState{ResourceID: server.ID, Version: server.Version, CodeHash: r.Hash}
				if err = state.Save(root, st); err != nil {
					return err
				}
			}
		}
	}
	graph, err := resources.BuildGraph(ctx, local, registry)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	changes := map[string]Change{}
	for _, change := range FindChanges(local, remote) {
		id := ""
		if change.Remote != nil {
			id = change.Remote.ID
		}
		if filter.Match(change.Resource.Type, change.Resource.Name, id) {
			key := resources.Key(change.Resource.Type, change.Resource.Name)
			selected[key] = true
			changes[key] = change
		}
	}
	// Check every baseline before mutation, while leaving unrelated edits eligible.
	blocked := map[string]string{}
	for key, change := range changes {
		base, tracked := st.Resources[key]
		if change.Remote != nil && change.Remote.HasCode && (!tracked || base.ResourceID != change.Remote.ID || base.CodeHash != change.Remote.CodeHash || base.Version != change.Remote.Version) {
			blocked[key] = "has untracked or newer server code; pull and reconcile before committing"
		}
	}
	order, skipped := graph.CommitPlan(selected, blocked)
	if s.Output != nil && len(selected) > 0 {
		fmt.Fprintf(s.Output, "Committing %s.\n", resourceCount(len(order)))
	}
	s.printSkipped(skipped)
	count := 0
	connectorIDs := map[string]string{}
	for _, row := range registry {
		if row.Type == "connector" {
			connectorIDs[row.Name] = row.ID
		}
	}
	for _, key := range order {
		if _, skip := skipped[key]; skip {
			continue
		}
		err = func() error {
			change := changes[key]
			r := change.Resource

			id := ""
			version := 0
			if change.Remote != nil {
				id = change.Remote.ID
				version = change.Remote.Version
			}
			if change.Remote == nil {
				connectorID := ""
				if r.Type == "sync" {
					owner, _, _ := strings.Cut(r.Name, "/")
					connectorID = connectorIDs[owner]
					if connectorID == "" {
						return fmt.Errorf("sync %s requires registered connector %s", r.Name, owner)
					}
				}
				created, err := s.Client.CreateResource(ctx, cfg.ProjectID, r.Type, r.Name, connectorID)
				if err != nil {
					return err
				}
				if created.ID == "" || created.Type != r.Type || created.Name != r.Name {
					return fmt.Errorf("invalid registration response")
				}
				id, version = created.ID, created.Version
				if r.Type == "connector" {
					connectorIDs[r.Name] = id
				}
				st.Resources[key] = state.ResourceState{ResourceID: id, Version: version, CodeHash: created.CodeHash}
				if err = state.Save(root, st); err != nil {
					return err
				}
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			archive, err := os.CreateTemp("", "vortexify-upload-*.tar.gz")
			if err != nil {
				return err
			}
			err = func() error {
				defer os.Remove(archive.Name())
				defer archive.Close()
				hash, err := resources.CreateArchiveWithHash(r.Path, archive, r.Type)
				if err != nil {
					return err
				}
				if hash != r.Hash {
					return fmt.Errorf("%s changed while preparing upload; retry", key)
				}
				if _, err = archive.Seek(0, io.SeekStart); err != nil {
					return err
				}
				info, err := archive.Stat()
				if err != nil {
					return err
				}
				saved, err := s.Client.CommitResource(ctx, cfg.ProjectID, api.CommitResourceRequest{ResourceID: id, Type: r.Type, Name: r.Name, CodeHash: r.Hash, BaseVersion: version, Archive: archive, ArchiveSize: info.Size()})
				if err != nil {
					return err
				}
				if saved.ID == "" || (id != "" && saved.ID != id) || !saved.HasCode || saved.Type != r.Type || saved.Name != r.Name || saved.CodeHash != r.Hash || saved.Version <= version {
					return fmt.Errorf("invalid commit response for %s; pull to reconcile", key)
				}
				st.Resources[key] = state.ResourceState{ResourceID: saved.ID, Version: saved.Version, CodeHash: saved.CodeHash}
				if err = state.Save(root, st); err != nil {
					return err
				}
				if s.Output != nil {
					fmt.Fprintf(s.Output, "Committed %s (version %d).\n", key, saved.Version)
					for _, warning := range saved.Warnings {
						fmt.Fprintf(s.Output, "Warning: %s\n", warning)
					}
					if saved.MigrationStatus == "approval_required" {
						fmt.Fprintf(s.Output, "Migration approval required: vortexify database %s --approve %d --environment %s\n", saved.Name, saved.Version, saved.MigrationEnvironment)
					}
				}
				return nil
			}()
			if err != nil {
				return err
			}
			return nil
		}()
		if err != nil {
			var rejected *api.APIError
			if !errors.As(err, &rejected) || (rejected.StatusCode != 400 && rejected.StatusCode != 409 && rejected.StatusCode != 422) {
				return err
			}
			// Server-side validation can discover a blocker absent from the local
			// graph. Keep independent work, but do not commit its consumers.
			blocked[key] = err.Error()
			_, remaining := graph.CommitPlan(selected, blocked)
			newlySkipped := map[string]string{}
			for name, reason := range remaining {
				if _, exists := skipped[name]; !exists {
					newlySkipped[name] = reason
				}
			}
			skipped = remaining
			s.printSkipped(newlySkipped)
			continue
		}
		count++
	}
	if filter.Type == "" && filter.Resource == "" {
		n, err := projectfiles.Sync(ctx, root, cfg, s.Client, true, s.Output)
		if err != nil {
			return err
		}
		count += n
	}
	if len(skipped) > 0 {
		return &PartialError{Committed: count, Skipped: skipped}
	}
	if count == 0 && s.Output != nil {
		fmt.Fprintln(s.Output, "No changes to commit.")
	}
	return nil
}
func remoteNames(remote []api.Resource) []resources.RegistryEntry {
	out := []resources.RegistryEntry{}
	for _, r := range remote {
		out = append(out, resources.RegistryEntry{ID: r.ID, Type: r.Type, Name: r.Name})
	}
	return out
}
