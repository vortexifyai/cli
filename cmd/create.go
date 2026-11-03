package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
	workspacefs "vortexify-cli/internal/workspace"
)

func newCreateCommand(opts *options) *cobra.Command {
	var connector string
	command := &cobra.Command{Use: "create <type> <name>", Short: "Register a resource and add starter code without replacing local files", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		kind, name := args[0], args[1]
		if err := (resources.Filter{Type: kind}).Validate(); err != nil {
			return err
		}
		if kind != "sync" {
			if err := resources.ValidateName(name); err != nil {
				return err
			}
		}
		root, cfg, client, err := workspace(opts)
		if err != nil {
			return err
		}

		lock, err := workspacefs.Acquire(root)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err := workspacefs.Recover(root, cfg, cmd.OutOrStdout()); err != nil {
			return err
		}
		registry, err := client.GetResourceGraph(cmd.Context(), cfg.ProjectID)
		if err != nil {
			return err
		}
		if kind == "sync" {
			owner, base, qualified := strings.Cut(name, "/")
			if qualified {
				if err := resources.ValidateResourceName(kind, name); err != nil {
					return err
				}
			} else {
				base = name
				owner = connector
				if err := resources.ValidateName(base); err != nil {
					return err
				}
			}
			foundID, foundName := "", ""
			for _, row := range registry {
				if row.Type == "connector" && (row.ID == owner || row.Name == owner) {
					foundID, foundName = row.ID, row.Name
				}
			}
			if foundID == "" {
				return fmt.Errorf("provide connector/sync or --connector identifying a registered connector")
			}
			if qualified && connector != "" && connector != foundID && connector != foundName {
				return fmt.Errorf("--connector does not match the qualified sync name")
			}
			connector = foundID
			name = foundName + "/" + base
		}
		relative, err := resources.ResourceRelativePath(cfg, kind, name)
		if err != nil {
			return err
		}
		target, err := resources.SafePath(root, relative)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(target); err == nil && !info.IsDir() {
			return fmt.Errorf("resource path is not a directory")
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, row := range registry {
			if row.Type == kind && row.Name == name {
				return fmt.Errorf("%s/%s is already registered; use pull to retrieve it", kind, name)
			}
		}

		row, err := client.CreateResource(cmd.Context(), cfg.ProjectID, kind, name, connector)
		if err != nil {
			return err
		}
		if row.ID == "" || row.Type != kind || row.Name != name {
			return fmt.Errorf("invalid registration response; check status before retrying")
		}
		st, err := state.Load(root)
		if err != nil {
			return err
		}
		for _, previous := range registry {
			if previous.Type == "sync" {
				state.QualifySync(st, previous.ID, previous.Name)
			}
		}
		// Save the server baseline, never the hash of an existing local implementation.
		st.Resources[state.ResourceKey(kind, name)] = state.ResourceState{ResourceID: row.ID, Version: row.Version, CodeHash: row.CodeHash}
		if err = state.Save(root, st); err != nil {
			return err
		}
		if _, err = os.Lstat(target); os.IsNotExist(err) {
			if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			staged, err := os.MkdirTemp(filepath.Dir(target), "."+filepath.Base(name)+".create-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(staged)
			source, err := client.PullSource(cmd.Context(), cfg.ProjectID, *row)
			if err != nil {
				return err
			}
			err = resources.ExtractArchive(source, staged)
			source.Close()
			if err != nil {
				return err
			}
			hash, err := resources.HashDirectory(staged, kind)
			if err != nil {
				return err
			}
			if hash != row.CodeHash {
				return fmt.Errorf("starter source hash mismatch")
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				return fmt.Errorf("resource path appeared during creation; preserved it")
			}
			if err = os.Rename(staged, target); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created %s/%s (%s).\n", kind, name, row.ID)
		return nil
	}}
	command.Flags().StringVar(&connector, "connector", "", "Registered connector name or ID for a sync")
	return command
}
