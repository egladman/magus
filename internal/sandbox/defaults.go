package sandbox

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// PolicyOptions is everything BuildPolicy reads about the host. The caller gathers it
// (see confinement.FromConfig), so a test states every input.
type PolicyOptions struct {
	Workspace  string // workspace root: read, write, exec
	CacheDir   string // magus's cache: read, write
	TempDir    string // private temp dir the caller created: read, write, exec, and children's TMPDIR
	Executable string // the running magus binary: read, exec
	// GitDir is the checkout's own git directory and GitCommonDir the repository's
	// shared one. A linked worktree keeps both outside the workspace, so they get
	// grants of their own: GitDir and the object store read-write, which is what
	// status, add and diff write, and the rest of the common dir read-only.
	GitDir, GitCommonDir string
	// Home is the user's home directory. It locates tool caches and is never
	// granted itself: ~/.ssh, ~/.aws and ~/.config stay out.
	Home string
	GOOS string // picks the per-OS default cache locations
	// Environ is the host environment. Children's BaseEnv is scrubbed from it, and
	// PATH and the tool location variables (GOCACHE, CARGO_HOME, ...) are read from it.
	Environ []string
	// InstallDirs are toolchain roots found on the host rather than named in Environ,
	// such as the GOROOT of the go on PATH: read, exec.
	InstallDirs []string
	Allow       []filesystem.Rule // sandbox.allow entries
	Env         env.Allowlist     // sandbox.env.passthrough, on top of env.DefaultAllow
}

// BuildPolicy assembles the Policy o describes. It reads nothing from the host beyond
// resolving each rule path through its symlinks, which a rule needs to compare equal
// to a checked path. A path that does not exist is kept: it matches nothing, and the
// kernel layer skips it.
//
// Beyond o's own paths it grants read+exec on the system trees (/usr, /bin, /sbin,
// /lib*, /opt, /nix/store, /snap), on each absolute PATH entry other than home and its
// ancestors, and on each toolchain install root (GOROOT, RUSTUP_HOME, mise and asdf
// data). It grants read on /etc and a few /proc and /sys files runtimes probe, and
// read+write on /dev/null, the terminal devices and per-tool caches (GOCACHE,
// GOMODCACHE, cargo's registry, npm, pnpm, yarn, pip, uv, mise).
//
// The shared temp dirs are not granted: they hold ssh-agent, gpg and docker sockets.
// Children get o.TempDir as TMPDIR instead.
func BuildPolicy(o PolicyOptions) *Policy {
	vars := envMap(o.Environ)
	rules := []filesystem.Rule{
		rwx(o.Workspace), rw(o.CacheDir), rwx(o.TempDir), rx(o.Executable),
		rw(o.GitDir), ro(o.GitCommonDir), rw(join(o.GitCommonDir, "objects")),
	}
	rules = append(rules, systemRules...)
	rules = append(rules, pathRules(vars["PATH"], o.Home)...)
	rules = append(rules, toolRules(vars, o.Home, o.GOOS)...)
	for _, d := range o.InstallDirs {
		rules = append(rules, rx(d))
	}
	rules = append(rules, o.Allow...)

	rules = slices.DeleteFunc(rules, func(r filesystem.Rule) bool { return r.Path == "" })
	for i := range rules {
		rules[i].Path = filesystem.ResolveRulePath(rules[i].Path)
	}
	rules = mergeRulesByPath(rules)

	allow := env.Allowlist{
		Names:    append(env.DefaultAllow(), o.Env.Names...),
		Prefixes: slices.Clone(o.Env.Prefixes),
	}
	kept, dropped := allow.Scrub(o.Environ)
	kept = slices.DeleteFunc(kept, func(kv string) bool { return strings.HasPrefix(kv, "TMPDIR=") })
	if o.TempDir != "" {
		kept = append(kept, "TMPDIR="+o.TempDir)
	}
	slices.Sort(kept)
	return &Policy{
		FS:         filesystem.Ruleset{Rules: rules},
		Env:        allow,
		BaseEnv:    kept,
		EnvDropped: dropped,
		TempDir:    o.TempDir,
	}
}

func ro(path string) filesystem.Rule { return filesystem.Rule{Path: path, Read: true} }
func rx(path string) filesystem.Rule { return filesystem.Rule{Path: path, Read: true, Exec: true} }
func rw(path string) filesystem.Rule { return filesystem.Rule{Path: path, Read: true, Write: true} }
func rwx(path string) filesystem.Rule {
	return filesystem.Rule{Path: path, Read: true, Write: true, Exec: true}
}

// join is filepath.Join that keeps an unknown base unknown rather than making the
// rest a relative path.
func join(base string, elem ...string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, elem...)...)
}

// systemRules is what a toolchain needs from the host outside any home directory.
//
// /usr is read+exec as a whole, lib included, because toolchains run their own
// helpers from lib directories (gcc's cc1, git-core). With /usr/bin executable,
// refusing exec one directory over denies nothing an attacker needs.
//
// The /etc files beside /etc are there for their symlink targets: resolv.conf and
// localtime point outside /etc, and a rule path is resolved when it is added.
//
// The /proc/self entries are the magus process's own when landlock applies them, as
// landlock binds a rule to the inode it opened. /proc/self/environ is deliberately
// absent: in-process Buzz would read magus's unscrubbed environment through it.
var systemRules = []filesystem.Rule{
	rx("/usr"), rx("/bin"), rx("/sbin"),
	rx("/lib"), rx("/lib32"), rx("/lib64"), rx("/libx32"),
	rx("/opt"), rx("/nix/store"), rx("/run/current-system/sw"), rx("/snap"),
	rx("/home/linuxbrew/.linuxbrew"), rx("/Library/Developer"),

	ro("/etc"), ro("/etc/resolv.conf"), ro("/etc/hosts"), ro("/etc/localtime"),
	ro("/etc/ssl"), ro("/etc/pki"), ro("/etc/ca-certificates"),

	rw("/dev/null"), ro("/dev/zero"), ro("/dev/random"), ro("/dev/urandom"),
	rw("/dev/tty"), rw("/dev/ptmx"), rw("/dev/pts"), rw("/dev/fd"),

	ro("/proc/cpuinfo"), ro("/proc/meminfo"), ro("/proc/stat"), ro("/proc/loadavg"),
	ro("/proc/self/cgroup"), ro("/proc/self/mountinfo"), ro("/proc/self/stat"),
	ro("/proc/self/status"), rw("/proc/self/fd"),
	ro("/sys/fs/cgroup"), ro("/sys/devices/system/cpu"),
}

// pathRules grants read+exec on each PATH entry a child could run a tool from. A
// relative entry would follow the working directory, and home or any ancestor of it
// (a ~/ on PATH, or /) would grant the whole home tree, so both are skipped: a tool
// living there needs a sandbox.allow entry of its own.
func pathRules(path, home string) []filesystem.Rule {
	var out []filesystem.Rule
	for _, dir := range filepath.SplitList(path) {
		dir = filepath.Clean(dir)
		if !filepath.IsAbs(dir) || (home != "" && filesystem.Under(filepath.Clean(home), dir)) {
			continue
		}
		out = append(out, rx(dir))
	}
	return out
}

// toolRules grants the install roots and caches of the toolchains magus's spells
// drive, each at the location the tool itself would use: its own variable when set,
// its documented default otherwise. Credentials beside them (~/.cargo/credentials.toml,
// ~/.npmrc) stay out, which is why cargo gets its registry and not its home.
func toolRules(vars map[string]string, home, goos string) []filesystem.Rule {
	pick := func(name, def string) string {
		if v := vars[name]; v != "" {
			return v
		}
		return def
	}
	// Go's os.UserCacheDir and os.UserConfigDir, and the XDG homes on every OS.
	userCache := pick("XDG_CACHE_HOME", join(home, ".cache"))
	userConfig := pick("XDG_CONFIG_HOME", join(home, ".config"))
	if goos == "darwin" {
		userCache = join(home, "Library", "Caches")
		userConfig = join(home, "Library", "Application Support")
	}
	xdgCache := pick("XDG_CACHE_HOME", join(home, ".cache"))
	xdgData := pick("XDG_DATA_HOME", join(home, ".local", "share"))
	xdgState := pick("XDG_STATE_HOME", join(home, ".local", "state"))
	gopath := pick("GOPATH", join(home, "go"))
	if i := strings.IndexRune(gopath, filepath.ListSeparator); i >= 0 {
		gopath = gopath[:i]
	}
	cargo := pick("CARGO_HOME", join(home, ".cargo"))
	pnpmStore := join(xdgData, "pnpm")
	if goos == "darwin" {
		pnpmStore = join(home, "Library", "pnpm")
	}

	rules := []filesystem.Rule{
		// Go
		rx(vars["GOROOT"]),
		rx(join(gopath, "bin")),
		rw(pick("GOMODCACHE", join(gopath, "pkg", "mod"))),
		rw(pick("GOCACHE", join(userCache, "go-build"))),
		ro(pick("GOENV", join(userConfig, "go", "env"))),
		rw(pick("GOLANGCI_LINT_CACHE", join(userCache, "golangci-lint"))),
		// Rust
		rx(pick("RUSTUP_HOME", join(home, ".rustup"))),
		rx(join(cargo, "bin")),
		rw(join(cargo, "registry")),
		rw(join(cargo, "git")),
		// Node
		rw(pick("npm_config_cache", join(home, ".npm"))),
		rx(vars["PNPM_HOME"]),
		rw(pnpmStore),
		rw(join(userCache, "pnpm")),
		rw(pick("YARN_CACHE_FOLDER", join(userCache, "yarn"))),
		rw(pick("COREPACK_HOME", join(xdgCache, "node", "corepack"))),
		// Python
		rw(pick("PIP_CACHE_DIR", join(userCache, "pip"))),
		rw(pick("UV_CACHE_DIR", join(xdgCache, "uv"))),
		rx(join(xdgData, "uv")),
		// Version managers: installs and shims run, the trust store is only read.
		rx(pick("MISE_DATA_DIR", join(xdgData, "mise"))),
		rw(pick("MISE_CACHE_DIR", join(userCache, "mise"))),
		ro(pick("MISE_STATE_DIR", join(xdgState, "mise"))),
		rx(pick("ASDF_DATA_DIR", join(home, ".asdf"))),
	}
	// A variable is a location, not a grant: GOCACHE=off is relative, and
	// XDG_CACHE_HOME=$HOME would hand over the whole home tree.
	return slices.DeleteFunc(rules, func(r filesystem.Rule) bool {
		return !filepath.IsAbs(r.Path) || (home != "" && filesystem.Under(filepath.Clean(home), filepath.Clean(r.Path)))
	})
}

func envMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// mergeRulesByPath collapses same-path rules by OR-ing R/W/X flags, preserving first-seen order.
func mergeRulesByPath(rules []filesystem.Rule) []filesystem.Rule {
	seen := make(map[string]int, len(rules))
	out := make([]filesystem.Rule, 0, len(rules))
	for _, r := range rules {
		if idx, ok := seen[r.Path]; ok {
			out[idx].Read = out[idx].Read || r.Read
			out[idx].Write = out[idx].Write || r.Write
			out[idx].Exec = out[idx].Exec || r.Exec
			continue
		}
		seen[r.Path] = len(out)
		out = append(out, r)
	}
	return out
}
