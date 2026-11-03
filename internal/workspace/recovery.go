package workspace

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
	"vortexify-cli/internal/storage"
)

const journalName = "pull-journal.json"

type Transaction struct {
	Format      int                  `json:"format"`
	ProjectID   string               `json:"project_id"`
	Phase       string               `json:"phase"`
	Key         string               `json:"resource_key"`
	Target      string               `json:"target"`
	Stage       string               `json:"stage"`
	Backup      string               `json:"backup"`
	HadOriginal bool                 `json:"had_original"`
	OldHash     string               `json:"old_hash"`
	OldTreeHash string               `json:"old_tree_hash,omitempty"`
	NewTreeHash string               `json:"new_tree_hash,omitempty"`
	Previous    *state.ResourceState `json:"previous,omitempty"`
	Next        state.ResourceState  `json:"next"`
}

func journalPath(root string) string { return filepath.Join(root, ".vortexify", journalName) }
func Begin(root string, cfg *config.ProjectConfig, key, target, stage string, exists bool, oldHash string, previous *state.ResourceState) (*Transaction, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(journalPath(root)); err == nil {
		return nil, fmt.Errorf("unfinished pull; run vortexify recover")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	targetRel, err := filepath.Rel(root, target)
	if err != nil {
		return nil, err
	}
	stageRel, err := filepath.Rel(root, stage)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(target)
	suffix := strings.TrimPrefix(filepath.Base(stage), "."+name+".tmp-")
	backup := filepath.Join(filepath.Dir(targetRel), "."+name+".backup-"+suffix)
	transaction := &Transaction{Format: 1, ProjectID: cfg.ProjectID, Phase: "downloading", Key: key, Target: filepath.ToSlash(targetRel), Stage: filepath.ToSlash(stageRel), Backup: filepath.ToSlash(backup), HadOriginal: exists, OldHash: oldHash, Previous: previous}
	if _, _, _, err = transaction.paths(root, cfg); err != nil {
		return nil, err
	}
	if err = storage.WriteJSON(journalPath(root), transaction, 0600); err != nil {
		return nil, err
	}
	return transaction, nil
}
func Prepare(root string, cfg *config.ProjectConfig, t *Transaction, next state.ResourceState) error {
	target, stage, _, err := t.paths(root, cfg)
	if err != nil {
		return err
	}
	if t.HadOriginal {
		hash, err := resources.HashDirectory(target, strings.SplitN(t.Key, ":", 2)[0])
		if err != nil {
			return err
		}
		if hash != t.OldHash {
			return fmt.Errorf("resource changed during download; files unchanged")
		}
		originalIgnored, err := treeHashForResource(target, strings.SplitN(t.Key, ":", 2)[0], true)
		if err != nil {
			return err
		}
		stagedIgnored, err := treeHashForResource(stage, strings.SplitN(t.Key, ":", 2)[0], true)
		if err != nil {
			return err
		}
		if originalIgnored != stagedIgnored {
			return fmt.Errorf("ignored local files changed during download; files preserved")
		}
		t.OldTreeHash, err = treeHash(target)
		if err != nil {
			return err
		}
	}
	hash, err := resources.HashDirectory(stage, strings.SplitN(t.Key, ":", 2)[0])
	if err != nil {
		return err
	}
	if hash != next.CodeHash {
		return fmt.Errorf("staged resource changed before replacement")
	}
	if err = syncTree(stage); err != nil {
		return err
	}
	t.NewTreeHash, err = treeHash(stage)
	if err != nil {
		return err
	}
	t.Next = next
	t.Phase = "prepared"
	return storage.WriteJSON(journalPath(root), t, 0600)
}

// Recover requires the workspace lock and never needs the network.
func Recover(root string, cfg *config.ProjectConfig, output ...io.Writer) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	path := journalPath(root)
	if err = storage.CheckFilePath(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var t Transaction
	if err = json.Unmarshal(data, &t); err != nil {
		return fmt.Errorf("invalid recovery journal: %w", err)
	}
	target, stage, backup, err := t.paths(root, cfg)
	if err != nil {
		return err
	}
	targetExists, err := directoryExists(target)
	if err != nil {
		return err
	}
	stageExists, err := directoryExists(stage)
	if err != nil {
		return err
	}
	backupExists, err := directoryExists(backup)
	if err != nil {
		return err
	}
	if t.Phase == "downloading" {
		if backupExists {
			return fmt.Errorf("unexpected backup in incomplete download; preserve files and inspect %s", path)
		}
		if stageExists {
			if err = os.RemoveAll(stage); err != nil {
				return err
			}
		}
		return clearJournal(root)
	}
	if t.Phase == "installed" {
		return finishInstalled(root, &t, target, stage, backup, output...)
	}
	if t.Phase != "prepared" || t.NewTreeHash == "" || t.Next.ResourceID == "" || t.Next.CodeHash == "" {
		return fmt.Errorf("invalid prepared pull journal; inspect %s", path)
	}
	// Verify retained copies before moving or deleting anything, including ignored files.
	if backupExists {
		if !t.HadOriginal {
			return fmt.Errorf("unexpected pull backup")
		}
		if err = verifyTree(backup, t.OldTreeHash); err != nil {
			return err
		}
	}
	if stageExists {
		if err = verifyTree(stage, t.NewTreeHash); err != nil {
			return err
		}
	}
	if targetExists {
		hash, err := treeHash(target)
		if err != nil {
			return err
		}
		if hash == t.NewTreeHash {
			return finish(root, &t, stage, backup, stageExists, backupExists)
		}
		if !t.HadOriginal || hash != t.OldTreeHash || backupExists {
			return fmt.Errorf("resource was edited or replaced after interruption; files preserved; inspect %s", path)
		}
		if !stageExists {
			return rollback(root, &t, stage, backup, false, false)
		}
		if err = os.Rename(target, backup); err != nil {
			return err
		}
		if err = storage.SyncDirectory(filepath.Dir(target)); err != nil {
			return err
		}
		targetExists = false
		backupExists = true
	}
	if !targetExists && stageExists {
		if t.HadOriginal && !backupExists {
			return fmt.Errorf("original pull backup is missing; files preserved; inspect %s", path)
		}
		if err = os.Rename(stage, target); err != nil {
			return err
		}
		if err = storage.SyncDirectory(filepath.Dir(target)); err != nil {
			return err
		}
		return finish(root, &t, stage, backup, false, backupExists)
	}
	if !targetExists && backupExists {
		if err = os.Rename(backup, target); err != nil {
			return err
		}
		if err = storage.SyncDirectory(filepath.Dir(target)); err != nil {
			return err
		}
		return rollback(root, &t, stage, backup, false, false)
	}
	if !t.HadOriginal {
		return rollback(root, &t, stage, backup, false, false)
	}
	return fmt.Errorf("cannot safely recover missing resource copies; inspect %s", path)
}
func finish(root string, t *Transaction, stage, backup string, stageExists, backupExists bool) error {
	st, err := state.Load(root)
	if err != nil {
		return err
	}
	st.Resources[t.Key] = t.Next
	if err = state.Save(root, st); err != nil {
		return err
	}
	t.Phase = "installed"
	if err = storage.WriteJSON(journalPath(root), t, 0600); err != nil {
		return err
	}
	target := filepath.Join(root, filepath.FromSlash(t.Target))
	return finishInstalled(root, t, target, stage, backup)
}
func finishInstalled(root string, t *Transaction, target, stage, backup string, output ...io.Writer) error {
	exists, err := directoryExists(target)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("installed resource is missing; recovery files preserved")
	}
	st, err := state.Load(root)
	if err != nil {
		return err
	}
	if st.Resources[t.Key] != t.Next {
		return fmt.Errorf("installed recovery state was changed; recovery files preserved")
	}
	// Once state was saved, edits to the installed resource remain ordinary local edits.
	for _, copy := range []struct{ path, hash string }{{stage, t.NewTreeHash}, {backup, t.OldTreeHash}} {
		exists, err := directoryExists(copy.path)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err = verifyTree(copy.path, copy.hash); err != nil {
			if len(output) > 0 && output[0] != nil {
				fmt.Fprintf(output[0], "Preserved changed or partially cleaned recovery copy: %s\n", copy.path)
			}
			continue
		}
		if err = os.RemoveAll(copy.path); err != nil {
			return err
		}
	}
	if err = storage.SyncDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	return clearJournal(root)
}
func rollback(root string, t *Transaction, stage, backup string, stageExists, backupExists bool) error {
	st, err := state.Load(root)
	if err != nil {
		return err
	}
	if t.Previous == nil {
		delete(st.Resources, t.Key)
	} else {
		st.Resources[t.Key] = *t.Previous
	}
	if err = state.Save(root, st); err != nil {
		return err
	}
	if stageExists {
		if err = os.RemoveAll(stage); err != nil {
			return err
		}
	}
	if backupExists {
		if err = os.RemoveAll(backup); err != nil {
			return err
		}
	}
	return clearJournal(root)
}
func clearJournal(root string) error {
	if err := os.Remove(journalPath(root)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return storage.SyncDirectory(filepath.Join(root, ".vortexify"))
}
func (t *Transaction) paths(root string, cfg *config.ProjectConfig) (string, string, string, error) {
	if t.Format != 1 || t.ProjectID != cfg.ProjectID {
		return "", "", "", fmt.Errorf("recovery journal belongs to a different project or format")
	}
	resourceType, name, ok := strings.Cut(t.Key, ":")
	if !ok {
		return "", "", "", fmt.Errorf("invalid recovery resource key")
	}
	var relative string
	var err error
	if resourceType == "sync" && !strings.Contains(name, "/") && cfg.Syncs != nil {
		if err := resources.ValidateName(name); err != nil {
			return "", "", "", err
		}
		relative = filepath.Join(cfg.Syncs.Folder, name)
	} else {
		relative, err = resources.ResourceRelativePath(cfg, resourceType, name)
		if err != nil {
			return "", "", "", err
		}
	}
	expected := filepath.ToSlash(relative)
	if t.Target != expected {
		return "", "", "", fmt.Errorf("recovery target does not match project configuration")
	}
	parent := filepath.Dir(expected)
	stage := filepath.FromSlash(t.Stage)
	backup := filepath.FromSlash(t.Backup)
	name = filepath.Base(name)
	prefix := "." + name + ".tmp-"
	suffix := strings.TrimPrefix(filepath.Base(stage), prefix)
	if filepath.Dir(stage) != parent || !strings.HasPrefix(filepath.Base(stage), prefix) || suffix == "" || strings.ContainsAny(suffix, "/\\:") || filepath.Dir(backup) != parent || filepath.Base(backup) != "."+name+".backup-"+suffix {
		return "", "", "", fmt.Errorf("invalid recovery staging paths")
	}
	target, err := resources.SafePath(root, filepath.FromSlash(t.Target))
	if err != nil {
		return "", "", "", err
	}
	staged, err := resources.SafePath(root, stage)
	if err != nil {
		return "", "", "", err
	}
	backed, err := resources.SafePath(root, backup)
	return target, staged, backed, err
}
func directoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("recovery path must be a real directory: %s", path)
	}
	return true, nil
}
func verifyTree(path, expected string) error {
	actual, err := treeHash(path)
	if err != nil {
		return err
	}
	if expected == "" || actual != expected {
		return fmt.Errorf("recovery copy was edited; preserve files and inspect %s", path)
	}
	return nil
}

// treeHash includes ignored files so recovery cannot silently discard edited local secrets.
func treeHash(root string, ignoredOnly ...bool) (string, error) {
	return treeHashForResource(root, "", ignoredOnly...)
}
func treeHashForResource(root, kind string, ignoredOnly ...bool) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if len(ignoredOnly) > 0 && ignoredOnly[0] && !resources.ShouldIgnore(root, path, d.IsDir(), kind) {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in recovery copy: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		binary.Write(h, binary.BigEndian, uint64(len(rel)))
		io.WriteString(h, rel)
		if d.IsDir() {
			h.Write([]byte{0})
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular recovery file: %s", path)
		}
		h.Write([]byte{1})
		binary.Write(h, binary.BigEndian, uint64(info.Size()))
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		n, err := io.Copy(h, file)
		file.Close()
		if err != nil {
			return err
		}
		if n != info.Size() {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func syncTree(root string) error {
	directories := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in staged pull")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		err = file.Sync()
		file.Close()
		return err
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err = storage.SyncDirectory(directories[i]); err != nil {
			return err
		}
	}
	return storage.SyncDirectory(filepath.Dir(root))
}
