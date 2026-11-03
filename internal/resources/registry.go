package resources

import "fmt"

type RegistryEntry struct{ ID, Type, Name string }

func ValidateRegistry(entries []RegistryEntry) error {
	keys := map[string]bool{}
	ids := map[string]bool{}
	for _, r := range entries {
		if r.ID == "" {
			return fmt.Errorf("registry contains a resource without an ID")
		}
		if err := ValidateResourceName(r.Type, r.Name); err != nil {
			return err
		}
		key := r.Type + ":" + r.Name
		if keys[key] || ids[r.ID] {
			return fmt.Errorf("duplicate registry resource %s", key)
		}
		keys[key] = true
		ids[r.ID] = true
	}
	return nil
}
