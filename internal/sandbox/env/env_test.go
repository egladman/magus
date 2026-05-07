package env

import (
	"slices"
	"testing"
)

// TestValidateGlobs_RejectsBareWildcard ensures a bare "*" is rejected: it has
// an empty prefix and would otherwise match every variable name, passing the
// entire environment (secrets included) through Scrub.
func TestValidateGlobs_RejectsBareWildcard(t *testing.T) {
	cases := []struct {
		name      string
		globs     []string
		wantFirst string // first offending pattern, "" if all valid
	}{
		{"bare wildcard", []string{"*"}, "*"},
		{"bare wildcard among valid", []string{"MISE_*", "*"}, "*"},
		{"valid prefix glob", []string{"MISE_*"}, ""},
		{"valid single-char prefix", []string{"M*"}, ""},
		{"interior wildcard", []string{"A*B*"}, "A*B*"},
		{"no wildcard", []string{"PATH"}, "PATH"},
		{"empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidateGlobs(tc.globs); got != tc.wantFirst {
				t.Errorf("ValidateGlobs(%v) = %q, want %q", tc.globs, got, tc.wantFirst)
			}
		})
	}
}

// TestScrub_BareWildcardDoesNotLeakEnv is the defence-in-depth check: even if a
// bare "*" glob slips past ValidateGlobs, matchGlobs must not treat it as
// matching everything — that would defeat the secret-stripping allowlist.
func TestScrub_BareWildcardDoesNotLeakEnv(t *testing.T) {
	a := Allowlist{Allow: []string{"PATH"}, Globs: []string{"*"}}
	env := []string{
		"PATH=/usr/bin",
		"AWS_SECRET_ACCESS_KEY=topsecret",
		"GITHUB_TOKEN=ghp_xxx",
	}
	kept, dropped := a.Scrub(env)

	if !slices.Contains(kept, "PATH=/usr/bin") {
		t.Errorf("PATH should be kept (exact allow); kept=%v", kept)
	}
	for _, kv := range kept {
		if kv == "AWS_SECRET_ACCESS_KEY=topsecret" || kv == "GITHUB_TOKEN=ghp_xxx" {
			t.Errorf("bare-wildcard glob leaked secret %q through Scrub", kv)
		}
	}
	if !slices.Contains(dropped, "AWS_SECRET_ACCESS_KEY") || !slices.Contains(dropped, "GITHUB_TOKEN") {
		t.Errorf("secrets should be dropped; dropped=%v", dropped)
	}
}

// TestScrub_ValidGlobStillMatches confirms the fix does not break legitimate
// prefix globs.
func TestScrub_ValidGlobStillMatches(t *testing.T) {
	a := Allowlist{Globs: []string{"MISE_*"}}
	kept, _ := a.Scrub([]string{"MISE_DATA_DIR=/x", "AWS_SECRET=y"})
	if !slices.Contains(kept, "MISE_DATA_DIR=/x") {
		t.Errorf("MISE_* glob should keep MISE_DATA_DIR; kept=%v", kept)
	}
	if slices.Contains(kept, "AWS_SECRET=y") {
		t.Errorf("MISE_* glob should not keep AWS_SECRET; kept=%v", kept)
	}
}
