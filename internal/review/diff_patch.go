package review

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/types/diff"
)

// PatchReader is the workspace's side of reading a patch: the working tree's, or a revision
// range's.
type PatchReader interface {
	WorkingDiff(ctx context.Context, paths []string) (string, error)
	RangeDiff(ctx context.Context, base, head string, paths []string) (string, error)
}

// ReadPatch returns the unified patch for this input.
func (in Source) ReadPatch(ctx context.Context, m PatchReader, paths []string) (string, string, error) {
	switch in.Kind {
	case SourceStdin:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", "", fmt.Errorf("magus diff: read stdin: %w", err)
		}
		return string(b), "stdin", nil
	case SourceFile:
		b, err := os.ReadFile(in.Path)
		if err != nil {
			return "", "", fmt.Errorf("magus diff: %w", err)
		}
		return string(b), in.Path, nil
	case SourceRevRange:
		// The BASE side, because every reader of types.Diff.Base labels it "compared against".
		// Returning the head made the prompt say a branch was compared against itself, which is
		// not a wrong-looking value a reader would question; it is a sentence that parses.
		p, err := m.RangeDiff(ctx, in.Base, in.Head, paths)
		if err != nil {
			return "", "", fmt.Errorf("magus diff: %w", err)
		}
		return p, in.Base, nil
	case SourceWorkingTree:
	}
	p, err := m.WorkingDiff(ctx, paths)
	return p, "working", err
}

// KeepFilteredHunks drops the parsed hunks rev no longer holds, so a viewer drawn from a filtered
// changeset shows only what the filter kept.
func KeepFilteredHunks(rev diff.Diff, parsed []changeset.FileHunks) []changeset.FileHunks {
	kept := map[string]bool{}
	for _, f := range rev.Files {
		for _, h := range f.Hunks {
			kept[diff.HunkRef{Path: f.Path, Index: h.Index}.Key()] = true
		}
	}
	out := make([]changeset.FileHunks, 0, len(parsed))
	for _, f := range parsed {
		var hunks []changeset.Hunk
		for _, h := range f.Hunks {
			if kept[diff.HunkRef{Path: f.Path, Index: h.Index}.Key()] {
				hunks = append(hunks, h)
			}
		}
		if len(hunks) > 0 {
			f.Hunks = hunks
			out = append(out, f)
		}
	}
	return out
}

// ChangedPathsFromPatch lists the files a patch touches, in patch order.
//
// It defers to the session parser rather than reading headers itself. This file used to carry
// its own copy, and the copy is how a GNU `diff -u` patch (no `diff --git` line anywhere)
// reported zero changed files and exited 0. Two readers of the same bytes will drift, and when
// they do the annotations describe different files than the hunks a reader is marking.
func ChangedPathsFromPatch(patch string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range changeset.ParseHunks(patch) {
		if f.Path != "" && !seen[f.Path] {
			seen[f.Path] = true
			out = append(out, f.Path)
		}
	}
	return out
}
