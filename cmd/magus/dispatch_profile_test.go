package main

import "testing"

// TestResolveProfileRunAffectedUsageSkipsForward pins the fix for the silent
// `run -h` / `affected -h` / bare `affected` bug: a usage-only invocation of an
// adoptable subcommand must NOT forward to the daemon (which would print usage on the
// daemon's stderr and hand the caller a bare non-zero exit). It runs locally with
// only config loaded, so the per-subcommand usage reaches the caller's stderr.
func TestResolveProfileRunAffectedUsageSkipsForward(t *testing.T) {
	usageOnly := dispatchProfile{needsConfig: true}
	full := dispatchProfile{needsConfig: true, needsDaemonFwd: true, needsWorkspace: true}

	cases := []struct {
		name    string
		sub     string
		subArgs []string
		want    dispatchProfile
	}{
		{"run bare", "run", nil, usageOnly},
		{"run -h", "run", []string{"-h"}, usageOnly},
		{"run --help", "run", []string{"--help"}, usageOnly},
		{"run help", "run", []string{"help"}, usageOnly},
		{"run target still forwards", "run", []string{"build"}, full},
		{"run flag-then-target still forwards", "run", []string{"-v", "build"}, full},
		{"affected bare", "affected", nil, usageOnly},
		{"affected -h", "affected", []string{"-h"}, usageOnly},
		{"affected --help", "affected", []string{"--help"}, usageOnly},
		{"affected target still forwards", "affected", []string{"ci"}, full},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveProfile(tc.sub, tc.subArgs)
			if got != tc.want {
				t.Fatalf("resolveProfile(%q, %v) = %+v, want %+v", tc.sub, tc.subArgs, got, tc.want)
			}
		})
	}
}

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
			if got := isUsageOnlyInvocation(tc.subArgs); got != tc.want {
				t.Fatalf("isUsageOnlyInvocation(%v) = %v, want %v", tc.subArgs, got, tc.want)
			}
		})
	}
}
