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

// probeInputs are the files a probed tool reads to pick its version, searched in the
// probe's directory and every ancestor. Only these tools are cached: go switches
// toolchains through GOTOOLCHAIN and go.mod, rustup proxies through rust-toolchain
// files and its own settings, and a script wrapper can read anything, so none of those
// has an input list that could be trusted.
var probeInputs = map[string][]string{
	"node --version": nil,
	// pnpm switches to the version a manifest pins (manage-package-manager-versions),
	// configured from any of these.
	"pnpm --version": {"package.json", "pnpm-workspace.yaml", ".npmrc"},
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

// probeCacheFormat changes whenever the key's composition does, so an older entry is
// never read under a new meaning.
const probeCacheFormat = "probe-cache/1"

// probeCacheKey returns the key a probe of tool in dir caches under, or false when
// that probe must fork.
func probeCacheKey(probe spells.Command, dir string) (string, bool) {
	selectors, ok := probeInputs[strings.Join(append([]string{probe.Bin}, probe.Args...), " ")]
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
	fmt.Fprintf(&b, "%s\n%s %q\n%s\n%s\n", probeCacheFormat, probe.Bin, probe.Args, dir, found)
	if !writeBinaryIdentity(&b, target) {
		return "", false
	}
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

// writeBinaryIdentity records the executable a probe runs, refusing a script: a shell
// or node wrapper decides what to run at run time, from inputs nothing here can list.
// So does a multi-call binary reached through a hard link, which is how rustup proxies.
func writeBinaryIdentity(b *strings.Builder, path string) bool {
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
	if err != nil || bytes.Equal(head, []byte("#!")) {
		return false
	}
	writeFileIdentity(b, path)
	return true
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

// cachedProbe reads a cached probe answer.
func (m *Magus) cachedProbe(key string) (string, bool) {
	if m.cache == nil {
		return "", false
	}
	out, err := os.ReadFile(filepath.Join(m.CacheDir(), "probes", key))
	if err != nil {
		return "", false
	}
	return string(out), true
}

// storeProbe caches a probe answer. Best-effort: a failed write costs the next run a fork.
func (m *Magus) storeProbe(key, out string) {
	if m.cache == nil || !m.cfg.Cache.WriteEnabled() {
		return
	}
	dir := filepath.Join(m.CacheDir(), "probes")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	_ = file.WriteFileAtomic(filepath.Join(dir, key), []byte(out), 0o644)
}
