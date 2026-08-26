package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsDeclaredRunRejectsEverythingButThreeBareTokens pins the shape half of the job-dispatch
// allowlist. dispatchJob exists so a browser-reachable RPC can never name an arbitrary command,
// and isDeclaredRun is the one path that widens it beyond the fixed job argvs - so each rejection
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
		// refuse. A daemon with no loaded workspace admits no run at all rather than guessing.
		"no workspace declares anything": {"run", "sh", "."},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			assert.False(t, isDeclaredRun(context.Background(), argv))
		})
	}
}
