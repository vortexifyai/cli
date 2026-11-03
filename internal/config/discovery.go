package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrProjectNotFound = errors.New("project file not found")

func FindProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return FindProjectRootFrom(cwd)
}
func FindProjectRootFrom(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		info, err := os.Stat(filepath.Join(current, ConfigFileName))
		if err == nil && !info.IsDir() {
			return current, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("%w: no %s found; run vortexify pull --project-id <id> or vortexify init", ErrProjectNotFound, ConfigFileName)
		}
		current = parent
	}
}
