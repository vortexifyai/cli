package resources

import (
	"fmt"
	"sort"
	"strings"
)

type Resource struct {
	ID   string
	Type string
	Name string
	Path string
	Hash string
}
type Filter struct {
	Type     string
	Resource string
}

func (f Filter) Match(t, name, id string) bool {
	return (f.Type == "" || f.Type == t) && (f.Resource == "" || f.Resource == name || f.Resource == id || (t == "sync" && f.Resource == SyncBaseName(name)))
}
func (f Filter) Validate() error {
	if f.Type != "" {
		switch f.Type {
		case "transform", "api", "agent", "connector", "sync", "database", "app":
		default:
			return fmt.Errorf("unsupported resource type %q", f.Type)
		}
	}
	return nil
}
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\:\x00") {
		return fmt.Errorf("unsafe resource name %q", name)
	}
	return nil
}

func SyncBaseName(name string) string {
	_, base, qualified := strings.Cut(name, "/")
	if qualified {
		return base
	}
	return name
}
func ValidateResourceName(kind, name string) error {
	if kind != "sync" {
		return ValidateName(name)
	}
	owner, base, ok := strings.Cut(name, "/")
	if !ok {
		return fmt.Errorf("sync name must be qualified as connector/sync: %q", name)
	}
	if err := ValidateName(owner); err != nil {
		return err
	}
	return ValidateName(base)
}
func (f Filter) CheckSelection(entries []RegistryEntry) error {
	if f.Resource == "" || strings.Contains(f.Resource, "/") || (f.Type != "" && f.Type != "sync") {
		return nil
	}
	matches := map[string]bool{}
	for _, row := range entries {
		if row.Type == "sync" && f.Match(row.Type, row.Name, row.ID) {
			matches[row.Name] = true
		}
	}
	if len(matches) > 1 {
		names := []string{}
		for name := range matches {
			names = append(names, name)
		}
		sort.Strings(names)
		return fmt.Errorf("ambiguous sync %q; choose %s", f.Resource, strings.Join(names, ", "))
	}
	return nil
}
