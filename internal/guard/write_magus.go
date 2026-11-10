package guard

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/xdg"
)

// denyOwnBinaryWrite refuses a lease's write to any checkout's own ./magus,
// the file the hooks run by name to judge the lease's own calls.
func denyOwnBinaryWrite(lease, writePath string) writeGrade {
	if lease == "" || !filepath.IsAbs(writePath) {
		return writeGrade{}
	}
	for _, p := range []string{filepath.Clean(writePath), writeDestination(writePath)} {
		dir := filepath.Dir(p)
		if filepath.Base(p) != "magus" || findMagusCheckout(dir) != dir {
			continue
		}
		return writeGrade{Decision: decisionDeny, Reason: denial{
			Say: fmt.Sprintf("magus workspace: %s is a checkout's own magus binary; lease %s runs the one placed for it.", writePath, lease),
			Why: fmt.Sprintf("Only the workspace's bootstrap and build targets write that file. The hooks and the guard run it by name, so a lease that writes it picks the guard its own calls are judged by. `%s` shows what the job holds.", hint.DescribeJob.With(lease)),
		}.full()}
	}
	return writeGrade{}
}

// denyMagusStateWrite refuses a lease's write into magus's own state,
// cache or config directory. The job store there holds each lease's write
// paths, so a worker writing a row widens its own boundary. Scratch
// checkouts are denyOtherCheckout's.
func denyMagusStateWrite(lease, writePath string) writeGrade {
	if lease == "" || !filepath.IsAbs(writePath) {
		return writeGrade{}
	}
	dest := writeDestination(writePath)
	if scratch, err := scratchCheckouts(); err == nil && within(dest, scratch) {
		return writeGrade{}
	}
	for _, d := range magusUserDirs() {
		if !within(dest, d.dir) {
			continue
		}
		return writeGrade{Decision: decisionDeny, Reason: denial{
			Say: fmt.Sprintf("magus workspace: %s is in magus's own %s directory (%s); record a result with `%s`.",
				writePath, d.kind, d.dir, hint.JobExit.With(lease, "--stdin")),
			Why: fmt.Sprintf("Only magus commands write that directory. It holds the job store and the other records every verdict is computed from, lease %s's write paths included, so a write there by hand rewrites the boundary it is graded against. `%s` shows what the job holds.",
				lease, hint.DescribeJob.With(lease)),
		}.full()}
	}
	return writeGrade{}
}

// magusUserDir is one of magus's own user directories and what it holds.
type magusUserDir struct {
	kind string
	dir  string
}

// magusUserDirs are magus's user state, cache and config directories, as
// the job store, shared cache and user-global magus.yaml locate them.
func magusUserDirs() []magusUserDir {
	var out []magusUserDir
	for _, base := range []struct {
		kind string
		dir  func() (string, error)
	}{{"state", xdg.StateDir}, {"cache", xdg.CacheDir}, {"config", xdg.ConfigDir}} {
		if dir, err := base.dir(); err == nil {
			out = append(out, magusUserDir{kind: base.kind, dir: filepath.Join(dir, "magus")})
		}
	}
	return out
}

// writeDestination is the file a write to p lands in, symlinks resolved.
// A dangling final link is followed too: writing through it creates its
// target.
func writeDestination(p string) string {
	for range 40 {
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		link, err := os.Readlink(p)
		if err != nil {
			break
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(p), link)
		}
		p = filepath.Clean(link)
	}
	return realPath(p)
}
