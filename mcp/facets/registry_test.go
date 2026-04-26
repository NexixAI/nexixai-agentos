package facets

import (
	"sync"
	"testing"
)

func TestNewFacetRegistry_DefaultFacets(t *testing.T) {
	r := NewFacetRegistry()
	facets := r.List()

	wantNames := map[string]bool{"task": false, "complexity": false, "thinking": false}
	for _, f := range facets {
		if _, ok := wantNames[f.Name]; ok {
			wantNames[f.Name] = true
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("default facet %q not found in registry", name)
		}
	}
}

func TestRegister(t *testing.T) {
	r := NewFacetRegistry()

	f := Facet{
		Name:        "language",
		Description: "Natural language of the prompt.",
		Values:      []string{"en", "es", "fr"},
		Dynamic:     true,
	}
	if err := r.Register(f); err != nil {
		t.Fatalf("Register() returned error: %v", err)
	}

	got := r.Get("language")
	if got == nil {
		t.Fatal("Get() returned nil after Register")
	}
	if got.Name != "language" {
		t.Errorf("Name = %q, want %q", got.Name, "language")
	}
	if got.Description != f.Description {
		t.Errorf("Description = %q, want %q", got.Description, f.Description)
	}
	if len(got.Values) != 3 {
		t.Errorf("Values length = %d, want 3", len(got.Values))
	}
	if !got.Dynamic {
		t.Error("Dynamic = false, want true")
	}
}

func TestRegister_EmptyName(t *testing.T) {
	r := NewFacetRegistry()
	err := r.Register(Facet{Description: "no name"})
	if err == nil {
		t.Fatal("Register() with empty name should return error")
	}
}

func TestRegister_Duplicate(t *testing.T) {
	r := NewFacetRegistry()

	// "task" is a default facet — registering it again should fail.
	err := r.Register(Facet{Name: "task", Description: "duplicate"})
	if err == nil {
		t.Fatal("duplicate Register() should return error")
	}
}

func TestUnregister(t *testing.T) {
	r := NewFacetRegistry()

	f := Facet{Name: "temp", Description: "temporary"}
	if err := r.Register(f); err != nil {
		t.Fatalf("Register() returned error: %v", err)
	}

	if err := r.Unregister("temp"); err != nil {
		t.Fatalf("Unregister() returned error: %v", err)
	}

	if got := r.Get("temp"); got != nil {
		t.Error("Get() should return nil after Unregister")
	}
}

func TestUnregister_NotFound(t *testing.T) {
	r := NewFacetRegistry()
	err := r.Unregister("nonexistent")
	if err == nil {
		t.Fatal("Unregister() for unknown facet should return error")
	}
}

func TestGet_NotFound(t *testing.T) {
	r := NewFacetRegistry()
	if got := r.Get("nonexistent"); got != nil {
		t.Errorf("Get() for unknown facet should return nil, got %v", got)
	}
}

func TestGet_ReturnsCopy(t *testing.T) {
	r := NewFacetRegistry()

	got := r.Get("task")
	if got == nil {
		t.Fatal("Get(\"task\") returned nil")
	}

	// Mutate the returned copy — should not affect the registry.
	got.Values = append(got.Values, "mutated")

	original := r.Get("task")
	for _, v := range original.Values {
		if v == "mutated" {
			t.Fatal("mutating Get() result affected the registry — not a true copy")
		}
	}
}

func TestList_Sorted(t *testing.T) {
	r := NewFacetRegistry()
	facets := r.List()

	for i := 1; i < len(facets); i++ {
		if facets[i].Name < facets[i-1].Name {
			t.Errorf("List() not sorted: %q came after %q", facets[i].Name, facets[i-1].Name)
		}
	}
}

func TestList_Snapshot(t *testing.T) {
	r := NewFacetRegistry()
	snap := r.List()
	before := len(snap)

	if err := r.Register(Facet{Name: "new-facet", Description: "after snapshot"}); err != nil {
		t.Fatalf("Register() returned error: %v", err)
	}

	if len(snap) != before {
		t.Error("List() snapshot was mutated by subsequent Register")
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := NewFacetRegistry()

	var wg sync.WaitGroup
	const n = 100

	// Concurrent reads.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.List()
			_ = r.Get("task")
		}()
	}

	// Concurrent writes.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f := Facet{
				Name:        "concurrent-" + string(rune('A'+i%26)),
				Description: "concurrent test",
			}
			// Ignore errors — duplicates are expected.
			_ = r.Register(f) //nolint:errcheck // concurrent test, duplicates expected
		}(i)
	}

	wg.Wait()

	// Sanity check: defaults still present.
	if r.Get("task") == nil {
		t.Error("default facet 'task' missing after concurrent access")
	}
}
