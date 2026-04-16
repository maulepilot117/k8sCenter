package resources

import (
	"fmt"
	"sort"
	"sync"
)

// registry holds all registered resource adapters, keyed by Kind().
// Adapters self-register via Register() in their init() functions.
var (
	registryMu sync.RWMutex
	registry   = make(map[string]ResourceAdapter)
)

// Register adds an adapter to the global registry.
// Panics if an adapter with the same Kind() is already registered,
// preventing silent overwrites from copy-paste errors.
func Register(a ResourceAdapter) {
	registryMu.Lock()
	defer registryMu.Unlock()

	kind := a.Kind()
	if _, exists := registry[kind]; exists {
		panic(fmt.Sprintf("resources: duplicate adapter registration for kind %q", kind))
	}
	registry[kind] = a
}

// GetAdapter returns the adapter for the given kind, or nil if not registered.
func GetAdapter(kind string) ResourceAdapter {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[kind]
}

// RegisteredKinds returns a sorted list of all registered adapter kinds.
func RegisteredKinds() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	kinds := make([]string, 0, len(registry))
	for k := range registry {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}
