package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"vortexify-cli/internal/storage"
)

type ResourceState struct {
	ResourceID string `json:"resource_id"`
	Version    int    `json:"version"`
	CodeHash   string `json:"code_hash"`
}
type State struct {
	Resources map[string]ResourceState `json:"resources"`
}

func ResourceKey(resourceType, name string) string { return resourceType + ":" + name }
func Load(root string) (*State, error) {
	s := &State{Resources: map[string]ResourceState{}}
	path := filepath.Join(root, ".vortexify", "state.json")
	if err := storage.CheckFilePath(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Resources == nil {
		s.Resources = map[string]ResourceState{}
	}
	return s, nil
}
func Save(root string, s *State) error {
	for key, row := range s.Resources {
		if strings.HasPrefix(key, "sync:") && strings.Contains(key, "/") {
			QualifySync(s, row.ResourceID, strings.TrimPrefix(key, "sync:"))
		}
	}
	return storage.WriteJSON(filepath.Join(root, ".vortexify", "state.json"), s, 0600)
}

func QualifySync(s *State, id, name string) {
	if !strings.Contains(name, "/") {
		return
	}
	key := ResourceKey("sync", name)
	current, exists := s.Resources[key]
	if exists && current.ResourceID != id {
		return
	}
	for old, row := range s.Resources {
		if strings.HasPrefix(old, "sync:") && !strings.Contains(old, "/") && row.ResourceID == id {
			if !exists {
				s.Resources[key] = row
			}
			delete(s.Resources, old)
			return
		}
	}
}
