// Package spells holds this test only; the directory is otherwise a tree of
// built-in spell sources and their doc examples, not a Go package.
package spells

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	pg "github.com/egladman/magus/internal/playground"
)

// TestExamplesParseAndRecord walks every spells/examples/**/*.buzz file and
// asserts two things the spell docs rely on:
//
//  1. It parses under ParseEmbedded (the mode magusfiles and the playground use),
//     catching a syntax typo before it ships in a docs page. Mirrors
//     std/examples_test.go.
//  2. It records at least one host op under the recording evaluator
//     (playground.EvalBuzz with WithRecorder). This is the load-bearing guarantee for
//     the Run button: an example must actually invoke its op inside a target, or
//     the dry-run trace is empty and the button shows nothing. Catches an example
//     that wires a spell but forgets to call it.
func TestExamplesParseAndRecord(t *testing.T) {
	root := "examples"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("no spells/examples/ directory yet")
	}

	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".buzz") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", path, err)
			return nil
		}
		if _, err := buzz.ParseEmbedded(string(src)); err != nil {
			t.Errorf("%s: parse: %v", path, err)
			return nil
		}
		r := pg.EvalBuzz(context.Background(), string(src), pg.WithRecorder())
		if !r.OK {
			t.Errorf("%s: recorder eval failed: %v", path, r.Diag)
			return nil
		}
		if len(r.Trace) == 0 {
			t.Errorf("%s: recorder captured no ops; the example must call its op inside a target", path)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	t.Logf("checked %d spell example file(s)", count)
}
