package resources

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed source.py
var sourceParser string

type Reference struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	APIID string `json:"api_id"`
}

func ReadReferences(ctx context.Context, resource Resource) ([]Reference, error) {
	if resource.Type == "app" || resource.Type == "database" {
		return nil, nil
	}
	paths, err := Files(resource.Path, resource.Type)
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, path := range paths {
		if !strings.HasSuffix(path, ".py") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(resource.Path, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		files[path] = string(data)
	}
	data, err := json.Marshal(map[string]any{"type": resource.Type, "files": files})
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-c", sourceParser)
	cmd.Stdin = bytes.NewReader(data)
	out, runErr := cmd.Output()
	var result struct {
		References []Reference `json:"references"`
		Error      string      `json:"error"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("source parsing requires Python 3: %w", runErr)
		}
		return nil, fmt.Errorf("invalid source parser response: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("%s/%s: %s", resource.Type, resource.Name, result.Error)
	}
	if runErr != nil {
		return nil, runErr
	}
	return result.References, nil
}
