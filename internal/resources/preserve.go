package resources

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// PreserveIgnored keeps local credentials and build artifacts during a code update.
func PreserveIgnored(source, target string, kind ...string) error {
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if !ShouldIgnore(source, path, d.IsDir(), kind...) {
			return nil
		}
		dest := filepath.Join(target, rel)
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot preserve ignored symlink %s", path)
		}
		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot preserve non-regular file %s", path)
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
