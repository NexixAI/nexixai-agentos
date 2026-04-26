package facets

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Facet describes a classification dimension for prompt analysis.
type Facet struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Values      []string `json:"values"`  // Suggested values (not exhaustive).
	Dynamic     bool     `json:"dynamic"` // true = may change at runtime.
}

// FacetRegistry manages facet dimensions. It is safe for concurrent use.
type FacetRegistry struct {
	mu     sync.RWMutex
	facets map[string]*Facet
}

// NewFacetRegistry creates a FacetRegistry pre-loaded with the default facet
// dimensions (task, complexity, thinking).
func NewFacetRegistry() *FacetRegistry {
	r := &FacetRegistry{
		facets: make(map[string]*Facet),
	}
	for _, f := range defaultFacets() {
		// Default facets are trusted; ignore the impossible error.
		r.facets[f.Name] = &f
	}
	return r
}

// defaultFacets returns the built-in facet dimensions.
func defaultFacets() []Facet {
	return []Facet{
		{
			Name:        "task",
			Description: "Primary task type inferred from the prompt.",
			Values:      []string{"code", "reasoning", "factual", "creative", "analysis"},
			Dynamic:     false,
		},
		{
			Name:        "complexity",
			Description: "Estimated prompt complexity.",
			Values:      []string{"low", "medium", "high"},
			Dynamic:     false,
		},
		{
			Name:        "thinking",
			Description: "Whether extended thinking / chain-of-thought is recommended.",
			Values:      []string{"true", "false"},
			Dynamic:     false,
		},
	}
}

// Register adds a facet to the registry. Returns an error if a facet with the
// same name already exists.
func (r *FacetRegistry) Register(f Facet) error {
	if f.Name == "" {
		return fmt.Errorf("facets: facet name must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.facets[f.Name]; exists {
		return fmt.Errorf("facets: facet %q is already registered", f.Name)
	}

	r.facets[f.Name] = &f
	slog.Info("facets: facet registered", "facet", f.Name)
	return nil
}

// Unregister removes a facet by name. Returns an error if not found.
func (r *FacetRegistry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.facets[name]; !exists {
		return fmt.Errorf("facets: facet %q not found", name)
	}

	delete(r.facets, name)
	slog.Info("facets: facet unregistered", "facet", name)
	return nil
}

// Get returns the facet with the given name, or nil if not found.
func (r *FacetRegistry) Get(name string) *Facet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f := r.facets[name]
	if f == nil {
		return nil
	}
	// Return a copy to avoid data races on the caller side.
	cp := *f
	cp.Values = make([]string, len(f.Values))
	copy(cp.Values, f.Values)
	return &cp
}

// List returns all registered facets sorted by name. The returned slice is a
// snapshot; mutations to the registry after List returns are not reflected.
func (r *FacetRegistry) List() []Facet {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Facet, 0, len(r.facets))
	for _, f := range r.facets {
		cp := *f
		cp.Values = make([]string, len(f.Values))
		copy(cp.Values, f.Values)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}
