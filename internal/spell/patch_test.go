package spell

import (
	"encoding/json"
	"reflect"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

func TestApplyPatch(t *testing.T) {
	cases := []struct {
		name    string
		argv    []string
		ops     []PatchOp
		want    []string
		wantErr bool
	}{
		{"no ops", []string{"a", "b"}, nil, []string{"a", "b"}, false},
		{"append end", []string{"run", "./..."}, []PatchOp{{Op: "add", Path: "/-", Value: "-v"}}, []string{"run", "./...", "-v"}, false},
		{"prepend", []string{"build"}, []PatchOp{{Op: "add", Path: "/0", Value: "-x"}}, []string{"-x", "build"}, false},
		{"insert middle", []string{"run", "./..."}, []PatchOp{{Op: "add", Path: "/1", Value: "--fix"}}, []string{"run", "--fix", "./..."}, false},
		{"replace element", []string{"-l", "."}, []PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}, []string{"-w", "."}, false},
		{"remove element", []string{"mod", "tidy", "--diff"}, []PatchOp{{Op: "remove", Path: "/2"}}, []string{"mod", "tidy"}, false},
		{"two removes (rust fmt)", []string{"fmt", "--", "--check"}, []PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}, []string{"fmt"}, false},
		{"compose: insert then append", []string{"run", "./..."}, []PatchOp{{Op: "add", Path: "/1", Value: "--fix"}, {Op: "add", Path: "/-", Value: "-v"}}, []string{"run", "--fix", "./...", "-v"}, false},
		{"move", []string{"a", "b", "c"}, []PatchOp{{Op: "move", Path: "/0", From: "/2"}}, []string{"c", "a", "b"}, false},
		{"copy", []string{"a", "b"}, []PatchOp{{Op: "copy", Path: "/-", From: "/0"}}, []string{"a", "b", "a"}, false},
		{"test pass", []string{"go", "test"}, []PatchOp{{Op: "test", Path: "/0", Value: "go"}}, []string{"go", "test"}, false},
		{"test fail", []string{"go", "test"}, []PatchOp{{Op: "test", Path: "/0", Value: "rustc"}}, nil, true},
		{"index out of range", []string{"a"}, []PatchOp{{Op: "remove", Path: "/3"}}, nil, true},
		{"replace past end", []string{"a"}, []PatchOp{{Op: "replace", Path: "/1", Value: "x"}}, nil, true},
		{"add past end", []string{"a"}, []PatchOp{{Op: "add", Path: "/5", Value: "x"}}, nil, true},
		{"dash on remove invalid", []string{"a"}, []PatchOp{{Op: "remove", Path: "/-"}}, nil, true},
		{"leading zero invalid", []string{"a", "b"}, []PatchOp{{Op: "remove", Path: "/01"}}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyPatch(tc.argv, tc.ops)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ApplyPatch = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyPatch: unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ApplyPatch = %v, want %v", got, tc.want)
			}
		})
	}

	// Input must never be mutated.
	base := []string{"a", "b"}
	if _, err := ApplyPatch(base, []PatchOp{{Op: "add", Path: "/-", Value: "c"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base, []string{"a", "b"}) {
		t.Errorf("base mutated: %v", base)
	}
}

// TestApplyPatchConformance proves our flat-array applier agrees with the
// canonical RFC 6902 implementation (evanphx/json-patch) on the argv subset we
// use, so "follow the RFC" is verified, not just asserted.
func TestApplyPatchConformance(t *testing.T) {
	cases := []struct {
		argv []string
		ops  []PatchOp
	}{
		{[]string{"run", "./..."}, []PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		{[]string{"run", "./..."}, []PatchOp{{Op: "add", Path: "/1", Value: "--fix"}}},
		{[]string{"-l", "."}, []PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}},
		{[]string{"mod", "tidy", "--diff"}, []PatchOp{{Op: "remove", Path: "/2"}}},
		{[]string{"fmt", "--", "--check"}, []PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}},
		{[]string{"a", "b", "c"}, []PatchOp{{Op: "move", Path: "/0", From: "/2"}}},
		{[]string{"a", "b"}, []PatchOp{{Op: "copy", Path: "/-", From: "/0"}}},
		{[]string{"run", "./..."}, []PatchOp{{Op: "add", Path: "/1", Value: "--fix"}, {Op: "add", Path: "/-", Value: "-v"}}},
	}
	for i, tc := range cases {
		got, err := ApplyPatch(tc.argv, tc.ops)
		if err != nil {
			t.Fatalf("case %d: ApplyPatch: %v", i, err)
		}
		ref, err := applyWithEvanphx(tc.argv, tc.ops)
		if err != nil {
			t.Fatalf("case %d: evanphx: %v", i, err)
		}
		if !reflect.DeepEqual(got, ref) {
			t.Errorf("case %d: ApplyPatch = %v, evanphx = %v", i, got, ref)
		}
	}
}

// applyWithEvanphx runs ops over argv via the reference RFC 6902 library by
// marshalling argv to a JSON array document and the ops to a JSON Patch.
func applyWithEvanphx(argv []string, ops []PatchOp) ([]string, error) {
	doc, err := json.Marshal(argv)
	if err != nil {
		return nil, err
	}
	patchJSON, err := json.Marshal(ops)
	if err != nil {
		return nil, err
	}
	patch, err := jsonpatch.DecodePatch(patchJSON)
	if err != nil {
		return nil, err
	}
	out, err := patch.Apply(doc)
	if err != nil {
		return nil, err
	}
	var res []string
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res, nil
}
