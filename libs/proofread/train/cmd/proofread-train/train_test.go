package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestTrainRefusesBeforeFetching(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"train", "-model", "m.gz"}, "proofread-train: train needs both a model path and a test split path\n"},
		{[]string{"train", "-model", "m.gz", "-testdata", "t.gz", "-iterations", "0"}, "proofread-train: train needs at least 1 iteration and 1 level, got 0 and 1024\n"},
		{[]string{"train", "-model", "m.gz", "-testdata", "t.gz", "extra"}, "proofread-train: train takes no arguments\n"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(tc.args, strings.NewReader(""), &stdout, &stderr); code != exitError || stderr.String() != tc.want {
			t.Errorf("run(%q): exit %d, stderr %q; want exit 2 and %q", tc.args, code, stderr.String(), tc.want)
		}
	}
}
