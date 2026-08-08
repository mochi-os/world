package aircraft

import "testing"

// Grant must never return nil — its whole contract is that spawning paths
// can hand the result straight to flight.New's immediate dereference. An
// unknown name is a live case, not a can't-happen: a newer client's jet
// arriving at an older server's join payload.
func TestGrantNeverNil(t *testing.T) {
	for _, name := range []string{"", "fa18c", "fa18d", "su27", "not an aircraft at all"} {
		kind, airframe := Grant(name)
		if airframe == nil {
			t.Fatalf("Grant(%q) returned a nil airframe", name)
		}
		if Get(kind) != airframe {
			t.Fatalf("Grant(%q) granted kind %q, which Get does not resolve to the same airframe — a stored kind must survive respawn", name, kind)
		}
		if kind == "" {
			t.Fatalf("Grant(%q) granted the empty kind — the stored name must be canonical", name)
		}
	}
	if kind, _ := Grant("fa18c"); kind != "fa18c" {
		t.Fatalf("a valid request was not honoured: asked fa18c, granted %q", kind)
	}
	if kind, _ := Grant("no such jet"); kind != "fa18c" {
		t.Fatalf("an unknown request must fall back to the default, got %q", kind)
	}
}
