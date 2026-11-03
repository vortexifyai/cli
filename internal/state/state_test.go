package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := Load(root)
	if err != nil || len(s.Resources) != 0 {
		t.Fatal(err)
	}
	key := ResourceKey("transform", "orders")
	s.Resources[key] = ResourceState{"id", 2, "hash"}
	if err = Save(root, s); err != nil {
		t.Fatal(err)
	}
	again, err := Load(root)
	if err != nil || again.Resources[key] != s.Resources[key] {
		t.Fatalf("roundtrip %v", err)
	}
	info, _ := os.Stat(filepath.Join(root, ".vortexify", "state.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("state permissions")
	}
}
func TestCorruptState(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".vortexify"), 0700)
	os.WriteFile(filepath.Join(root, ".vortexify", "state.json"), []byte("{"), 0600)
	if _, err := Load(root); err == nil {
		t.Fatal("accepted corrupt state")
	}
}

func TestQualifyLegacySyncBaselineByIDWithoutOverwritingNewerState(t *testing.T) {
	old := ResourceState{ResourceID: "a", Version: 1, CodeHash: "old"}
	newer := ResourceState{ResourceID: "a", Version: 2, CodeHash: "new"}
	s := &State{Resources: map[string]ResourceState{"sync:customers": old}}
	QualifySync(s, "b", "crm/customers")
	if len(s.Resources) != 1 {
		t.Fatal("migrated a different resource's baseline")
	}
	QualifySync(s, "a", "shop/customers")
	if s.Resources["sync:shop/customers"] != old {
		t.Fatal("lost baseline")
	}
	s.Resources["sync:customers"] = old
	s.Resources["sync:shop/customers"] = newer
	QualifySync(s, "a", "shop/customers")
	if s.Resources["sync:shop/customers"] != newer {
		t.Fatal("overwrote newer state")
	}
	if _, exists := s.Resources["sync:customers"]; exists {
		t.Fatal("retained old alias")
	}
}
