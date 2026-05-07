package spell_test

import (
	"testing"

	ispell "github.com/egladman/magus/internal/spell"
)

func TestValidatePatch(t *testing.T) {
	cases := []struct {
		name    string
		ops     []ispell.PatchOp
		wantErr bool
	}{
		{"empty", nil, false},
		{"add end", []ispell.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}, false},
		{"replace index", []ispell.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}, false},
		{"remove index", []ispell.PatchOp{{Op: "remove", Path: "/2"}}, false},
		{"move", []ispell.PatchOp{{Op: "move", Path: "/0", From: "/1"}}, false},
		{"copy", []ispell.PatchOp{{Op: "copy", Path: "/0", From: "/1"}}, false},
		{"test", []ispell.PatchOp{{Op: "test", Path: "/0", Value: "go"}}, false},
		{"unknown op", []ispell.PatchOp{{Op: "patch", Path: "/0"}}, true},
		{"root path rejected", []ispell.PatchOp{{Op: "replace", Path: "", Value: "x"}}, true},
		{"path without slash", []ispell.PatchOp{{Op: "add", Path: "0", Value: "x"}}, true},
		{"move without from", []ispell.PatchOp{{Op: "move", Path: "/0"}}, true},
		{"copy bad from", []ispell.PatchOp{{Op: "copy", Path: "/0", From: "1"}}, true},
	}
	for _, tc := range cases {
		err := ispell.ValidatePatch(tc.ops)
		if tc.wantErr && err == nil {
			t.Errorf("ValidatePatch(%s) = nil, want error", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("ValidatePatch(%s) = %v, want nil", tc.name, err)
		}
	}
}

func TestSpec_TargetNames(t *testing.T) {
	m := ispell.Spec{
		Name: "test",
		Targets: map[string]ispell.Target{
			"vet":   {},
			"build": {},
			"test":  {},
		},
	}
	got := m.TargetNames()
	want := []string{"build", "test", "vet"}
	if len(got) != len(want) {
		t.Fatalf("TargetNames() = %v, want %v", got, want)
	}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("TargetNames()[%d] = %q, want %q", i, v, want[i])
		}
	}
}

func TestSpec_TargetNamesEmpty(t *testing.T) {
	m := ispell.Spec{Name: "empty"}
	got := m.TargetNames()
	if len(got) != 0 {
		t.Errorf("TargetNames() on empty Targets = %v, want []", got)
	}
}
