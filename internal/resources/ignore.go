package resources

import (
	"path/filepath"
	"strings"
)

func ShouldIgnore(root, path string, isDir bool, kind ...string) bool {
	if len(kind) > 0 && (kind[0] == "context" || kind[0] == "knowledge-base") {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	if len(kind) > 0 && kind[0] == "connector" && (filepath.ToSlash(rel) == "syncs" || strings.HasPrefix(filepath.ToSlash(rel), "syncs/")) {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		switch part {
		case ".DS_Store", ".git", ".vortexify", "__pycache__", "node_modules", "dist", "build", ".next", "coverage", ".venv", "venv":
			return true
		}
		if part == ".env" || strings.HasPrefix(part, ".env.") || strings.HasSuffix(part, ".pyc") {
			return true
		}
	}
	return false
}
