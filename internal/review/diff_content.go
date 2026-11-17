package review

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/diff"
	typesvcs "github.com/egladman/magus/types/vcs"
)

// ContentSource is the workspace's side of resolving the bytes a receipt attests to.
type ContentSource interface {
	Root() string
	FileAt(ctx context.Context, rev, path string) (string, error)
	RevisionCheckpoint(ctx context.Context, rev string) (typesvcs.Checkpoint, error)
}

// Content resolves the bytes a receipt attests to, for whichever changeset source is
// being read.
//
// It exists because getting this wrong is silent and severe. Every receipt was minted from the
// WORKING TREE, which is correct for a working-tree review and a lie for a range one: it would
// stamp a colleague's file as read at the content of your own checkout, and Covers would then
// agree forever. Making the source an explicit value means a new minting site has to say which
// tree it means rather than inheriting the wrong default.
type Content struct {
	root string
	// at is zero for the working tree, and otherwise the revision the content is read from.
	at   typesvcs.Checkpoint
	read func(rev, path string) (string, error)
}

// Digest fingerprints one path, or returns "" where there is nothing to attest to.
//
// "" is the answer for a file that is absent: deleted in the working tree, or not present at the
// revision. Recording a receipt against it would satisfy Covers for every unreadable file forever.
func (c Content) Digest(path string) string {
	if c.at.Revision == "" {
		return DigestFile(filepath.Join(c.root, filepath.FromSlash(path)))
	}
	body, err := c.read(c.at.Revision, path)
	if err != nil {
		return ""
	}
	return Digest([]byte(body))
}

// ReadContent says which tree a receipt minted for this source should attest to.
//
// A source magus cannot address yields the working tree, which is what every caller did before
// this existed. That is safe only because the two gates that mint receipts (--ack and the viewer)
// both refuse an unaddressable source outright, so the fallback is unreachable rather than
// merely unlikely. If either ever accepts a patch on stdin, this has to refuse instead.
func ReadContent(ctx context.Context, m ContentSource, src Source) Content {
	c := Content{
		root: m.Root(),
		read: func(rev, path string) (string, error) { return m.FileAt(ctx, rev, path) },
	}
	if src.Kind != SourceRevRange {
		return c
	}
	// A range magus cannot resolve leaves at zero, and the caller then digests the working tree
	// for a review of somebody else's branch. Refused rather than degraded: --ack has already
	// checked the range resolves, so reaching here means the repository moved mid-command.
	at, err := m.RevisionCheckpoint(ctx, src.Head)
	if err != nil {
		return c
	}
	c.at = at
	return c
}

// AckChangeset records a receipt for every non-generated changed file at the content the reader
// was shown, carrying the reason the caller gave for covering them all at once.
func AckChangeset(content Content, cacheDir string, rev diff.Diff, reason string, now time.Time) (int, error) {
	var add []Receipt
	for _, f := range rev.Files {
		if f.Generated() {
			continue
		}
		digest := content.Digest(f.Path)
		if digest == "" {
			continue
		}
		add = append(add, Receipt{
			Path: f.Path, Digest: digest, At: now, Source: content.at, Reason: reason,
		})
	}
	if len(add) == 0 {
		return 0, nil
	}
	return len(add), Record(cacheDir, add)
}

// ScopeAck narrows a changeset to the paths the caller named, so a reader can record the
// three files they just read in their editor without claiming the other thirty.
//
// An unnamed path is an ERROR rather than a silent no-op. The whole value of a receipt is
// that it names something real; a typo that quietly acknowledged nothing would leave the
// reader believing they had recorded work they had not.
func ScopeAck(rev diff.Diff, paths []string) (diff.Diff, error) {
	if len(paths) == 0 {
		return rev, nil
	}
	inChange := make(map[string]bool, len(rev.Files))
	for _, f := range rev.Files {
		inChange[f.Path] = true
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "./")
		if !inChange[clean] {
			return diff.Diff{}, exit.Usagef("magus diff --ack: %q is not a changed file in this changeset; `%s` lists them", p, hint.Diff.With("-o", "name"))
		}
		want[clean] = true
	}
	out := diff.Diff{Base: rev.Base}
	for _, f := range rev.Files {
		if want[f.Path] {
			out.Files = append(out.Files, f)
		}
	}
	return out, nil
}

// BulkReasons is every distinct reason a bulk ack recorded against a file in this
// changeset, in first-seen order.
func BulkReasons(cacheDir string, rev diff.Diff) []string {
	store, err := Load(cacheDir)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range rev.Files {
		r, ok := store[f.Path]
		if !ok || r.Reason == "" || seen[r.Reason] {
			continue
		}
		seen[r.Reason] = true
		out = append(out, r.Reason)
	}
	return out
}

// RequiredMatcher reports whether a workspace-relative path sits inside any project's
// declared review_required globs.
//
// Globs are matched against the path relative to the DECLARING project, so a project names
// its own files the same way its sources and outputs do rather than having to spell the
// workspace prefix that its magusfile already sits under.
//
// nil when no project declares any, which the caller treats as "single nothing out" rather
// than as "everything matters".
func RequiredMatcher(ws types.WorkspaceReader) func(string) bool {
	type scope struct {
		dir   string
		globs []string
	}
	var scopes []scope
	for _, p := range ws.All() {
		if len(p.ReviewRequired) > 0 {
			scopes = append(scopes, scope{dir: p.Path, globs: p.ReviewRequired})
		}
	}
	if len(scopes) == 0 {
		return nil
	}
	return func(path string) bool {
		for _, s := range scopes {
			rel := path
			if s.dir != "" && s.dir != "." {
				if !strings.HasPrefix(path, s.dir+"/") {
					continue
				}
				rel = strings.TrimPrefix(path, s.dir+"/")
			}
			for _, g := range s.globs {
				if ok, err := doublestar.Match(g, rel); err == nil && ok {
					return true
				}
			}
		}
		return false
	}
}

// Checkpoint is the revision the content is read from, zero for the working tree. It is what a
// receipt minted from this content records as its source.
func (c Content) Checkpoint() typesvcs.Checkpoint { return c.at }
