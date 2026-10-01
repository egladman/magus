package magus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/spells"
)

// A version probe's answer is a function of its inputs: the binary that runs, the files
// that binary reads to decide which version it is, and the environment. probeCacheKey
// fingerprints all of them, so a hit replays the answer without a fork and any change
// misses. A tool whose inputs cannot be enumerated with confidence is never cached.
//
// This is a second content-addressed store beside internal/cache, deliberately: that one
// keys a cache.Step (sources, tools, charms, deps, ...) to a manifest describing a whole
// target run, with replay, remote push/pull, locking and GC built for that shape. A probe
// answer is one short string keyed by one hash, looked up before ANY step exists to key
// against, so building a Step just to hold it would be a heavier write path for the
// common case (a plain os.ReadFile) than the read it exists to save.

// probeInputs says, per probe argv, what beyond the binary decides the answer. Only these
// probes are cached: rustup proxies through rust-toolchain files and its own settings,
// and a script wrapper can read anything, so neither has an input list that could be
// trusted.
var probeInputs = map[string]probeRecipe{
	"node --version": {},
	// pnpm switches to the version a manifest pins (manage-package-manager-versions),
	// configured from any of these.
	"pnpm --version": {files: pnpmInputs},
	// `pnpm exec` runs the nearest node_modules/.bin/tsc and falls back to PATH. pnpm
	// links node_modules/typescript to a store path that names the version, so the link
	// moves with an upgrade; package.json is read through it for an npm-style install.
	// The lockfile and the stamps pnpm writes when an install finishes make installing
	// into a tree with no node_modules a change of inputs.
	"pnpm exec tsc --version": {
		files: append(slices.Clone(pnpmInputs), "pnpm-lock.yaml",
			"node_modules/.modules.yaml", "node_modules/.pnpm/lock.yaml",
			"node_modules/.bin/tsc", "node_modules/typescript", "node_modules/typescript/package.json"),
		pathBins: []string{"tsc"},
		execs:    "tsc",
	},
	"go version":              {goToolchain: true},
	"golangci-lint --version": {},
	"buf --version":           {},
	// The symbol indexers' observe probes. govulncheck's is absent on purpose: it reports
	// a vulnerability database that moves on a clock, not on any input listed here.
	"scip-go --version":         {},
	"scip-typescript --version": {},
	"scip-python --version":     {},
}

var pnpmInputs = []string{"package.json", "pnpm-workspace.yaml", ".npmrc"}

// probeRecipe is what one probe's answer depends on besides the binary it runs and the
// variables probeEnvPrefixes names.
type probeRecipe struct {
	// files are read in the probe's directory and every ancestor.
	files []string
	// pathBins are other binaries the probe may run, found on PATH.
	pathBins []string
	// goToolchain marks go's toolchain switch: the go and toolchain lines of the go.work
	// and go.mod it would read, the GOENV file, and the variables in goEnvInputs.
	goToolchain bool
	// execs is the binary the probe reaches through a package manager's exec: found in
	// node_modules/.bin of the directory or an ancestor, then on PATH. Its absence is
	// what makes a failed probe an absent tool rather than a broken one.
	execs string
}

// execPresent reports whether a package manager's exec would find bin from dir.
func execPresent(bin, dir string) bool {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "node_modules", ".bin", bin)); err == nil {
			return true
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// goEnvInputs are the variables that move which toolchain `go version` reports. Reading
// GOTOOLCHAIN here keys the cache on it as an input go itself reads; nothing in magus
// behaves differently on its value.
var goEnvInputs = []string{"GOTOOLCHAIN=", "GOWORK=", "GOENV=", "GOROOT=", "GOFLAGS="}

// miseDirInputs are the files a mise shim reads, in any ancestor of the working
// directory, to pick the version it runs.
var miseDirInputs = []string{
	".config/mise/config.toml", ".config/mise/mise.toml", ".config/mise.toml",
	".mise/config.toml", "mise/config.toml", ".rtx.toml", "mise.toml", ".mise.toml",
	".config/mise/config.local.toml", ".config/mise/mise.local.toml", ".config/mise.local.toml",
	".mise/config.local.toml", ".rtx.local.toml", "mise.local.toml", ".mise.local.toml",
	".tool-versions", ".nvmrc", ".node-version", "package.json",
}

// probeCacheFormat changes whenever the key's composition or the records' encoding
// does, so an older entry is never read under a new meaning.
const probeCacheFormat = "probe-cache/3"

// probeSpecFor returns what decides probe's answer, and false for a probe never cached.
func probeSpecFor(probe spells.Command) (probeRecipe, bool) {
	recipe, ok := probeInputs[strings.Join(append([]string{probe.Bin}, probe.Args...), " ")]
	return recipe, ok
}

// probeCacheKey returns the key a probe of tool in dir caches under, or false when
// that probe must fork.
//
// The directory itself is not part of the key, only the input files that exist along its
// ancestry, by path and identity. Two project directories that see the same files resolve
// the same version, so they share one answer; a file created in either adds a line and
// moves its key, which is what an absent-file line used to catch.
func probeCacheKey(probe spells.Command, dir string) (string, bool) {
	recipe, ok := probeSpecFor(probe)
	if !ok || !filepath.IsAbs(dir) {
		return "", false
	}
	// A relative PATH entry resolves against the probe's directory, which the lookup
	// below does not model.
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(entry) {
			return "", false
		}
	}
	found, err := exec.LookPath(probe.Bin)
	if err != nil {
		return "", false
	}
	target, err := filepath.EvalSymlinks(found)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s %q\n%s\n", probeCacheFormat, probe.Bin, probe.Args, found)
	if !binaryCacheable(target) {
		return "", false
	}
	writeBinaryIdentity(&b, target)
	writeEnv(&b, recipe)
	names := recipe.files
	switch base := filepath.Base(target); {
	case base == "mise":
		names = append(slices.Clone(recipe.files), miseDirInputs...)
		names = append(names, miseEnvInputs()...)
		writeMiseGlobals(&b)
	case strings.Contains(base, "shim") || slices.Contains([]string{"asdf", "volta", "proto", "corepack", "rustup"}, base):
		return "", false
	}
	for _, bin := range recipe.pathBins {
		if path, err := exec.LookPath(bin); err == nil {
			writePresentIdentity(&b, path)
		}
	}
	for d := dir; ; d = filepath.Dir(d) {
		for _, name := range names {
			writePresentIdentity(&b, filepath.Join(d, name))
		}
		writePresentEntries(&b, filepath.Join(d, ".config", "mise", "conf.d"))
		if filepath.Dir(d) == d {
			break
		}
	}
	if recipe.goToolchain {
		writeGoToolchain(&b, dir)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), true
}

// writePresentIdentity is writeFileIdentity for a file that exists, and nothing for one
// that does not, so a directory chain contributes only what is really there.
func writePresentIdentity(b *strings.Builder, path string) {
	if _, err := os.Lstat(path); err == nil {
		writeFileIdentity(b, path)
	}
}

// writePresentEntries is writeDirEntries for a directory that exists, and nothing for one
// that does not.
func writePresentEntries(b *strings.Builder, dir string) {
	if _, err := os.Stat(dir); err == nil {
		writeDirEntries(b, dir)
	}
}

// writeGoToolchain records what go reads to decide whether to switch toolchains: the go
// and toolchain lines of the go.work and go.mod it would find from dir, and the GOENV file
// `go env -w` writes. The lines are recorded by content rather than by file identity, so
// a go.mod edit that leaves both alone keeps the answer.
func writeGoToolchain(b *strings.Builder, dir string) {
	switch work := os.Getenv("GOWORK"); work {
	case "off":
	case "":
		writeToolchainLines(b, "go.work", nearest(dir, "go.work"))
	default:
		writeToolchainLines(b, "go.work", work)
	}
	writeToolchainLines(b, "go.mod", nearest(dir, "go.mod"))
	env := os.Getenv("GOENV")
	if env == "" {
		if config, err := os.UserConfigDir(); err == nil {
			env = filepath.Join(config, "go", "env")
		}
	}
	if env != "" && env != "off" {
		writeFileIdentity(b, env)
	}
}

// nearest returns name in dir or its closest ancestor, or "" when none has it.
func nearest(dir, name string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, name)); err == nil {
			return filepath.Join(d, name)
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// writeToolchainLines records the go and toolchain directives of the file at path, under
// label, or that there is none.
func writeToolchainLines(b *strings.Builder, label, path string) {
	if path == "" {
		fmt.Fprintf(b, "%s -\n", label)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(b, "%s unreadable\n", label)
		return
	}
	fmt.Fprintf(b, "%s\n", label)
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") || strings.HasPrefix(line, "toolchain ") {
			fmt.Fprintf(b, "  %s\n", line)
		}
	}
}

// binaryCacheable reports whether a probe of path may be cached at all: not a script,
// since a shell or node wrapper decides what to run at run time, from inputs nothing
// here can list, and not a multi-call binary reached through a hard link, which is how
// rustup proxies.
func binaryCacheable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	// Where the platform cannot say, it cannot rule out the hard link either.
	if _, nlink, ok := file.Identity(fi); !ok || nlink > 1 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	head := make([]byte, 2)
	_, err = io.ReadFull(f, head)
	_ = f.Close()
	return err == nil && !bytes.Equal(head, []byte("#!"))
}

// writeBinaryIdentity records the executable a probe runs. Call it only after
// binaryCacheable confirms path is worth keying on.
func writeBinaryIdentity(b *strings.Builder, path string) {
	writeFileIdentity(b, path)
}

// writeFileIdentity records whether path exists and, when it does, what would change
// with its content. A file created later changes the line as surely as an edit does.
func writeFileIdentity(b *strings.Builder, path string) {
	fi, err := os.Lstat(path)
	if err != nil {
		fmt.Fprintf(b, "%s -\n", path)
		return
	}
	ino, _, _ := file.Identity(fi)
	fmt.Fprintf(b, "%s %d %d %d %o", path, fi.Size(), fi.ModTime().UnixNano(), ino, fi.Mode())
	if fi.Mode()&os.ModeSymlink != 0 {
		link, _ := os.Readlink(path)
		fmt.Fprintf(b, " -> %s", link)
	}
	b.WriteByte('\n')
}

// writeDirEntries records every entry of dir, since editing a file inside a directory
// does not move the directory's own modification time.
func writeDirEntries(b *strings.Builder, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(b, "%s/ -\n", dir)
		return
	}
	for _, e := range entries {
		writeFileIdentity(b, filepath.Join(dir, e.Name()))
	}
}

// probeEnvPrefixes are the variables node, pnpm and a mise shim read, matched without
// case because npm's config variables are. The whole environment was not usable: a
// benchmark harness or a terminal can set a variable per invocation, and every probe
// then missed.
var probeEnvPrefixes = []string{
	"PATH=", "HOME=", "XDG_", "MISE_", "RTX_", "ASDF_", "NODE_", "NPM_CONFIG_", "PNPM_", "COREPACK_",
}

// writeEnv records the variables the cached tools read to decide their version.
func writeEnv(b *strings.Builder, recipe probeRecipe) {
	prefixes := probeEnvPrefixes
	if recipe.goToolchain {
		prefixes = append(slices.Clone(prefixes), goEnvInputs...)
	}
	env := os.Environ()
	slices.Sort(env)
	for _, kv := range env {
		upper := strings.ToUpper(kv)
		if slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(upper, p) }) {
			b.WriteString(kv)
			b.WriteByte('\n')
		}
	}
}

// miseEnvInputs are the per-environment config files MISE_ENV selects.
func miseEnvInputs() []string {
	var out []string
	for _, env := range strings.Split(os.Getenv("MISE_ENV"), ",") {
		if env = strings.TrimSpace(env); env == "" {
			continue
		}
		for _, name := range miseDirInputs {
			if base, ok := strings.CutSuffix(name, ".toml"); ok {
				out = append(out, base+"."+env+".toml")
			}
		}
	}
	return out
}

// writeMiseGlobals records what a mise shim reads outside the directory chain: the
// system and explicitly named configs, which configs are trusted, and which versions
// are installed, since a fuzzy request such as "24" or "lts" resolves against those.
func writeMiseGlobals(b *strings.Builder) {
	home, _ := os.UserHomeDir()
	xdg := func(env, fallback string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		return filepath.Join(home, fallback)
	}
	data := os.Getenv("MISE_DATA_DIR")
	if data == "" {
		data = filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "mise")
	}
	state := os.Getenv("MISE_STATE_DIR")
	if state == "" {
		state = filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "mise")
	}
	config := os.Getenv("MISE_CONFIG_DIR")
	if config == "" {
		config = filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "mise")
	}
	for _, path := range []string{
		filepath.Join(config, "config.toml"), filepath.Join(config, "config.local.toml"),
		filepath.Join(config, "mise.toml"), filepath.Join(config, "mise.local.toml"),
		"/etc/mise/config.toml", "/etc/mise/config.local.toml",
		os.Getenv("MISE_CONFIG_FILE"), os.Getenv("MISE_GLOBAL_CONFIG_FILE"), os.Getenv("MISE_SYSTEM_CONFIG_FILE"),
	} {
		if path != "" {
			writeFileIdentity(b, path)
		}
	}
	writeDirEntries(b, filepath.Join(config, "conf.d"))
	writeDirEntries(b, "/etc/mise/conf.d")
	writeDirEntries(b, filepath.Join(state, "trusted-configs"))
	// tracked-configs is left out: it is the list `mise prune` reads to see which configs
	// still name a version, one symlink per config mise ever loaded (hundreds on a machine
	// with many worktrees), and nothing in it changes which version a shim runs. Keying on
	// it cost every probe a readlink per entry.
	writeDirEntries(b, filepath.Join(data, "installs"))
}

// probeFailureTTL bounds how long a cached failure answers. A success is a function of
// the key's inputs; a failure usually is too (no node_modules, no installed toolchain,
// each of which the key sees), but a download or a network hiccup is not, and this is
// how long such a failure can outlive its cause.
const probeFailureTTL = 10 * time.Minute

// probeFailedSuffix names the file a failed probe is cached under, beside the key a
// success would use.
const probeFailedSuffix = ".failed"

// probeAbsentSuffix names the file an absent tool's cause is cached under.
const probeAbsentSuffix = ".absent"

// probeCached answers probe in dir from the probe cache when the probe's inputs can be
// enumerated, and otherwise runs fork, which forks it. What it forks for is cached,
// failure included.
//
// Concurrent callers asking under one key share a single fork: project directories that
// see the same inputs share a key, so a sweep over seven Go projects forks `go version`
// once rather than seven times.
func (m *Magus) probeCached(ctx context.Context, probe spells.Command, dir string, fork func() (string, error)) (string, error) {
	key, cacheable := probeCacheKey(probe, dir)
	// A Magus with no workspace has no cache directory to answer from.
	if !cacheable || m.ws == nil {
		return fork()
	}
	if a, hit := m.cachedProbe(key); hit {
		return a.out, a.err
	}
	return probeFlights.do(m.CacheDir()+"\x00"+key, func() (string, error) {
		// A flight that finished between the lookup above and this one stored its answer.
		if a, hit := m.cachedProbe(key); hit {
			return a.out, a.err
		}
		out, err := fork()
		// A cancelled probe says nothing about the tool.
		if ctx.Err() == nil {
			m.storeProbe(key, out, err)
		}
		return out, err
	})
}

// probeAnswer is one cached probe: its output, or the error it failed with.
type probeAnswer struct {
	out string
	err error
}

// probeFlights joins concurrent forks of one key. A flight is forgotten once it lands, so
// the next caller reads the cache: holding answers here would outlive a failure's TTL in
// a long-lived server.
var probeFlights flightGroup

type flightGroup struct {
	mu      sync.Mutex
	flights map[string]*flight
}

type flight struct {
	done chan struct{}
	out  string
	err  error
}

func (g *flightGroup) do(key string, fn func() (string, error)) (string, error) {
	g.mu.Lock()
	if f, ok := g.flights[key]; ok {
		g.mu.Unlock()
		<-f.done
		return f.out, f.err
	}
	if g.flights == nil {
		g.flights = map[string]*flight{}
	}
	f := &flight{done: make(chan struct{})}
	g.flights[key] = f
	g.mu.Unlock()

	f.out, f.err = fn()
	g.mu.Lock()
	delete(g.flights, key)
	g.mu.Unlock()
	close(f.done)
	return f.out, f.err
}

// cachedProbe returns a cached answer under key: the output of a probe that succeeded,
// the cause of an absent tool, or the error of one that failed within probeFailureTTL.
//
// An absence takes no TTL. Its cause is a fact the key already sees (no node_modules, an
// unfinished install), so it holds until one of the key's inputs moves, and a TTL would
// only re-fork and re-warn on a clock.
//
// It reads the cache directory whether or not this workspace opened the cache: an
// Inspect-built workspace probes for the symbol-index verdict too, and a read-only lookup
// needs none of what Open wires.
func (m *Magus) cachedProbe(key string) (probeAnswer, bool) {
	dir := filepath.Join(m.CacheDir(), "probes")
	if out, err := os.ReadFile(filepath.Join(dir, key)); err == nil {
		return probeAnswer{out: string(out)}, true
	}
	if cause, err := os.ReadFile(filepath.Join(dir, key+probeAbsentSuffix)); err == nil && len(cause) > 0 {
		return probeAnswer{err: &absentTool{cause: string(cause), recorded: true}}, true
	}
	failed := filepath.Join(dir, key+probeFailedSuffix)
	fi, err := os.Stat(failed)
	if err != nil || time.Since(fi.ModTime()) >= probeFailureTTL {
		return probeAnswer{}, false
	}
	msg, err := os.ReadFile(failed)
	if err != nil {
		return probeAnswer{}, false
	}
	return probeAnswer{err: errors.New(string(msg))}, true
}

// storeProbe caches a probe's answer, or its failure when err is set. Best-effort: a
// failed write costs the next run a fork.
func (m *Magus) storeProbe(key, out string, err error) {
	if !m.cfg.Cache.WriteEnabled() {
		return
	}
	dir := filepath.Join(m.CacheDir(), "probes")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	var absent *absentTool
	if errors.As(err, &absent) {
		_ = file.WriteFileAtomic(filepath.Join(dir, key+probeAbsentSuffix), []byte(absent.cause), 0o644)
		return
	}
	if err != nil {
		_ = file.ReplaceFile(filepath.Join(dir, key+probeFailedSuffix), []byte(err.Error()), 0o644)
		return
	}
	_ = file.WriteFileAtomic(filepath.Join(dir, key), []byte(out), 0o644)
}

// absentTool is a probe failure that says the tool is not there (see probeAbsence),
// carrying the cause and its fix.
type absentTool struct {
	cause string
	// recorded is set on an answer read back from the cache rather than just forked,
	// so the warning is printed once per change to the probe's inputs.
	recorded bool
}

func (e *absentTool) Error() string { return e.cause }
