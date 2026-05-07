package bindings

import (
	"context"
	"reflect"
	"testing"

	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/types"
)

func TestNewCommandRenderer(t *testing.T) {
	targets := map[string]ispell.Target{
		"lint": {Cmd: "go", Args: []string{"tool", "golangci-lint", "run", "./..."}, Charms: map[string]ispell.Charm{
			"write": {Ops: []ispell.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
			"debug": {Ops: []ispell.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		}},
		"build": {Cmd: "go", Args: []string{"build"}},
		"fn":    {Func: "handler"}, // function-op: no static command
		"noop":  {},                // empty cmd
	}
	render := newCommandRenderer(targets)

	cases := []struct {
		name     string
		target   string
		charms   []string
		wantCmd  string
		wantArgs []string
		wantOK   bool
	}{
		{"base, no charms", "lint", nil, "go", []string{"tool", "golangci-lint", "run", "./..."}, true},
		{"charms applied", "lint", []string{"write", "debug"}, "go", []string{"tool", "golangci-lint", "run", "--fix", "./...", "-v"}, true},
		{"charmless target", "build", []string{"write"}, "go", []string{"build"}, true},
		{"function-op → no command", "fn", nil, "", nil, false},
		{"no-op (empty cmd) → none", "noop", nil, "", nil, false},
		{"unknown target → none", "missing", nil, "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, args, ok := render(tc.target, tc.charms)
			if ok != tc.wantOK || cmd != tc.wantCmd || !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("render(%q, %v) = (%q, %v, %v), want (%q, %v, %v)",
					tc.target, tc.charms, cmd, args, ok, tc.wantCmd, tc.wantArgs, tc.wantOK)
			}
		})
	}
}

func TestResolveCharmArgs(t *testing.T) {
	base := []string{"run", "./..."}
	// write inserts --fix before ./... (index 1); debug/trace append at the end.
	charmArgs := map[string]ispell.Charm{
		"write": {Ops: []ispell.PatchOp{{Op: "add", Path: "/1", Value: "--fix"}}},
		"debug": {Ops: []ispell.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		"trace": {Ops: []ispell.PatchOp{{Op: "add", Path: "/-", Value: "--trace"}}},
	}
	with := func(names ...string) context.Context {
		return types.WithCharms(context.Background(), names)
	}

	cases := []struct {
		name string
		ctx  context.Context
		want []string
	}{
		{"none active", context.Background(), []string{"run", "./..."}},
		{"append one", with("debug"), []string{"run", "./...", "-v"}},
		{"insert one", with("write"), []string{"run", "--fix", "./..."}},
		{"insert + append compose", with("write", "debug"), []string{"run", "--fix", "./...", "-v"}},
		{"appends sorted, order-independent", with("trace", "debug"), []string{"run", "./...", "-v", "--trace"}},
		{"duplicate active charm applied once", with("debug", "debug"), []string{"run", "./...", "-v"}},
		{"unknown charm ignored", with("nope"), []string{"run", "./..."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCharmArgs(tc.ctx, base, charmArgs)
			if err != nil {
				t.Fatalf("resolveCharmArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveCharmArgs = %v, want %v", got, tc.want)
			}
		})
	}

	// The base slice must never be mutated.
	if !reflect.DeepEqual(base, []string{"run", "./..."}) {
		t.Errorf("base mutated: %v", base)
	}
	// nil charmArgs is a no-op.
	if got, err := resolveCharmArgs(with("write"), base, nil); err != nil || !reflect.DeepEqual(got, base) {
		t.Errorf("nil charmArgs: got %v, err %v, want %v", got, err, base)
	}
}

func TestDedupStrings(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{nil, nil},
		{[]string{"a"}, []string{"a"}},
		{[]string{"a", "b", "a"}, []string{"a", "b"}}, // manual + glob overlap
		{[]string{"go-build", "image-build", "go-build"}, []string{"go-build", "image-build"}},
		{[]string{"a", "a", "a"}, []string{"a"}},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}}, // no dups: unchanged
	}
	for _, tc := range cases {
		got := dedupStrings(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("dedupStrings(%v) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("dedupStrings(%v) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}
