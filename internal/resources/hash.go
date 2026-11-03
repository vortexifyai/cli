package resources

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// Hash framing is uint64 path length, path bytes, uint64 content length, content bytes.
func HashDirectory(path string, kind ...string) (string, error) {
	files, err := Files(path, kind...)
	if err != nil {
		return "", err
	}
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
		binary.Write(h, binary.BigEndian, uint64(len(rel)))
		io.WriteString(h, rel)
		binary.Write(h, binary.BigEndian, uint64(info.Size()))
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
		if n != info.Size() {
			return "", io.ErrUnexpectedEOF
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
