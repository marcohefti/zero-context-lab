package native

import "testing"

func TestRegistryRegisterAndLookup(t *testing.T) {
	reg := NewRegistry()
	rt := fakeRuntime{id: "alpha"}
	if err := reg.Register(rt); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := reg.Get("alpha")
	if !ok {
		t.Fatalf("expected runtime")
	}
	if got.ID() != "alpha" {
		t.Fatalf("unexpected id: %q", got.ID())
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(fakeRuntime{id: "alpha"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := reg.Register(fakeRuntime{id: "alpha"}); err == nil {
		t.Fatalf("expected duplicate error")
	}
}

func TestRegistryIDsAreSorted(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(fakeRuntime{id: "zeta"})
	reg.MustRegister(fakeRuntime{id: "alpha"})
	ids := reg.IDs()
	if len(ids) != 2 {
		t.Fatalf("expected two ids, got %d", len(ids))
	}
	if ids[0] != "alpha" || ids[1] != "zeta" {
		t.Fatalf("unexpected ids: %#v", ids)
	}
}
