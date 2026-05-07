package std

import (
	"context"
	"reflect"
	"testing"
)

// op builds the expected op map a constructor emits.
func op(fields ...string) map[string]any {
	m := map[string]any{"op": fields[0], "path": fields[1]}
	if len(fields) > 2 {
		m["value"] = fields[2]
	}
	return m
}

func wantCharm(ops ...map[string]any) map[string]any {
	arr := make([]any, len(ops))
	for i := range ops {
		arr[i] = ops[i]
	}
	return map[string]any{"ops": arr}
}

func TestCharmConstructors(t *testing.T) {
	ctx := context.Background()
	argv := []string{"run", "ruff", "check", "."}

	cases := []struct {
		name string
		got  func() (map[string]any, error)
		want map[string]any
	}{
		{"append", func() (map[string]any, error) { return CharmAppend(ctx, []string{"-v", "-x"}) },
			wantCharm(op("add", "/-", "-v"), op("add", "/-", "-x"))},
		{"prepend", func() (map[string]any, error) { return CharmPrepend(ctx, []string{"a", "b"}) },
			wantCharm(op("add", "/0", "a"), op("add", "/1", "b"))},
		{"after", func() (map[string]any, error) { return CharmAfter(ctx, argv, "check", []string{"--fix"}) },
			wantCharm(op("add", "/3", "--fix"))},
		{"before", func() (map[string]any, error) { return CharmBefore(ctx, argv, "check", []string{"--fix"}) },
			wantCharm(op("add", "/2", "--fix"))},
		{"set", func() (map[string]any, error) { return CharmSet(ctx, argv, "check", "format") },
			wantCharm(op("replace", "/2", "format"))},
		{"remove", func() (map[string]any, error) { return CharmRemove(ctx, argv, "check") },
			wantCharm(map[string]any{"op": "remove", "path": "/2"})},
		{"move to front", func() (map[string]any, error) { return CharmMove(ctx, argv, "check", "/0") },
			wantCharm(map[string]any{"op": "move", "from": "/2", "path": "/0"})},
		{"move to end", func() (map[string]any, error) { return CharmMove(ctx, argv, "run", "/-") },
			wantCharm(map[string]any{"op": "move", "from": "/0", "path": "/-"})},
		{"copy to end", func() (map[string]any, error) { return CharmCopy(ctx, argv, "check", "/-") },
			wantCharm(map[string]any{"op": "copy", "from": "/2", "path": "/-"})},
		{"test guard", func() (map[string]any, error) { return CharmTest(ctx, argv, "check") },
			wantCharm(map[string]any{"op": "test", "path": "/2", "value": "check"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.got()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCharmAnchorNotFound: a missing anchor is a spell bug surfaced at author time.
func TestCharmAnchorNotFound(t *testing.T) {
	ctx := context.Background()
	if _, err := CharmAfter(ctx, []string{"a", "b"}, "missing", []string{"x"}); err == nil {
		t.Error("after with missing anchor: want error, got nil")
	}
	if _, err := CharmRemove(ctx, []string{"a", "b"}, "missing"); err == nil {
		t.Error("remove with missing anchor: want error, got nil")
	}
	if _, err := CharmPath(ctx, []string{"a", "b"}, "missing"); err == nil {
		t.Error("path with missing anchor: want error, got nil")
	}
	if _, err := CharmMove(ctx, []string{"a", "b"}, "missing", "/0"); err == nil {
		t.Error("move with missing anchor: want error, got nil")
	}
	// A destination that isn't a JSON Pointer is rejected at author time.
	if _, err := CharmMove(ctx, []string{"a", "b"}, "a", "b"); err == nil {
		t.Error("move with non-pointer destination: want error, got nil")
	}
}

// TestCharmPath: the path helper resolves an anchor to its 0-based JSON Pointer.
func TestCharmPath(t *testing.T) {
	ctx := context.Background()
	argv := []string{"run", "ruff", "check", "."}
	if got, err := CharmPath(ctx, argv, "check"); err != nil || got != "/2" {
		t.Errorf("path(check) = %q, err %v; want /2", got, err)
	}
	if got, err := CharmPath(ctx, argv, "run"); err != nil || got != "/0" {
		t.Errorf("path(run) = %q, err %v; want /0", got, err)
	}
}
