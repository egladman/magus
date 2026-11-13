package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// releases carries the row's recorded releases forward and adds the paths this write
// gave up, the ones prev owned and next does not.
//
// A path that is owned again drops OUT of the list: a row saying it both owns and has
// released the same path tells a reader nothing they can act on. A path released twice
// keeps its position and takes the NEWER digest, because the version the next agent
// inherits is the one left behind last. revoked marks what this write gave up as taken from
// its holder rather than given up by it.
func (s *Store) releases(ctx context.Context, prev, next types.Job, now int64, revoked bool) []types.JobRelease {
	refork := prev.State.Terminal() && next.State.Live()
	out := slices.DeleteFunc(slices.Clone(prev.Releases), func(r types.JobRelease) bool {
		switch {
		// A narrowed release outlives its entry; a re-fork ends it.
		case r.Narrowed && refork:
			return true
		case r.Narrowed && slices.Contains(prev.Paths.Write, r.Path):
			return false
		}
		return slices.Contains(next.Paths.Write, r.Path)
	})
	for _, p := range prev.Paths.Write {
		if slices.Contains(next.Paths.Write, p) || slices.Contains(prev.Paths.Soft, p) {
			continue
		}
		rel := types.JobRelease{Path: p, Digest: s.digest(ctx, p), ReleasedAt: now, Revoked: revoked}
		if at := slices.IndexFunc(out, func(r types.JobRelease) bool { return r.Path == p }); at >= 0 {
			out[at] = rel
			continue
		}
		out = append(out, rel)
	}
	return out
}

// ReleasedUnderPattern says of each file row released that a pattern of its
// write paths still names: the release stands, so the pattern does not hold it.
func ReleasedUnderPattern(row types.Job) []string {
	var notes []string
	for _, r := range row.Releases {
		file, decl := types.SplitClaim(r.Path)
		if decl != "" || !heldByPattern(row, file) {
			continue
		}
		if claims := claimsOnFile(row, file); len(claims) > 0 {
			notes = append(notes, fmt.Sprintf("%s stays released, so the pattern naming it does not hold it: only its claims do (%s)", file, strings.Join(claims, ", ")))
			continue
		}
		notes = append(notes, fmt.Sprintf("%s stays released, so the pattern naming it does not hold it and neither does a claim: this job cannot write it until paths.write name `%s#<declaration>`", file, file))
	}
	return notes
}

// digest identifies the content at a released path, resolved inside the workspace.
// Computed here rather than passed in: a worker announcing a release should not have to
// hash anything, and a digest the releaser supplied would describe the tree it believed
// it left rather than the one it did.
//
// Everything that is not a readable file inside the root answers with one of the three
// documented markers rather than a hash; types.DigestAbsent, types.DigestDir and
// types.DigestUnreadable say which. The absent/unreadable split matters to the next
// agent: "the releaser deleted it" and "something is there nobody could read" are
// different problems. A path escaping the root is absent by the same rule: the ledger
// describes this workspace, so a row is never handed a digest of something outside it.
//
// Runs under the store's mutex, which is deliberate (reading the previous owned set and
// recording what it gave up has to be one step), and is why every branch below is bounded.
//
// A released declaration (`run.go#executeStages`) digests the lines the declaration spans
// in the file now, as the footprint places them, so the next holder inherits that body
// rather than a file other claims on it keep changing. A declaration no line is placed in
// any more is absent.
func (s *Store) digest(ctx context.Context, declared string) string {
	p, decl := types.SplitClaim(declared)
	if decl != "" {
		return s.declarationDigest(ctx, p, decl)
	}
	return s.fileDigest(ctx, p)
}

// fileDigest is digest for a path SplitClaim already cut.
func (s *Store) fileDigest(ctx context.Context, declared string) string {
	if s.root == "" {
		return types.DigestAbsent
	}
	if ctx.Err() != nil {
		return types.DigestUnreadable
	}
	// Symlinks are resolved on BOTH sides before the containment check. A link inside
	// the root pointing outside it was followed and hashed, which is the one thing this
	// function promises not to do; and the root is resolved too, or a workspace reached
	// through a symlinked parent (/tmp on macOS) would read as an escape from itself.
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return types.DigestAbsent
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(declared)))
	if err != nil {
		// Nothing resolves at the path: a deleted file, a broken link, or a declared
		// glob, which is a pattern rather than a path.
		return types.DigestAbsent
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return types.DigestAbsent
	}
	info, err := os.Stat(full)
	switch {
	case err != nil:
		return types.DigestUnreadable
	case info.IsDir():
		return types.DigestDir
	case !info.Mode().IsRegular():
		// A fifo, socket, or device. os.Open on a released named pipe BLOCKS until
		// somebody writes to it, and it would block holding the store's mutex: one
		// released fifo would wedge every ledger operation in the server.
		return types.DigestUnreadable
	case info.Size() > maxDigestBytes:
		return types.DigestUnreadable
	}
	fh, err := os.Open(full)
	if err != nil {
		return types.DigestUnreadable
	}
	defer fh.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, fh); err != nil {
		return types.DigestUnreadable
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// declarationDigest is digest for one declaration of the file at p. The file passes every
// check digest makes of a whole file before a line of it is placed.
func (s *Store) declarationDigest(ctx context.Context, p, decl string) string {
	if whole := s.fileDigest(ctx, p); !strings.HasPrefix(whole, "sha256:") {
		return whole
	}
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return types.DigestUnreadable
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return types.DigestUnreadable
	}
	res, err := vcs.Resolve(ctx, s.root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return types.DigestUnreadable
	}
	regions, err := res.VCS.RegionsBetween(ctx, s.root, path.Clean(p), nil, body)
	if err != nil {
		return types.DigestUnreadable
	}
	lines := strings.SplitAfter(string(body), "\n")
	var spanned strings.Builder
	found := false
	for _, r := range regions {
		if !types.NamesDeclaration(decl, r.Declaration) {
			continue
		}
		found = true
		for n := r.Lines[0]; n <= r.Lines[1] && n-1 < len(lines); n++ {
			spanned.WriteString(lines[n-1])
		}
	}
	if !found {
		return types.DigestAbsent
	}
	sum := sha256.Sum256([]byte(spanned.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// maxDigestBytes bounds one release digest, because the hash is computed while the store
// holds its mutex. 32 MiB is far above the source files a lease actually releases and far
// below the build artifact that would otherwise stall every other ledger caller for as
// long as it takes to read it; a path over the cap records unreadable, which is what it
// is from the reader's side.
const maxDigestBytes = 32 << 20
