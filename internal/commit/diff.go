package commit

import (
	"vortexify-cli/internal/api"
	"vortexify-cli/internal/resources"
	"vortexify-cli/internal/state"
)

type Change struct {
	Resource resources.Resource
	Remote   *api.Resource
}

func FindChanges(local []resources.Resource, remote []api.Resource) []Change {
	registry := map[string]api.Resource{}
	for _, r := range remote {
		registry[state.ResourceKey(r.Type, r.Name)] = r
	}
	changes := []Change{}
	for _, r := range local {
		server, ok := registry[state.ResourceKey(r.Type, r.Name)]
		if !ok {
			changes = append(changes, Change{Resource: r})
		} else if r.Hash != server.CodeHash || (server.Files != nil && server.Files.Changed) {
			copy := server
			changes = append(changes, Change{Resource: r, Remote: &copy})
		}
	}
	return changes
}
