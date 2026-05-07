package types_test

import (
	"slices"
	"testing"

	"github.com/egladman/magus/types"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in         string
		wantName   string
		wantCharms []string
		wantErr    bool
	}{
		{in: "build", wantName: "build"}, // bare target
		{in: "lint:read", wantName: "lint", wantCharms: []string{"read"}},
		{in: "lint:read,strict", wantName: "lint", wantCharms: []string{"read", "strict"}},
		{in: "format:write", wantName: "format", wantCharms: []string{"write"}},
		{in: "api:build", wantName: "api", wantCharms: []string{"build"}}, // project is positional now; this is target=api charm=build
		{in: "lint:", wantErr: true},                                      // empty charm
		{in: "lint:read,", wantErr: true},                                 // empty charm in list
		{in: "web/studio:test", wantErr: true},                            // '/' not allowed in target
		{in: "go::lint", wantErr: true},                                   // '::' not a target char (spell filter stripped earlier)
		{in: "", wantErr: true},                                           // empty string
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := types.ParseTarget(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTarget(%q) = %+v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTarget(%q) unexpected error: %v", tc.in, err)
			}
			if got.Name != tc.wantName || !slices.Equal(got.Charms, tc.wantCharms) {
				t.Errorf("ParseTarget(%q) = {Name:%q Charms:%v}, want {Name:%q Charms:%v}",
					tc.in, got.Name, got.Charms, tc.wantName, tc.wantCharms)
			}
		})
	}
}

func TestValidateTargetName(t *testing.T) {
	valid := []string{"build", "test", "lint-fix", "gen_2", "ABC123", "a"}
	for _, n := range valid {
		if err := types.ValidateTargetName(n); err != nil {
			t.Errorf("ValidateTargetName(%q) = %v, want nil", n, err)
		}
	}
	invalid := []string{"", "lint:read", "go::lint", "foo@bar", "web/studio", "build prod", "test.unit"}
	for _, n := range invalid {
		if err := types.ValidateTargetName(n); err == nil {
			t.Errorf("ValidateTargetName(%q) = nil, want error", n)
		}
	}
}

func TestDefaultTargetNameNormalizer(t *testing.T) {
	norm := types.DefaultTargetNameNormalizer
	cases := []struct{ in, want string }{
		{"go_build", "go-build"},
		{"goBuild", "go-build"},
		{"go-build", "go-build"},
		{"build", "build"},
		{"image_build_static", "image-build-static"},
	}
	for _, tc := range cases {
		if got := norm.NormalizeTargetName(tc.in); got != tc.want {
			t.Errorf("NormalizeTargetName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateCharmName(t *testing.T) {
	for _, n := range []string{"read", "write", "strict", "ci-only", "x"} {
		if err := types.ValidateCharmName(n); err != nil {
			t.Errorf("ValidateCharmName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range []string{"", "read:write", "a b", "fast@v2"} {
		if err := types.ValidateCharmName(n); err == nil {
			t.Errorf("ValidateCharmName(%q) = nil, want error", n)
		}
	}
}
