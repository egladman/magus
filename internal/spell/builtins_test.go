package spell

import (
	"testing"
)

func TestBuiltins_NonEmpty(t *testing.T) {
	m := Builtins()
	if len(m) == 0 {
		t.Fatal("Builtins() returned empty map")
	}
	for key, s := range m {
		if s.Name == "" {
			t.Errorf("Builtins()[%q].Name is empty", key)
		}
		// The registry is keyed by runtime name, so the key is the spell's Name.
		if key != s.Name {
			t.Errorf("Builtins() key %q != Spec.Name %q", key, s.Name)
		}
	}
}

func TestBuiltins_KeyedByName(t *testing.T) {
	m := Builtins()
	// The golang spell renames itself to "go": it must be reachable by name…
	if _, ok := m["go"]; !ok {
		t.Error(`Builtins()["go"] not found`)
	}
	// …and not by its source directory.
	if _, bad := m["golang"]; bad {
		t.Error(`Builtins()["golang"] present — registry is keyed by name, not source dir`)
	}
}

func TestBuiltinsHash_Format(t *testing.T) {
	h := BuiltinsHash()
	if len(h) != 64 {
		t.Errorf("BuiltinsHash() length = %d, want 64 (SHA-256 hex)", len(h))
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("BuiltinsHash() contains non-hex character %q", c)
			break
		}
	}
}

func TestBuiltinsHash_Stable(t *testing.T) {
	if h1, h2 := BuiltinsHash(), BuiltinsHash(); h1 != h2 {
		t.Errorf("BuiltinsHash() not stable: %q vs %q", h1, h2)
	}
}

func TestGoSpell_TidyTarget(t *testing.T) {
	goSpell := Builtins()["go"]
	tidy, ok := goSpell.Targets["go-mod-tidy"]
	if !ok {
		t.Fatalf("go spell has no go-mod-tidy target; targets: %v", goSpell.TargetNames())
	}
	// Default (no write charm): check mode via --diff (non-zero exit if changes
	// are needed — safe for CI gating).
	wantArgs := []string{"mod", "tidy", "--diff"}
	if tidy.Cmd != "go" || len(tidy.Args) != len(wantArgs) {
		t.Fatalf("tidy = {Cmd:%q Args:%v}, want {go %v}", tidy.Cmd, tidy.Args, wantArgs)
	}
	for i, a := range wantArgs {
		if tidy.Args[i] != a {
			t.Errorf("tidy.Args[%d] = %q, want %q", i, tidy.Args[i], a)
		}
	}
	// rw charm drops --diff (remove /2) so tidy actually applies the changes.
	w, ok := tidy.Charms["rw"]
	if !ok {
		t.Fatal("tidy has no rw charm")
	}
	want := PatchOp{Op: "remove", Path: "/2"}
	if len(w.Ops) != 1 || w.Ops[0] != want {
		t.Errorf("tidy rw charm Ops = %v, want [%v]", w.Ops, want)
	}
}
