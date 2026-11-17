package commands

import (
	"context"
	"flag"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

// valueFlags is the set a binder with no flags of its own yields: the bootstrap pair.
var valueFlags = GlobalValueFlags(func(*flag.FlagSet) {})

func TestIsUsageOnlyInvocation(t *testing.T) {
	cases := []struct {
		name    string
		subArgs []string
		want    bool
	}{
		{"empty", nil, true},
		{"-h", []string{"-h"}, true},
		{"--help", []string{"--help"}, true},
		{"help", []string{"help"}, true},
		{"target", []string{"build"}, false},
		{"help after target", []string{"build", "-h"}, false},
		{"leading global flag", []string{"-v"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUsageOnlyInvocation(tc.subArgs); got != tc.want {
				t.Fatalf("IsUsageOnlyInvocation(%v) = %v, want %v", tc.subArgs, got, tc.want)
			}
		})
	}
}

// PeekSub reads GlobalValueFlags to know a token consumes the one after it. Both
// short forms were missing, so `magus -C /ws run build` read the PATH as the
// subcommand and reported an unknown command instead of running.
func TestPeekSubConsumesTheShortGlobalValues(t *testing.T) {
	for _, flag := range []string{"-C", "--C", "-c", "--c"} {
		t.Run(flag, func(t *testing.T) {
			if !valueFlags[flag] {
				t.Fatalf("GlobalValueFlags is missing %q, so PeekSub cannot skip its value", flag)
			}
			sub, subArgs := PeekSub([]string{flag, "value", "run", "build"}, valueFlags)
			if sub != "run" || !slices.Equal(subArgs, []string{"build"}) {
				t.Fatalf("PeekSub(%s value run build) = (%q, %v), want (\"run\", [build])", flag, sub, subArgs)
			}
		})
	}
}

// Only the verbs that seat their own steps drop the width a parent step seated this
// process at; any other verb is that step's tool, and the seat counted its work.
func TestSchedulesStepsNamesTheVerbsThatSeatTheirOwnSteps(t *testing.T) {
	for _, args := range [][]string{{"run", "ci", "."}, {"-v", "affected", "ci"}, {"-C", "/ws", "run", "test"}} {
		if !SchedulesSteps(args, valueFlags) {
			t.Errorf("SchedulesSteps(%v) = false, want true", args)
		}
	}
	for _, args := range [][]string{{"query", "run"}, {"describe", "job", "x"}, {}, {"--version"}} {
		if SchedulesSteps(args, valueFlags) {
			t.Errorf("SchedulesSteps(%v) = true, want false", args)
		}
	}
}

// TestPeekSubRecognizesVersion pins `magus --version`: PeekSub used to treat
// --version like any other dash-prefixed token (skip it, keep scanning for a
// subcommand), so `magus --version` alone found no subcommand, fell through to
// the main flag parse, and died on an unregistered flag ("flag parse failed")
// instead of printing the version: `magus version` worked, `magus --version`
// did not. -v is deliberately excluded: it is the verbosity flag (-v, -vv,
// -vvv), not a version request, so it must keep being skipped as an ordinary
// boolean flag here.
func TestPeekSubRecognizesVersion(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantSub     string
		wantSubArgs []string
	}{
		{"--version alone", []string{"--version"}, "version", nil},
		{"-version alone", []string{"-version"}, "version", nil},
		{"version the real subcommand still works", []string{"version"}, "version", nil},
		{"-v is verbosity, not version", []string{"-v"}, "", nil},
		{"--version after a real subcommand is that command's own flag", []string{"run", "build", "--version"}, "run", []string{"build", "--version"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, subArgs := PeekSub(tc.args, valueFlags)
			if sub != tc.wantSub || !slices.Equal(subArgs, tc.wantSubArgs) {
				t.Fatalf("PeekSub(%v) = (%q, %v), want (%q, %v)", tc.args, sub, subArgs, tc.wantSub, tc.wantSubArgs)
			}
		})
	}
}

func TestWantsUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"bare help flag", []string{"-h"}, true},
		{"long form", []string{"--help"}, true},
		{"help subverb", []string{"help"}, true},
		{"after a flag", []string{"--impact", "-h"}, true},
		{"no args is not a help request", nil, false},
		{"a real invocation", []string{"--impact"}, false},
		// Past "--" the tokens belong to a forwarded tool: this asks the test binary for
		// help, not magus.
		{"after a passthrough marker", []string{"go::go-test", ".", "--", "-h"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WantsUsage(tc.args); got != tc.want {
				t.Fatalf("WantsUsage(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// TestIsDeclaredRunRejectsEverythingButThreeBareTokens pins the shape half of the job-dispatch
// allowlist. dispatchJob exists so a browser-reachable RPC can never name an arbitrary command,
// and IsDeclaredRun is the one path that widens it beyond the fixed job argvs, so each rejection
// below is load-bearing, not defensive tidiness.
//
// The workspace half (the target must appear in the magusfile's target graph) needs a loaded
// workspace and is covered where one exists; every case here is refused before that lookup, which
// is why they hold even with no Magus on the context.
func TestIsDeclaredRunRejectsEverythingButThreeBareTokens(t *testing.T) {
	cases := map[string][]string{
		"a maintenance verb is not a run": {"clean", "--cache"},
		"a bare run names no project":     {"run", "test"},
		"a fourth token could be a flag":  {"run", "test", ".", "--charm=rw"},
		"a flag smuggled as the target":   {"run", "--exec=/bin/sh", "."},
		"a spell op form is not a target": {"run", "go::go-test", "."},
		"an empty argv is not a run":      {},
		// The workspace is what says a target exists, so a context carrying none can only
		// refuse. A server with no loaded workspace admits no run at all rather than guessing.
		"no workspace declares anything": {"run", "sh", "."},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			assert.False(t, IsDeclaredRun(context.Background(), argv, nil))
		})
	}
}

// The workspace half of the allowlist: the target must be one the magusfile declares for that
// project, never one the caller names.
func TestIsDeclaredRunAdmitsOnlyATargetTheWorkspaceDeclares(t *testing.T) {
	declared := func(_ context.Context, project string) []string {
		if project == "web" {
			return []string{"build", "test"}
		}
		return nil
	}

	assert.True(t, IsDeclaredRun(context.Background(), []string{"run", "test", "web"}, declared))
	assert.False(t, IsDeclaredRun(context.Background(), []string{"run", "deploy", "web"}, declared), "undeclared target")
	assert.False(t, IsDeclaredRun(context.Background(), []string{"run", "test", "api"}, declared), "undeclared project")
}

// Both peeks stop at the separator and read every spelling the flag package accepts.
func TestHasDetachAndBriefFlag(t *testing.T) {
	for _, spelling := range []string{"-detach", "--detach", "--detach=true"} {
		assert.True(t, HasDetachFlag([]string{"build", spelling}), spelling)
	}
	assert.False(t, HasDetachFlag([]string{"build", "--", "--detach"}), "past the separator the tokens are a tool's")
	assert.False(t, HasDetachFlag([]string{"build", "--detached"}))

	assert.True(t, HasBriefFlag([]string{"--brief"}))
	assert.False(t, HasBriefFlag([]string{"--", "--brief"}))
	assert.False(t, HasBriefFlag([]string{"ls"}))
}
