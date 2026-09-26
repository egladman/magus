package sandbox

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// PolicyOptions is everything BuildPolicy reads about the host. The caller gathers it
// (see FromConfig), so a test states every input.
type PolicyOptions struct {
	Mode       types.SandboxMode // becomes Policy.Mode
	Workspace  string            // workspace root: read, write, exec
	CacheDir   string            // magus's cache: read, write
	TempDir    string            // private temp dir the caller created: read, write, exec, and children's TMPDIR
	Executable string            // the running magus binary: read, exec
	// GitDir is the checkout's own git directory and GitCommonDir the repository's
	// shared one. A linked worktree keeps both outside the workspace, so they get
	// grants of their own: GitDir and the object store read-write, which is what
	// status, add and diff write, and the rest of the common dir read-only.
	GitDir, GitCommonDir string
	// Home is the user's home directory. It is the base of the entries that name one and
	// is never granted itself: ~/.ssh, ~/.aws and ~/.config stay out.
	Home string
	GOOS string // picks the per-OS locations of the userCache and userConfig bases
	// Environ is the host environment. Children's BaseEnv is scrubbed from it, and PATH
	// and the variables the declarations name are read from it.
	Environ []string
	// Tools, when set, locates every entry that grants no write in place of Environ and
	// Home: the host the toolchains were installed on, when Environ and Home are a box of
	// the children's own (see FromConfigBoxed). A writable entry always resolves against
	// Environ and Home.
	Tools *ToolHost
	// Sandbox is the workspace layer: magus.yaml's sandbox.allow and env.passthrough.
	Sandbox spells.Sandbox
	// Spells is the spell layer: each loaded spell's mgs_getSandbox, keyed by spell
	// name. Policy.Scoped keeps some of them.
	Spells map[string]spells.Sandbox
	// Target is the target layer, the running target's own `sandbox` policy; nil for none.
	Target *spells.Sandbox
}

// ToolHost is the environment and home a host's toolchains were installed under.
type ToolHost struct {
	Environ []string
	Home    string
}

// BuildPolicy assembles the Policy o describes: the core grants, and over them every
// declaration layer merged by mergeLayers. It reads nothing from the host beyond
// resolving each rule path through its symlinks, which a rule needs to compare equal
// to a checked path, and the binaries a binRoot base names. A path that does not exist
// is kept: the kernel layer creates a writable one as a directory and skips the rest.
//
// The core is the operating system and magus's own: o's paths, read+exec on the system
// trees (/usr, /bin, /sbin, /lib*, /opt, /nix/store, /snap) and on each absolute PATH
// entry other than home and its ancestors, read on /etc and a few /proc and /sys files
// runtimes probe, and read+write on /dev/null and the terminal devices. It knows no
// toolchain: what a tool keeps under home arrives through the spell layer.
//
// The shared temp dirs are not granted: they hold ssh-agent, gpg and docker sockets.
// Children get o.TempDir as TMPDIR instead.
func BuildPolicy(o PolicyOptions) *Policy {
	vars := envMap(o.Environ)
	host := hostDirs{vars: vars, home: o.Home, goos: o.GOOS}
	pathHome := o.Home
	if o.Tools != nil {
		host.tools = &hostDirs{vars: envMap(o.Tools.Environ), home: o.Tools.Home, goos: o.GOOS}
		pathHome = o.Tools.Home
	}
	rules := []filesystem.Rule{
		rwx(o.Workspace), rw(o.CacheDir), rwx(o.TempDir), rx(o.Executable),
		rw(o.GitDir), ro(o.GitCommonDir), rw(join(o.GitCommonDir, "objects")),
	}
	rules = append(rules, systemRules...)
	rules = append(rules, pathRules(vars["PATH"], pathHome)...)
	rules = slices.DeleteFunc(rules, func(r filesystem.Rule) bool { return r.Path == "" })

	layers := []spells.Sandbox{o.Sandbox}
	for _, name := range slices.Sorted(maps.Keys(o.Spells)) {
		layers = append(layers, o.Spells[name])
	}
	if o.Target != nil {
		layers = append(layers, *o.Target)
	}
	declared, allow := host.mergeLayers(layers...)
	rules = append(rules, declared...)
	writtenAt := map[string]string{}
	for i := range rules {
		written := rules[i].Path
		rules[i].Path = filesystem.ResolveRulePath(written)
		if rules[i].Path != written {
			writtenAt[rules[i].Path] = written
		}
	}
	rules = mergeRulesByPath(rules)

	kept, dropped := allow.Scrub(o.Environ)
	kept = slices.DeleteFunc(kept, func(kv string) bool { return strings.HasPrefix(kv, "TMPDIR=") })
	if o.TempDir != "" {
		kept = append(kept, "TMPDIR="+o.TempDir)
	}
	slices.Sort(kept)
	var gitDirs []string
	for _, d := range []string{o.GitDir, o.GitCommonDir} {
		if d != "" {
			gitDirs = append(gitDirs, filesystem.ResolveRulePath(d))
		}
	}
	var workspace string
	if o.Workspace != "" {
		workspace = filesystem.ResolveRulePath(o.Workspace)
	}
	return &Policy{
		FS:         filesystem.Ruleset{Rules: rules},
		Env:        allow,
		BaseEnv:    kept,
		EnvDropped: dropped,
		TempDir:    o.TempDir,
		Mode:       o.Mode.Resolved(),
		Workspace:  workspace,
		GitDirs:    slices.Compact(gitDirs),
		writtenAt:  writtenAt,
		opts:       &o,
		scoped:     newScopedPolicies(),
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
// The /proc/self entries resolve to magus's own when the policy is built, and landlock
// binds a rule to the inode it opened, so the launcher grants them again on the child's
// own entries (see procSelfRules). /proc/self/environ is deliberately absent: it holds
// the environment as it was before the scrub.
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

// hostDirs resolves the locations a declaration names against one host.
type hostDirs struct {
	vars       map[string]string
	home, goos string
	// tools, when set, resolves the entries that grant no write (see PolicyOptions.Tools).
	tools *hostDirs
}

// mergeLayers merges sandbox declarations into the rules and the variable allowlist
// they grant, each rule at the path it was declared at, links not yet followed. It is
// the one merge every layer goes through, workspace, spells and target alike: entries
// union, a path two entries name gets both their modes (rx and rw make rwx), and
// passthrough patterns union. No layer can take away what another granted.
//
// An entry resolving nowhere grants nothing. So does a location read from a variable
// or a base that is not absolute, or is home or an ancestor of it: GOCACHE=off is
// relative, and XDG_CACHE_HOME=$HOME would hand over the whole home tree. A literal
// path is taken as written. A passthrough pattern that does not parse was refused
// where it was declared, and is skipped here.
func (h hostDirs) mergeLayers(layers ...spells.Sandbox) ([]filesystem.Rule, env.Allowlist) {
	var rules []filesystem.Rule
	allow := env.Allowlist{Names: env.DefaultAllow()}
	for _, layer := range layers {
		for _, a := range layer.Allow {
			rule, err := filesystem.ModeRule(string(a.Mode))
			if err != nil {
				continue
			}
			on := h
			if !rule.Write && h.tools != nil {
				on = *h.tools
			}
			path, literal := on.locate(a)
			if path == "" || (!literal && (!filepath.IsAbs(path) || on.holdsHome(path))) {
				continue
			}
			rule.Path = path
			rules = append(rules, rule)
		}
		if pass, err := env.Parse(layer.Env.Passthrough); err == nil {
			allow.Names = append(allow.Names, pass.Names...)
			allow.Prefixes = append(allow.Prefixes, pass.Prefixes...)
		}
	}
	return mergeRulesByPath(rules), allow
}

// holdsHome reports whether path is home or one of its ancestors.
func (h hostDirs) holdsHome(path string) bool {
	return h.home != "" && filesystem.Under(filepath.Clean(h.home), filepath.Clean(path))
}

// locate returns where a points on this host, and whether that is a literal path the
// declaration wrote rather than one read from a variable or a base.
func (h hostDirs) locate(a spells.SandboxAllow) (path string, literal bool) {
	if a.Env != "" {
		if v := h.vars[a.Env]; v != "" {
			return v, false
		}
	}
	if a.Base == "" {
		if a.Path == "" {
			return "", false
		}
		return h.expand(a.Path), true
	}
	base := h.base(a)
	if base == "" {
		return "", false
	}
	if a.Requires != "" {
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(a.Requires))); err != nil {
			return "", false
		}
	}
	return join(base, filepath.FromSlash(a.Path)), false
}

// expand resolves a literal path's ~ and $VAR, "" when a variable is unset or ~ has no
// home to name.
func (h hostDirs) expand(raw string) string {
	unset := false
	p := os.Expand(raw, func(name string) string {
		v := h.vars[name]
		unset = unset || v == ""
		return v
	})
	if unset {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = join(h.home, p[1:])
	}
	return p
}

// base resolves a's base, "" when it does not resolve on this host. userCache and
// userConfig follow Go's os.UserCacheDir and os.UserConfigDir; the xdg bases follow the
// XDG layout on every OS.
func (h hostDirs) base(a spells.SandboxAllow) string {
	pick := func(name, def string) string {
		if v := h.vars[name]; v != "" {
			return v
		}
		return def
	}
	switch a.Base {
	case spells.SandboxBaseHome:
		return h.home
	case spells.SandboxBaseUserCache:
		if h.goos == "darwin" {
			return join(h.home, "Library", "Caches")
		}
		return pick("XDG_CACHE_HOME", join(h.home, ".cache"))
	case spells.SandboxBaseUserConfig:
		if h.goos == "darwin" {
			return join(h.home, "Library", "Application Support")
		}
		return pick("XDG_CONFIG_HOME", join(h.home, ".config"))
	case spells.SandboxBaseXDGCache:
		return pick("XDG_CACHE_HOME", join(h.home, ".cache"))
	case spells.SandboxBaseXDGData:
		return pick("XDG_DATA_HOME", join(h.home, ".local", "share"))
	case spells.SandboxBaseXDGState:
		return pick("XDG_STATE_HOME", join(h.home, ".local", "state"))
	case spells.SandboxBaseBinRoot:
		return binRoot(h.vars["PATH"], a.Bin)
	}
	if name, ok := strings.CutPrefix(a.Base, "$"); ok {
		v, _, _ := strings.Cut(h.vars[name], string(filepath.ListSeparator))
		return v
	}
	return ""
}

// binRoot is the install root of the first bin on path: the directory above the bin/
// its symlinks resolve into. A binary resolving anywhere but a bin/ directory has no
// root, so a shim yields "".
func binRoot(path, bin string) string {
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, bin))
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(dir, bin))
		if err != nil || filepath.Base(filepath.Dir(resolved)) != "bin" {
			return ""
		}
		return filepath.Dir(filepath.Dir(resolved))
	}
	return ""
}

// CheckDeclaration refuses a spell's or a target's sandbox declaration that cannot be
// honored, naming every bad entry. Each is a declaration bug knowable where it is
// written; resolved later, a bad entry would grant nothing and the tool would fail with
// a permission error naming a path, not the declaration.
//
// magus.yaml takes the same shape and one more form, a literal path holding $VAR, which
// FromConfig resolves strictly. A spell and a target may not: a variable unset on one
// host would fail every run there, where env and a base say what to use instead.
func CheckDeclaration(sb spells.Sandbox) error {
	var errs []error
	for i, a := range sb.Allow {
		if err := checkAllow(a); err != nil {
			errs = append(errs, fmt.Errorf("allow[%d]: %w", i, err))
		}
		if strings.Contains(a.Path, "$") {
			errs = append(errs, fmt.Errorf("allow[%d]: path %q names a variable; name it in env, or use a $VAR base", i, a.Path))
		}
	}
	if _, err := env.Parse(sb.Env.Passthrough); err != nil {
		errs = append(errs, fmt.Errorf("env.passthrough: %w", err))
	}
	return errors.Join(errs...)
}

// checkAllow refuses an entry no host could resolve the way it reads.
func checkAllow(a spells.SandboxAllow) error {
	if _, err := filesystem.ModeRule(string(a.Mode)); err != nil {
		return err
	}
	if a.Env != "" && !envName(a.Env) {
		return fmt.Errorf("env %q is not a variable name", a.Env)
	}
	local := func(p string) bool { return filepath.IsLocal(filepath.FromSlash(p)) }
	if a.Requires != "" && !local(a.Requires) {
		return fmt.Errorf("requires %q must be a relative path under the base", a.Requires)
	}
	switch {
	case a.Base == "":
		if a.Path == "" && a.Env == "" {
			return errors.New("names no env, base or path, so it grants nothing")
		}
		if a.Path != "" && !filepath.IsAbs(a.Path) && a.Path != "~" && !strings.HasPrefix(a.Path, "~/") && !strings.HasPrefix(a.Path, "$") {
			return fmt.Errorf("path %q must be absolute or start with ~ when no base is given", a.Path)
		}
		if a.Requires != "" {
			return errors.New("requires needs a base to be relative to")
		}
		return nil
	case a.Base == spells.SandboxBaseBinRoot:
		if a.Bin == "" || strings.ContainsAny(a.Bin, `/\`) {
			return fmt.Errorf("bin %q: a binRoot base names a binary looked up on PATH", a.Bin)
		}
	case strings.HasPrefix(a.Base, "$"):
		if !envName(a.Base[1:]) {
			return fmt.Errorf("base %q is not a variable", a.Base)
		}
	case !slices.Contains(symbolicBases, a.Base):
		return fmt.Errorf("base %q: want one of %s, a $VAR, or %s", a.Base, strings.Join(symbolicBases, ", "), spells.SandboxBaseBinRoot)
	case a.Path == "":
		// ~/.cache is not one tool's cache: it holds every other program's too.
		return fmt.Errorf("base %s names no path under it, which would grant the whole directory", a.Base)
	}
	if a.Path != "" && !local(a.Path) {
		return fmt.Errorf("path %q must be a relative path that stays under base %s", a.Path, a.Base)
	}
	return nil
}

var symbolicBases = []string{
	spells.SandboxBaseHome, spells.SandboxBaseUserCache, spells.SandboxBaseUserConfig,
	spells.SandboxBaseXDGCache, spells.SandboxBaseXDGData, spells.SandboxBaseXDGState,
}

// envName reports whether name can be an environment variable's name. A prefix pattern
// is a passthrough's spelling, never a location's.
func envName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "=*$/")
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
