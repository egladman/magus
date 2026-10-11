package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// A provider shells out to a foreign tool, and it runs on the LOAD PATH of every
// magus command: `magus ls`, completion, every run, every server refresh. Without a
// cache, wiring one would make `nx graph` (seconds, on a large repo) the floor for
// everything magus does. So the answer is remembered, and re-derived only when
// something that decides the project set changes.
//
// FOUR things decide it, and all four are in the fingerprint:
//
//   - the files the provider spell DECLARES as its inputs (mgs_listRequiredGlobs,
//     read at WORKSPACE scope rather than per project: for a provider that
//     declaration means "the files that decide what the projects are": nx.json,
//     **/project.json, pnpm-workspace.yaml);
//   - the glob list itself, so narrowing or renaming a declaration invalidates even
//     when it happens to match the same files;
//   - the workspace's OWN spell and magusfile sources, because a workspace-local
//     provider spell is edited constantly in this model and its list_projects body
//     is as load-bearing as any file it reads;
//   - the built-in spell registry's hash, which moves with the magus binary and so
//     covers a built-in provider's implementation changing under a cached answer.
//
// A provider whose globs are missing, malformed, or matching nothing cannot be
// cached: there is nothing to invalidate against, and a constant digest pins one
// answer for the life of the cache directory. Those cases run the provider every
// time and say so.
//
// This is not the build cache. It is one small file per provider, holding the
// workspace's shape rather than any target's output, and nothing replays it as an
// artifact.

// providerCacheVersion is the entry format, folded into the fingerprint rather than
// compared separately. A field added, retyped or RE-TAGGED in a later magus would
// otherwise decode silently zeroed against a fingerprint that still matched, a wrong
// project set that looks fresh. Bumping this makes every existing entry miss.
//
// v2: spells.ProvidedProject carries json tags. Before them the record encoded under
// its Go field names, so a v1 file would decode to empty projects under the tagged
// names, the exact silently-zeroed hit this constant exists to prevent.
const providerCacheVersion = 2

// ProviderCache says where a provider's answer is remembered between commands. A
// zero value (empty Dir) disables the cache and re-runs every provider.
type ProviderCache struct {
	Dir string
	// Immutable mirrors cache.immutable: the workspace may be read but never
	// written, so a miss re-runs the provider without storing the result.
	Immutable bool
}

// providerCacheEntry is one provider's remembered answer.
type providerCacheEntry struct {
	Fingerprint string                   `json:"fingerprint"`
	Projects    []spells.ProvidedProject `json:"projects"`
}

// providedProjects returns spellName's projects, from cache when the fingerprint
// still matches and by running the provider otherwise. A cache read or write failure
// is never fatal: the provider is the source of truth and re-running it is always
// correct, so an unreadable or unwritable cache costs time, not correctness.
func providedProjects(ctx context.Context, cache ProviderCache, root, spellName string) ([]spells.ProvidedProject, error) {
	fingerprint := ""
	if cache.Dir != "" {
		fingerprint = providerFingerprint(ctx, root, spellName)
		if fingerprint != "" {
			var entry providerCacheEntry
			if readProviderCache(providerCachePath(cache.Dir, spellName), &entry) && entry.Fingerprint == fingerprint {
				slog.With(attr.Component("magus")).DebugContext(ctx, "workspace provider replayed from cache",
					slog.String("provider", spellName), slog.String("fingerprint", fingerprint))
				return entry.Projects, nil
			}
		}
	}

	provided, err := providerRunner(ctx, spellName, root)
	if err != nil {
		return nil, err
	}
	if fingerprint == "" || cache.Immutable {
		return provided, nil
	}
	if len(provided) == 0 {
		// Reporting nothing is tolerated (the tool may not be installed yet) but must
		// never be REMEMBERED: installing it changes only node_modules and the like,
		// which the fingerprint walk prunes, so a cached empty answer would replay long
		// after the user fixed the thing that caused it.
		return provided, nil
	}
	// Re-fingerprint AFTER the run and store only if nothing moved. The provider gets
	// up to two minutes; a branch switch inside that window would otherwise file an
	// answer describing the new tree under the old tree's key, and the stale answer
	// would replay until something else changed.
	if after := providerFingerprint(ctx, root, spellName); after != fingerprint {
		slog.With(attr.Component("magus")).DebugContext(ctx, "workspace inputs changed while the provider ran; not caching this answer",
			slog.String("provider", spellName))
		return provided, nil
	}
	writeProviderCache(ctx, providerCachePath(cache.Dir, spellName), providerCacheEntry{Fingerprint: fingerprint, Projects: provided})
	return provided, nil
}

// writeProviderCache stores one provider's entry at path. A failure is logged and
// dropped: the provider is the source of truth, so a lost entry costs a re-run.
func writeProviderCache(ctx context.Context, path string, entry any) {
	if data, err := json.Marshal(entry); err != nil {
		slog.With(attr.Component("magus")).DebugContext(ctx, "provider cache not encodable", slog.String("path", path), attr.Error(err))
	} else if err := file.WriteFileAtomic(path, data, 0o644); err != nil {
		slog.With(attr.Component("magus")).DebugContext(ctx, "provider cache not writable", slog.String("path", path), attr.Error(err))
	}
}

// readProviderCache decodes the entry at path into entry, reporting false when there is
// none or it does not decode, which every caller treats as a miss.
func readProviderCache(path string, entry any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, entry) == nil
}

// providerDigest is the fingerprint every provider cache shares: the entry format, the
// root, the spell name, the built-in registry's hash, then the caller's own inputs, one
// line each. See providerFingerprint for why each of the first four is there.
func providerDigest(format int, root, spellName string, inputs ...[]string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strconv.Itoa(format) + "\n"))
	// The root, because cache.dir may be absolute and shared: two workspaces both
	// wiring a spell named "nx" would otherwise overwrite each other's entry on every
	// command and neither would ever hit.
	_, _ = h.Write([]byte(root + "\n"))
	// The spell name, because providerCachePath sanitizes it: "my/nx" and "my_nx" land on
	// the same file, and without this the two would agree on a fingerprint whenever their
	// inputs matched, so each would replay the other's answer.
	_, _ = h.Write([]byte(spellName + "\n"))
	_, _ = h.Write([]byte(spell.BuiltinsHash() + "\n"))
	for _, lines := range inputs {
		for _, l := range lines {
			_, _ = h.Write([]byte(l + "\n"))
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// providerOwnSourceGlobs are folded into every provider's fingerprint on top of what
// the spell declares: a workspace-local provider spell's own body decides the
// project set as surely as the files it reads, and nothing else in the fingerprint
// would notice an edit to it.
var providerOwnSourceGlobs = []string{"spells/**/*.buzz", "magusfile.buzz", "magusfiles/*.buzz"}

// providerFingerprint digests everything that decides spellName's project set, or
// returns "" when the answer cannot be cached (see the file comment). The empty
// return is the ONLY uncacheable signal: a caller must not fall back to a digest of
// nothing, which would be stable and therefore permanently fresh.
func providerFingerprint(ctx context.Context, root, spellName string) string {
	sp, ok := project.DefaultSpellRegistry().Lookup(spellName)
	if !ok {
		// The runner reports the unregistered spell properly a moment later; saying it
		// twice, in different words, would send the reader to the wrong fix.
		return ""
	}
	declared := sp.Sources()
	if len(declared) == 0 {
		slog.With(attr.Component("magus")).WarnContext(ctx, "workspace provider declares no mgs_listRequiredGlobs, so its project set cannot be cached and it runs on every command",
			slog.String("provider", spellName))
		return ""
	}
	globs := append(append([]string{}, declared...), providerOwnSourceGlobs...)
	sort.Strings(globs)
	for _, g := range globs {
		if _, err := doublestar.Match(g, "probe"); err != nil {
			slog.With(attr.Component("magus")).WarnContext(ctx, "workspace provider declares a malformed glob, so its project set cannot be cached",
				slog.String("provider", spellName), slog.String("glob", g), attr.Error(err))
			return ""
		}
	}

	lines := matchedFileIdentities(ctx, root, globs)
	if ctx.Err() != nil {
		return "" // a cancelled walk saw only part of the tree; its digest means nothing
	}
	if len(lines) == 0 {
		// Everything the provider declared is missing, or lives in a directory the walk
		// prunes (node_modules, gen, a dot-dir). Digesting that would be a constant, so
		// there would be no observable change that could ever invalidate the entry.
		slog.With(attr.Component("magus")).WarnContext(ctx, "workspace provider's declared inputs match no files, so its project set cannot be cached",
			slog.String("provider", spellName))
		return ""
	}

	return providerDigest(providerCacheVersion, root, spellName, globs, lines)
}

// matchedFileIdentities returns "<rel>\x00<size>\x00<mtime>" for every file under
// root matching globs, sorted. Content is deliberately not read: the point is to be
// much cheaper than the provider it guards, and a project-declaring file that changes
// without changing size or mtime is not a case worth paying for on every command.
//
// The walk prunes the same directories discovery does, which matters more here than
// anywhere: an nx provider's natural glob is **/project.json, and node_modules is
// full of them.
func matchedFileIdentities(ctx context.Context, root string, globs []string) []string {
	var lines []string
	prefix := strings.TrimSuffix(root, string(filepath.Separator)) + string(filepath.Separator)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err() // a full-tree walk on the load path must stay interruptible
		}
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry must not fail the open; the provider still runs
		}
		if d.IsDir() {
			if p != root && project.IsIgnoreDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel, ok := strings.CutPrefix(p, prefix)
		if !ok {
			return nil
		}
		rel = filepath.ToSlash(rel)
		for _, g := range globs {
			if matched, _ := doublestar.Match(g, rel); !matched {
				continue
			}
			// os.Stat, not d.Info(): WalkDir lstats, so a declaring file that is a
			// symlink (a shared nx.json linked into place) would otherwise fingerprint
			// the link, whose size and mtime never move when the target changes.
			info, serr := os.Stat(p)
			if serr != nil {
				return nil //nolint:nilerr // a vanished file re-runs the provider, it does not fail the open
			}
			lines = append(lines, rel+"\x00"+strconv.FormatInt(info.Size(), 10)+"\x00"+strconv.FormatInt(info.ModTime().UnixNano(), 10))
			return nil
		}
		return nil
	})
	sort.Strings(lines) // WalkDir order is stable, but sorting makes the digest independent of it
	return lines
}

// providerCachePath is where one provider's answer lives. The spell name is
// sanitized because it reaches here from a magusfile.
func providerCachePath(cacheDir, spellName string) string {
	safe := make([]rune, 0, len(spellName))
	for _, r := range spellName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			safe = append(safe, r)
		default:
			safe = append(safe, '_')
		}
	}
	return filepath.Join(cacheDir, "providers", string(safe)+".json")
}
