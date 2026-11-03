package resources

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"vortexify-cli/internal/config"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestHashDeterminismIgnoresAndArchive(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "z.py", "z")
	write(t, a, "nested/a.py", "a")
	write(t, b, "nested/a.py", "a")
	write(t, b, "z.py", "z")
	ha, err := HashDirectory(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := HashDirectory(b)
	if ha != hb {
		t.Fatal("hash depends on insertion order")
	}
	for _, name := range []string{"node_modules/pkg/file", "dist/app.js", "__pycache__/cached.pyc", ".env", ".env.local", ".git/config", ".venv/bin/python", ".DS_Store"} {
		write(t, a, name, "ignored")
	}
	ignored, _ := HashDirectory(a)
	if ignored != ha {
		t.Fatal("ignored files changed hash")
	}
	var buffer bytes.Buffer
	if err = CreateArchive(a, &buffer); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err = ExtractArchive(&buffer, target); err != nil {
		t.Fatal(err)
	}
	extracted, _ := HashDirectory(target)
	if extracted != ha {
		t.Fatal("archive/hash mismatch")
	}
	if _, err = os.Stat(filepath.Join(target, ".env")); !os.IsNotExist(err) {
		t.Fatal("archived secret")
	}
	write(t, b, "z.py", "changed")
	hb, _ = HashDirectory(b)
	if hb == ha {
		t.Fatal("content not hashed")
	}
}
func TestHashFramesPathsAndContents(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "a", "bc")
	write(t, b, "ab", "c")
	ha, _ := HashDirectory(a)
	hb, _ := HashDirectory(b)
	if ha == hb {
		t.Fatal("ambiguous hash framing")
	}
}
func badArchive(t *testing.T, name string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	h := &tar.Header{Name: name, Mode: 0644, Typeflag: kind, Linkname: "/tmp/escape"}
	if kind == tar.TypeReg {
		h.Size = 1
	}
	if err := tw.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if h.Size > 0 {
		tw.Write([]byte("x"))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func TestUnsafeArchivesRejected(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "nested/../../escape", "C:/escape", "nested\\escape", ".env"} {
		t.Run(name, func(t *testing.T) {
			if err := ExtractArchive(bytes.NewReader(badArchive(t, name, tar.TypeReg)), t.TempDir()); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
	if err := ExtractArchive(bytes.NewReader(badArchive(t, "link", tar.TypeSymlink)), t.TempDir()); err == nil {
		t.Fatal("accepted symlink")
	}
}
func TestSymlinksRejected(t *testing.T) {
	root := t.TempDir()
	os.Symlink(t.TempDir(), filepath.Join(root, "linked"))
	if _, err := HashDirectory(root); err == nil {
		t.Fatal("hash accepted symlink")
	}
	if _, err := SafePath(root, "linked/file"); err == nil {
		t.Fatal("safe path accepted symlink")
	}
}
func TestFoldersAndImmediateScan(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig()
	write(t, root, "transforms/orders/main.py", "code")
	write(t, root, "transforms/orders/nested/helper.py", "helper")
	write(t, root, "transforms/.hidden/main.py", "hidden")
	write(t, root, "transforms/README.md", "not resource")
	local, err := Scan(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(local) != 1 || local[0].Name != "orders" || local[0].Type != "transform" {
		t.Fatalf("unexpected scan %#v", local)
	}
	if _, ok := FolderForType(cfg, "sync"); ok {
		t.Fatal("unconfigured type managed")
	}
}
func DefaultConfig() *config.ProjectConfig {
	return &config.ProjectConfig{ProjectID: "id", Transforms: &config.ResourceFolder{Folder: "transforms"}}
}
func TestReplacePreservesIgnored(t *testing.T) {
	parent := t.TempDir()
	old := filepath.Join(parent, "old")
	staged := filepath.Join(parent, "stage")
	write(t, old, "main.py", "old")
	write(t, old, ".env", "secret")
	write(t, staged, "main.py", "new")
	if err := PreserveIgnored(old, staged); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceDirectory(old, staged); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(old, ".env"))
	code, _ := os.ReadFile(filepath.Join(old, "main.py"))
	if string(env) != "secret" || string(code) != "new" {
		t.Fatal("lost local files")
	}
}

func TestArchiveHashMatchesExactBytes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.py", "code")
	write(t, root, ".env", "ignored")
	var archive bytes.Buffer
	hash, err := CreateArchiveWithHash(root, &archive)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err = ExtractArchive(&archive, target); err != nil {
		t.Fatal(err)
	}
	extracted, err := HashDirectory(target)
	if err != nil || extracted != hash {
		t.Fatal("archive hash does not match bytes", err)
	}
}

func TestNestedSyncsAndConnectorSourceIsolation(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default("project", "demo", "")
	for _, owner := range []string{"shop", "crm"} {
		write(t, root, "connectors/"+owner+"/connector.py", "# connector")
		write(t, root, "connectors/"+owner+"/syncs/customers/sync.py", "# sync")
	}
	rows, err := Scan(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatal(rows)
	}
	hashes := map[string]string{}
	for _, row := range rows {
		hashes[row.Type+":"+row.Name] = row.Hash
	}
	if hashes["sync:shop/customers"] == "" || hashes["sync:crm/customers"] == "" {
		t.Fatal("missing qualified syncs")
	}
	write(t, root, "connectors/shop/syncs/customers/sync.py", "# changed sync")
	rows, err = Scan(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Type == "connector" && hashes[row.Type+":"+row.Name] != row.Hash {
			t.Fatal("sync edit changed connector hash")
		}
	}
	var archive bytes.Buffer
	if err := CreateArchive(filepath.Join(root, "connectors/shop"), &archive, "connector"); err != nil {
		t.Fatal(err)
	}
	unpack := t.TempDir()
	if err := ExtractArchive(&archive, unpack); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(unpack, "syncs")); !os.IsNotExist(err) {
		t.Fatal("syncs leaked into connector archive")
	}
	names := []RegistryEntry{{Type: "sync", Name: "shop/customers"}, {Type: "sync", Name: "crm/customers"}}
	if err := (Filter{Type: "sync", Resource: "customers"}).CheckSelection(names); err == nil {
		t.Fatal("ambiguous short sync name accepted")
	}
	if err := (Filter{Type: "sync", Resource: "shop/customers"}).CheckSelection(names); err != nil {
		t.Fatal(err)
	}
}
