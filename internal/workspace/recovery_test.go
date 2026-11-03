package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vortexify-cli/internal/config"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
	"vortexify-cli/internal/storage"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestExclusiveLockAndRelease(t *testing.T) {
	root := t.TempDir()
	a, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(root)
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatal("second lock succeeded", err)
	}
	a.Close()
	b, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if _, err = os.Stat(filepath.Join(root, ".vortexify", "workspace.lock")); err != nil {
		t.Fatal("lock inode removed")
	}
}
func TestLockProcessHelper(t *testing.T) {
	root := os.Getenv("VORTEXIFY_TEST_LOCK_ROOT")
	if root == "" {
		return
	}
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	fmt.Println("locked")
	for {
		time.Sleep(time.Second)
	}
}
func TestLockReleasedAfterKilledProcess(t *testing.T) {
	root := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestLockProcessHelper$")
	command.Env = append(os.Environ(), "VORTEXIFY_TEST_LOCK_ROOT="+root)
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || !strings.Contains(line, "locked") {
		t.Fatal("helper did not lock", line, err)
	}
	if _, err = Acquire(root); err == nil {
		t.Fatal("process lock not exclusive")
	}
	command.Process.Kill()
	command.Wait()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal("crash left stale lock", err)
	}
	lock.Close()
}
func fixture(t *testing.T, original bool) (string, *config.ProjectConfig, *Transaction) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cfg := &config.ProjectConfig{ProjectID: "project", Transforms: &config.ResourceFolder{Folder: "transforms"}}
	target := filepath.Join(root, "transforms", "orders")
	stage := filepath.Join(root, "transforms", ".orders.tmp-test")
	oldHash := ""
	var previous *state.ResourceState
	if original {
		put(t, filepath.Join(target, "main.py"), "old")
		put(t, filepath.Join(target, ".env"), "secret")
		oldHash, _ = resources.HashDirectory(target)
		previous = &state.ResourceState{ResourceID: "r", Version: 1, CodeHash: oldHash}
		state.Save(root, &state.State{Resources: map[string]state.ResourceState{"transform:orders": *previous}})
	}
	put(t, filepath.Join(stage, "main.py"), "new")
	if original {
		put(t, filepath.Join(stage, ".env"), "secret")
	}
	tx, err := Begin(root, cfg, "transform:orders", target, stage, original, oldHash, previous)
	if err != nil {
		t.Fatal(err)
	}
	return root, cfg, tx
}
func prepare(t *testing.T, root string, cfg *config.ProjectConfig, tx *Transaction) {
	t.Helper()
	hash, err := resources.HashDirectory(filepath.Join(root, tx.Stage))
	if err != nil {
		t.Fatal(err)
	}
	if err = Prepare(root, cfg, tx, state.ResourceState{ResourceID: "r", Version: 2, CodeHash: hash}); err != nil {
		t.Fatal(err)
	}
}
func TestInterruptedPullPhases(t *testing.T) {
	for _, phase := range []string{"download", "prepared", "backup", "installed", "state-saved", "cleanup"} {
		t.Run(phase, func(t *testing.T) {
			root, cfg, tx := fixture(t, true)
			target, stage, backup, _ := tx.paths(root, cfg)
			if phase != "download" {
				prepare(t, root, cfg, tx)
			}
			if phase == "backup" || phase == "installed" || phase == "state-saved" || phase == "cleanup" {
				if err := os.Rename(target, backup); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "installed" || phase == "state-saved" || phase == "cleanup" {
				if err := os.Rename(stage, target); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "state-saved" || phase == "cleanup" {
				st, _ := state.Load(root)
				st.Resources[tx.Key] = tx.Next
				state.Save(root, st)
			}
			if phase == "cleanup" {
				tx.Phase = "installed"
				storage.WriteJSON(journalPath(root), tx, 0600)
				os.Remove(filepath.Join(backup, "main.py"))
			}
			if err := Recover(root, cfg); err != nil {
				t.Fatal(err)
			}
			content, _ := os.ReadFile(filepath.Join(target, "main.py"))
			want := "new"
			if phase == "download" {
				want = "old"
			}
			if string(content) != want {
				t.Fatal("incorrect recovered content", string(content))
			}
			secret, _ := os.ReadFile(filepath.Join(target, ".env"))
			if string(secret) != "secret" {
				t.Fatal("lost ignored file")
			}
			st, _ := state.Load(root)
			version := 2
			if phase == "download" {
				version = 1
			}
			if st.Resources[tx.Key].Version != version {
				t.Fatal("state not reconciled")
			}
			if _, err := os.Stat(journalPath(root)); !os.IsNotExist(err) {
				t.Fatal("journal retained")
			}
			if err := Recover(root, cfg); err != nil {
				t.Fatal("recovery not idempotent", err)
			}
		})
	}
}
func TestRestoreBackupWhenStageLost(t *testing.T) {
	root, cfg, tx := fixture(t, true)
	prepare(t, root, cfg, tx)
	target, stage, backup, _ := tx.paths(root, cfg)
	os.Rename(target, backup)
	os.RemoveAll(stage)
	if err := Recover(root, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(target, "main.py"))
	st, _ := state.Load(root)
	if string(data) != "old" || st.Resources[tx.Key].Version != 1 {
		t.Fatal("did not restore original")
	}
}
func TestNewResourceRecovery(t *testing.T) {
	root, cfg, tx := fixture(t, false)
	prepare(t, root, cfg, tx)
	if err := Recover(root, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, tx.Target, "main.py"))
	if string(data) != "new" {
		t.Fatal("new resource not installed")
	}
}
func TestEditedCopiesPreserved(t *testing.T) {
	for _, copy := range []string{"target", "stage", "backup"} {
		t.Run(copy, func(t *testing.T) {
			root, cfg, tx := fixture(t, true)
			prepare(t, root, cfg, tx)
			target, stage, backup, _ := tx.paths(root, cfg)
			path := target
			if copy == "stage" {
				path = stage
			}
			if copy == "backup" {
				os.Rename(target, backup)
				path = backup
			}
			put(t, filepath.Join(path, ".env"), "edited secret")
			if err := Recover(root, cfg); err == nil {
				t.Fatal("discarded edited copy")
			}
			data, _ := os.ReadFile(filepath.Join(path, ".env"))
			if string(data) != "edited secret" {
				t.Fatal("lost edited data")
			}
			if _, err := os.Stat(journalPath(root)); err != nil {
				t.Fatal("lost recovery record")
			}
		})
	}
}
func TestInstalledLocalEditsPreserved(t *testing.T) {
	root, cfg, tx := fixture(t, true)
	prepare(t, root, cfg, tx)
	target, stage, backup, _ := tx.paths(root, cfg)
	os.Rename(target, backup)
	os.Rename(stage, target)
	st, _ := state.Load(root)
	st.Resources[tx.Key] = tx.Next
	state.Save(root, st)
	tx.Phase = "installed"
	storage.WriteJSON(journalPath(root), tx, 0600)
	put(t, filepath.Join(target, "main.py"), "local edit")
	if err := Recover(root, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(target, "main.py"))
	if string(data) != "local edit" {
		t.Fatal("overwrote installed edits")
	}
}
func TestJournalPathTamperingRejected(t *testing.T) {
	root, cfg, tx := fixture(t, true)
	tx.Stage = "../external"
	storage.WriteJSON(journalPath(root), tx, 0600)
	if err := Recover(root, cfg); err == nil {
		t.Fatal("accepted escaped journal")
	}
	if _, err := os.Stat(journalPath(root)); err != nil {
		t.Fatal("removed invalid journal")
	}
}

func TestIgnoredEditDuringDownloadRejected(t *testing.T) {
	root, cfg, tx := fixture(t, true)
	put(t, filepath.Join(root, tx.Target, ".env"), "new secret")
	hash, _ := resources.HashDirectory(filepath.Join(root, tx.Stage))
	if err := Prepare(root, cfg, tx, state.ResourceState{ResourceID: "r", Version: 2, CodeHash: hash}); err == nil {
		t.Fatal("accepted stale ignored-file snapshot")
	}
	if err := Recover(root, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, tx.Target, ".env"))
	if string(data) != "new secret" {
		t.Fatal("lost ignored edits")
	}
}
func TestLostNewStageRollsBack(t *testing.T) {
	root, cfg, tx := fixture(t, false)
	prepare(t, root, cfg, tx)
	os.RemoveAll(filepath.Join(root, tx.Stage))
	if err := Recover(root, cfg); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(root)
	if _, ok := st.Resources[tx.Key]; ok {
		t.Fatal("tracked nonexistent downloaded resource")
	}
}

func TestConnectorRecoveryPreservesNestedSyncAfterBackupRename(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cfg := config.Default("project", "demo", "")
	target := filepath.Join(root, "connectors", "shop")
	stage := filepath.Join(root, "connectors", ".shop.tmp-test")
	put(t, filepath.Join(target, "connector.py"), "old connector")
	put(t, filepath.Join(target, "syncs", "customers", "sync.py"), "uncommitted sync edits")
	put(t, filepath.Join(target, "syncs", "customers", ".env"), "local credentials")
	put(t, filepath.Join(stage, "connector.py"), "new connector")
	oldHash, _ := resources.HashDirectory(target, "connector")
	tx, err := Begin(root, cfg, "connector:shop", target, stage, true, oldHash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = resources.PreserveIgnored(target, stage, "connector"); err != nil {
		t.Fatal(err)
	}
	newHash, _ := resources.HashDirectory(stage, "connector")
	if err = Prepare(root, cfg, tx, state.ResourceState{ResourceID: "c", Version: 2, CodeHash: newHash}); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(target, filepath.Join(root, filepath.FromSlash(tx.Backup))); err != nil {
		t.Fatal(err)
	}
	if err = Recover(root, cfg); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"connector.py": "new connector", "syncs/customers/sync.py": "uncommitted sync edits", "syncs/customers/.env": "local credentials"} {
		data, err := os.ReadFile(filepath.Join(target, path))
		if err != nil || string(data) != want {
			t.Fatal(path, err)
		}
	}
	if err = Recover(root, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestQualifiedSyncRecoveryPath(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cfg := config.Default("project", "demo", "")
	target := filepath.Join(root, "connectors/shop/syncs/customers")
	stage := filepath.Join(root, "connectors/shop/syncs/.customers.tmp-test")
	put(t, filepath.Join(stage, "sync.py"), "sync code")
	tx, err := Begin(root, cfg, "sync:shop/customers", target, stage, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := resources.HashDirectory(stage, "sync")
	if err = Prepare(root, cfg, tx, state.ResourceState{ResourceID: "s", Version: 1, CodeHash: hash}); err != nil {
		t.Fatal(err)
	}
	if err = Recover(root, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(target, "sync.py")); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(root)
	if err != nil || st.Resources["sync:shop/customers"].ResourceID != "s" {
		t.Fatal(err)
	}
}
