package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRefusesAMissingOrUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"voice"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &stdout, &stderr); code != exitError {
			t.Errorf("run(%q): exit %d, want %d", args, code, exitError)
		}

		if !strings.Contains(stderr.String(), "usage: proofread-train") {
			t.Errorf("run(%q): stderr %q, want the usage", args, stderr.String())
		}
	}
}
