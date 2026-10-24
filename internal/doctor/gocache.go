package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// goCacheWalkEntries bounds how many directory entries one cache walk visits. A build cache
// can hold millions of files, and doctor must stay quick: the walk also stops the moment it
// passes the budget, so the cap only bites on a cache that is under budget and still huge.
const goCacheWalkEntries = 500_000

const (
	goBuildCacheFix = "magus run go::go-clean . -- -cache"
	lintCacheFix    = "golangci-lint cache clean"
)

// goCache is one cache directory to measure and the command that frees it.
type goCache struct {
	label string
	dir   string
	fix   string
}

// CacheSize is what a bounded walk learned about a directory.
type cacheSize struct {
	Bytes int64
	// Stopped is why the walk ended before the tree did: "" when it covered everything.
	Stopped string
}

// MeasureCache sums file sizes under dir until the total passes limit or maxEntries entries
// have been visited. A missing dir measures zero.
func MeasureCache(ctx context.Context, dir string, limit int64, maxEntries int) (cacheSize, error) {
	var out cacheSize
	entries := 0
	errStop := errors.New("walk stopped")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir && errors.Is(err, fs.ErrNotExist) {
				return errStop
			}
			// A file evicted mid-walk is the cache working, not a failure to report.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		entries++
		if !d.IsDir() {
			info, ierr := d.Info()
			if ierr == nil {
				out.Bytes += info.Size()
			}
		}
		switch {
		case out.Bytes > limit:
			out.Stopped = "past the budget"
			return errStop
		case entries >= maxEntries:
			out.Stopped = fmt.Sprintf("after %d entries", maxEntries)
			return errStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return out, err
	}
	return out, nil
}

// goCacheBudget grades caches against budgetMB. Over budget is advice, never a failure, and
// nothing is deleted: the message names the command that frees each cache.
func goCacheBudget(ctx context.Context, caches []goCache, budgetMB, maxEntries int) types.Check {
	const name = "go-cache-budget"
	limit := int64(budgetMB) << 20
	var over, partial, unread []string
	for _, c := range caches {
		size, err := MeasureCache(ctx, c.dir, limit, maxEntries)
		switch {
		case err != nil:
			unread = append(unread, fmt.Sprintf("%s %s: cannot measure: %v", c.label, c.dir, err))
		case size.Bytes > limit:
			over = append(over, fmt.Sprintf("%s %s: at least %d MB, past the %d MB budget; free it with `%s`",
				c.label, c.dir, size.Bytes>>20, budgetMB, c.fix))
		case size.Stopped != "":
			partial = append(partial, fmt.Sprintf("%s %s: %d MB measured, stopped %s; the rest is unmeasured",
				c.label, c.dir, size.Bytes>>20, size.Stopped))
		}
	}
	if len(over)+len(partial)+len(unread) == 0 {
		return types.Check{Name: name, Status: types.CheckOK,
			Message: fmt.Sprintf("%d cache(s) within the %d MB go.cache_budget_mb", len(caches), budgetMB)}
	}
	details := append(append(over, partial...), unread...)
	return types.Check{
		Name:   name,
		Status: types.CheckAdvice,
		Message: fmt.Sprintf("%d of %d cache(s) past or unverifiable against the %d MB go.cache_budget_mb; doctor deletes nothing (the daemon trims the Go build cache past the budget)",
			len(details), len(caches), budgetMB),
		Details: details,
	}
}

// checkGoCacheBudget measures the Go build cache and the golangci-lint cache against
// go.cache_budget_mb.
func (r *runner) checkGoCacheBudget() types.Check {
	const name = "go-cache-budget"
	budget := r.opts.cfg.Go.CacheBudgetMB
	unscoped := lintCacheUnscoped(r.root)
	if budget <= 0 {
		if unscoped != "" {
			return types.Check{Name: name, Status: types.CheckAdvice, Message: unscoped, Details: []string{unscoped}}
		}
		return types.Check{Name: name, Status: types.CheckOK, Message: "go.cache_budget_mb is 0: the Go caches are not measured and the daemon trims nothing"}
	}
	var caches []goCache
	var notes []string
	if unscoped != "" {
		notes = append(notes, unscoped)
	}
	if dir, err := GoEnvCache(r.ctx); err != nil {
		notes = append(notes, fmt.Sprintf("go build cache not measured: %v", err))
	} else {
		caches = append(caches, goCache{label: "go build cache", dir: dir, fix: goBuildCacheFix})
	}
	if dir, err := lintCacheDir(); err != nil {
		notes = append(notes, fmt.Sprintf("golangci-lint cache not measured: %v", err))
	} else {
		caches = append(caches, goCache{label: "golangci-lint cache", dir: dir, fix: lintCacheFix})
	}
	// The go spell points golangci-lint at the checkout's own cache, so that is the one a
	// magus run fills; the user cache above grows only from runs outside magus.
	if r.root != "" {
		own := filepath.Join(r.root, ".magus", "golangci-lint")
		caches = append(caches, goCache{label: "checkout golangci-lint cache", dir: own,
			fix: fmt.Sprintf("%s=%s %s", lintCacheEnv, own, lintCacheFix)})
	}
	res := goCacheBudget(r.ctx, caches, budget, goCacheWalkEntries)
	if len(notes) > 0 {
		res.Details = append(res.Details, notes...)
		res.Message += "; " + strings.Join(notes, "; ")
		if res.Status == types.CheckOK {
			res.Status = types.CheckAdvice
		}
	}
	return res
}

// GoEnvCache asks the Go toolchain where its build cache lives.
func GoEnvCache(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "env", "GOCACHE").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOCACHE: %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" || dir == "off" {
		return "", fmt.Errorf("go env GOCACHE is %q", dir)
	}
	return dir, nil
}

// lintCacheUnscoped is the MGS2014 finding when the effective golangci-lint cache lies
// outside the checkout at root, "" when it is inside or cannot be resolved. It reports the
// location, not a count of paths from other checkouts: telling whose a cache entry is
// means reading each one, which doctor does not do.
func lintCacheUnscoped(root string) string {
	dir, err := lintCacheDir()
	if root == "" || err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, dir)
	if err == nil && filepath.IsLocal(rel) {
		return ""
	}
	source := lintCacheEnv + "=" + dir
	if os.Getenv(lintCacheEnv) == "" {
		source = fmt.Sprintf("%s is unset, so it is the shared user cache %s", lintCacheEnv, dir)
	}
	return fmt.Sprintf("[%s] the golangci-lint cache is outside the checkout %s (%s), so results from other checkouts can answer for this one's; `magus run golangci-lint <project>` keeps it in %s",
		types.ToolCacheUnscoped, root, source, filepath.Join(root, ".magus", "golangci-lint"))
}

const lintCacheEnv = "GOLANGCI_LINT_CACHE"

// lintCacheDir is where golangci-lint keeps its cache: GOLANGCI_LINT_CACHE, else the
// user cache directory's golangci-lint folder. Both are the tool's own rule, and reading
// them avoids running `golangci-lint cache status`, which walks the whole cache itself.
func lintCacheDir() (string, error) {
	if dir := os.Getenv("GOLANGCI_LINT_CACHE"); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "golangci-lint"), nil
}
