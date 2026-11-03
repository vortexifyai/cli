package resources

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func CreateArchive(path string, w io.Writer, kind ...string) error {
	_, err := CreateArchiveWithHash(path, w, kind...)
	return err
}

// CreateArchiveWithHash hashes the actual bytes placed in the archive.
func CreateArchiveWithHash(path string, w io.Writer, kind ...string) (string, error) {
	files, err := Files(path, kind...)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	h := sha256.New()
	for _, rel := range files {
		f, err := os.Open(filepath.Join(path, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return "", err
		}
		hdr := &tar.Header{Name: rel, Mode: 0644, Size: info.Size(), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
		if err = tw.WriteHeader(hdr); err != nil {
			f.Close()
			return "", err
		}
		binary.Write(h, binary.BigEndian, uint64(len(rel)))
		io.WriteString(h, rel)
		binary.Write(h, binary.BigEndian, uint64(info.Size()))
		_, err = io.CopyN(io.MultiWriter(tw, h), f, info.Size())
		f.Close()
		if err != nil {
			return "", err
		}
	}
	if err = tw.Close(); err != nil {
		return "", err
	}
	if err = gz.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

const MaxArchiveBytes int64 = 4 << 30

func ExtractArchive(r io.Reader, target string) error {
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	entries := 0
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			n, err := io.Copy(io.Discard, io.LimitReader(gz, (1<<20)+1))
			if err != nil {
				return err
			}
			if n > 1<<20 {
				return fmt.Errorf("excessive archive padding")
			}
			return nil
		}
		if err != nil {
			return err
		}
		entries++
		if entries > 100000 {
			return fmt.Errorf("archive exceeds entry limit")
		}
		if strings.ContainsAny(hdr.Name, "\\:\x00") || strings.HasPrefix(hdr.Name, "/") {
			return fmt.Errorf("unsafe archive path %q", hdr.Name)
		}
		for _, component := range strings.Split(hdr.Name, "/") {
			if component == ".." {
				return fmt.Errorf("unsafe archive path %q", hdr.Name)
			}
		}
		path, err := SafePath(target, filepath.FromSlash(hdr.Name))
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeDir {
			if ShouldIgnore(target, path, true) {
				return fmt.Errorf("ignored archive directory %q", hdr.Name)
			}
			if err = os.MkdirAll(path, 0755); err != nil {
				return err
			}
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return fmt.Errorf("unsupported archive entry %q", hdr.Name)
		}
		if path == filepath.Clean(target) || ShouldIgnore(target, path, false) || seen[path] {
			return fmt.Errorf("invalid archive entry %q", hdr.Name)
		}
		seen[path] = true
		if hdr.Size < 0 || hdr.Size > MaxArchiveBytes-total {
			return fmt.Errorf("archive exceeds extraction limit")
		}
		total += hdr.Size
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, err = io.CopyN(f, tr, hdr.Size)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
}

// ReplaceDirectory stages a complete replacement and rolls back a failed rename.
func ReplaceDirectory(target, staged string) error {
	backup := target + ".vortexify-backup"
	if _, err := os.Lstat(backup); err == nil {
		return fmt.Errorf("backup already exists: %s", backup)
	} else if !os.IsNotExist(err) {
		return err
	}
	exists := false
	if _, err := os.Lstat(target); err == nil {
		exists = true
		if err = os.Rename(target, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if exists {
			if rollback := os.Rename(backup, target); rollback != nil {
				return fmt.Errorf("replace failed: %v; rollback failed: %v; original at %s", err, rollback, backup)
			}
		}
		return err
	}
	if exists {
		return os.RemoveAll(backup)
	}
	return nil
}
