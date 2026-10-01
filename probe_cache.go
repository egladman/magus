package magus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

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

// probeInputs says, per probe argv, what decides its answer. Only these probes are
// cached: go switches toolchains through GOTOOLCHAIN and go.mod, rustup proxies through
// rust-toolchain files and its own settings, and a script wrapper can read anything, so
// none of those has an input list that could be trusted.
var probeInputs = map[string]probeSpec{
	"node --version": {},
	// pnpm switches to the version a manifest pins (manage-package-manager-versions),
	// configured from any of these.
	"pnpm --version": {files: []string{"package.json", "pnpm-workspace.yaml", ".npmrc"}},
	// The install decides this one: the stamps pnpm writes when an install finishes, the
	// shim it writes into .bin, and the typescript link, whose target names the version.
	// A tree with no node_modules records every one as absent, so installing misses.
	"pnpm exec tsc --version": {
		files: []string{
			"package.json", "pnpm-workspace.yaml", ".npmrc", "pnpm-lock.yaml",
			"node_modules/.modules.yaml", "node_modules/.pnpm/lock.yaml",
			"node_modules/.bin/tsc", "node_modules/typescript",
		},
		execs: "tsc",
	},
}

// probeSpec is what one probe's answer depends on beyond the binary and environment.
type probeSpec struct {
	// files are read in the probe's directory and every ancestor.
	files []string
	// execs is the binary the probe reaches through a package manager's exec: found in
	// node_modules/.bin of the directory or an ancestor, then on PATH.
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

// miseDirInputs are the files a mise shim reads, in any ancestor of the working
// directory, to pick the version it runs.
var miseDirInputs = []string{
	".config/mise/config.toml", ".config/mise/mise.toml", ".config/mise.toml",
	".mise/config.toml", "mise/config.toml", ".rtx.toml", "mise.toml", ".mise.toml",
	".config/mise/config.local.toml", ".config/mise/mise.local.toml", ".config/mise.local.toml",
	".mise/config.local.toml", ".rtx.local.toml", "mise.local.toml", ".mise.local.toml",
	".tool-versions", ".nvmrc", ".node-version", "package.json",
}

// probeCacheFormat changes whenever the key's composition or the record's encoding
// does, so an older entry is never read under a new meaning.
const probeCacheFormat = "probe-cache/2"

// probeSpecFor returns what decides probe's answer, and false for a probe never cached.
func probeSpecFor(probe spells.Command) (probeSpec, bool) {
	spec, ok := probeInputs[strings.Join(append([]string{probe.Bin}, probe.Args...), " ")]
	return spec, ok
}

// probeCacheKey returns the key a probe of tool in dir caches under, or false when
// that probe must fork.
func probeCacheKey(probe spells.Command, dir string) (string, bool) {
	spec, ok := probeSpecFor(probe)
	if !ok || !filepath.IsAbs(dir) {
		return "", false
	}
	selectors := spec.files
	// pnpm exec falls back to a tsc on PATH, whose own inputs nothing here lists.
	if spec.execs != "" {
		if _, err := exec.LookPath(spec.execs); err == nil {
			return "", false
		}
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
	fmt.Fprintf(&b, "%s\n%s %q\n%s\n%s\n", probeCacheFormat, probe.Bin, probe.Args, dir, found)
	if !binaryCacheable(target) {
		return "", false
	}
	writeBinaryIdentity(&b, target)
	writeEnv(&b)
	names := selectors
	switch base := filepath.Base(target); {
	case base == "mise":
		names = append(slices.Clone(selectors), miseDirInputs...)
		names = append(names, miseEnvInputs()...)
		writeMiseGlobals(&b)
	case strings.Contains(base, "shim") || slices.Contains([]string{"asdf", "volta", "proto", "corepack", "rustup"}, base):
		return "", false
	}
	for d := dir; ; d = filepath.Dir(d) {
		for _, name := range names {
			writeFileIdentity(&b, filepath.Join(d, name))
		}
		writeDirEntries(&b, filepath.Join(d, ".config", "mise", "conf.d"))
		if filepath.Dir(d) == d {
			break
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), true
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
func writeEnv(b *strings.Builder) {
	env := os.Environ()
	slices.Sort(env)
	for _, kv := range env {
		upper := strings.ToUpper(kv)
		if slices.ContainsFunc(probeEnvPrefixes, func(p string) bool { return strings.HasPrefix(upper, p) }) {
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
	writeDirEntries(b, filepath.Join(state, "tracked-configs"))
	writeDirEntries(b, filepath.Join(data, "installs"))
}

// probeRecord is one cached probe answer: the tool's output, or why the tool is absent.
// An absence is cached as surely as a version, since the same inputs give the same
// answer, and re-forking a probe that cannot succeed was a spawn on every invocation.
type probeRecord struct {
	out    string
	absent string
}

// The record's first line says which answer follows it.
const (
	probeRecordOK     = "ok\n"
	probeRecordAbsent = "absent\n"
)

func (m *Magus) cachedProbe(key string) (probeRecord, bool) {
	if m.cache == nil {
		return probeRecord{}, false
	}
	raw, err := os.ReadFile(filepath.Join(m.CacheDir(), "probes", key))
	if err != nil {
		return probeRecord{}, false
	}
	if out, ok := strings.CutPrefix(string(raw), probeRecordOK); ok {
		return probeRecord{out: out}, true
	}
	if cause, ok := strings.CutPrefix(string(raw), probeRecordAbsent); ok && cause != "" {
		return probeRecord{absent: cause}, true
	}
	return probeRecord{}, false
}

// storeProbe caches a probe answer. Best-effort: a failed write costs the next run a fork.
func (m *Magus) storeProbe(key string, r probeRecord) {
	if m.cache == nil || !m.cfg.Cache.WriteEnabled() {
		return
	}
	dir := filepath.Join(m.CacheDir(), "probes")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	body := probeRecordOK + r.out
	if r.absent != "" {
		body = probeRecordAbsent + r.absent
	}
	_ = file.WriteFileAtomic(filepath.Join(dir, key), []byte(body), 0o644)
}
