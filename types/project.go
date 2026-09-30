package types

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/spells"
)

// workspaceScheme is the URI scheme WorkspaceURI renders: "workspace://<path>".
//
// A bare workspace-relative path is the only spelling magus teaches;
// internal/file.ResolveProject warns on the scheme when it parses one, and
// nothing in magus renders it any more. Display (the bare path) is what every
// surface emits; WorkspaceURI and WorkspaceRef exist for external callers not
// yet migrated. Do not reach for them for new output.
const workspaceScheme = "workspace://"

// ProjectRef is the canonical project reference: holds the data once
// (workspace-relative path plus optional absolute dir) and exposes two
// render methods. One source of truth means the URI form and the display
// form cannot drift: they share the same fields and the same ProjectLabel
// / WorkspaceRef helpers both delegate to a method on this struct.
type ProjectRef struct {
	// Path is the workspace-relative identifier: "." for the root, "pkg/foo"
	// for a nested project. Always forward-slash, never absolute, never escapes.
	Path string `json:"path" yaml:"path"`
	// Name is the human label. It coincides with Path for a nested project and
	// diverges for the root, which reads as the workspace directory's base name
	// rather than a bare ".". Carried on the wire because consumers of
	// SymbolIndexStatus and friends have always been able to read it without
	// re-deriving it from a directory they do not have.
	Name string `json:"name" yaml:"name"`
	// Dir is the project's absolute directory ("" if unknown), used only to
	// derive Name for the root. Never serialized: it is a host-specific
	// absolute path, and every consumer of this type reads workspace-relative
	// data. Putting it on the wire would leak the user's directory layout.
	Dir string `json:"-" yaml:"-"`
}

// NewProjectRef builds a ProjectRef from a workspace-relative path and the
// project's absolute directory. The dir feeds the root display name; pass
// "" when it is not known.
func NewProjectRef(path, dir string) ProjectRef {
	r := ProjectRef{Path: path, Dir: dir}
	r.Name = r.Display()
	return r
}

// WorkspaceURI renders the project as a "workspace://<path>" reference. An
// empty path is the workspace root, so a caller never prints a bare "." or "".
//
// Deprecated: the workspace:// spelling is retired; render the bare
// workspace-relative path instead (Display or ProjectLabel). Magus no longer
// emits this form itself.
func (r ProjectRef) WorkspaceURI() string {
	if r.Path == "" {
		return workspaceScheme + "."
	}
	return workspaceScheme + r.Path
}

// Display renders the project for human consumption: the bare path for
// nested projects, the dir basename for the root (so a bare "." never
// appears in logs or graph labels), and "(workspace root)" as the final
// fallback. This is the canonical rendering: the bare workspace-relative path
// is also what every project arg takes, so what magus prints pastes back in.
func (r ProjectRef) Display() string {
	if r.Path != "" && r.Path != "." {
		return r.Path
	}
	if r.Dir != "" {
		if base := filepath.Base(r.Dir); base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return "(workspace root)"
}

// ProjectLabel is a convenience for the Display form when the caller only
// has a path and a dir, not a ProjectRef value. Routes through the struct so
// the two forms share one definition; add new rendering rules to Display,
// and ProjectLabel picks them up automatically.
func ProjectLabel(path, dir string) string {
	return ProjectRef{Path: path, Dir: dir}.Display()
}

// WorkspaceRef is a convenience for the WorkspaceURI form when the caller
// only has a path. The dir is intentionally absent: a URI is path-only, and
// Display's dir-based root naming has no place in the machine-readable form.
//
// Deprecated: the workspace:// spelling is retired; render the bare
// workspace-relative path instead (Display or ProjectLabel). Magus no longer
// emits this form itself.
func WorkspaceRef(path string) string {
	return ProjectRef{Path: path}.WorkspaceURI()
}

// ProjectDisplayName returns an explicit display name when available,
// otherwise derives the shared never-dot label from the project path and
// directory. Used where a project carries a declared human name (a name:
// annotation in magusfile) that should win over the path-derived default.
func ProjectDisplayName(path, name, dir string) string {
	if name != "" && name != "." {
		return name
	}
	return ProjectLabel(path, dir)
}

// Binding is the per-spell registration state attached to a project.
// One Binding is created per WithSpell call.
type Binding struct {
	Name string // spell identifier
}

// ProjectOrigin is what put a project in the workspace. It is a named type rather
// than a bare string because its two cases are not interchangeable: OriginMagusfile
// is a whole value you compare, while a provided project's origin CARRIES the
// provider's name, so it has no single constant to compare against. Constructing
// and reading it through ProvidedBy and Provider keeps that asymmetry out of every
// call site: a hand-written `origin == "provider"` would be a condition that never
// fires.
type ProjectOrigin string

// OriginMagusfile is the origin of a directory discovery found a magusfile in.
const OriginMagusfile ProjectOrigin = "magusfile"

// originProviderPrefix tags an origin with the workspace provider that reported it.
const originProviderPrefix = "provider:"

// ProvidedBy returns the origin of a project a workspace provider reported. The
// spell name is part of the value because a workspace can wire more than one
// provider, and "which tool says this is a project" is the whole question a reader
// has about a directory with no magusfile in it.
func ProvidedBy(spellName string) ProjectOrigin {
	return ProjectOrigin(originProviderPrefix + spellName)
}

// Provider returns the spell that reported this project, and whether a provider
// reported it at all. It is the read half of ProvidedBy: the spell name lives inside
// the value, so a caller comparing against a bare "provider" would be writing a
// condition that never fires.
func (o ProjectOrigin) Provider() (string, bool) {
	name, ok := strings.CutPrefix(string(o), originProviderPrefix)
	return name, ok
}

// Project is the record magus maintains for every directory with a marker file.
type Project struct {
	Path string // repo-relative directory, forward slashes (e.g. "api", ".")
	// Name is the declared human label from magus.project's "name" key, or "" to
	// derive one from the path. It exists for the ROOT project, whose path is "."
	// and whose label would otherwise fall back to the checkout's directory
	// basename, so a worktree, a clone under a different name, or a CI checkout
	// each renamed the root project and rewrote every generated index that names
	// it. Declaring the name makes generated output reproducible anywhere.
	Name string
	// Origin is what put this project in the workspace (see ProjectOrigin). It is
	// PROVENANCE, never identity (nothing dispatches on it), and it exists because a
	// provided project has no file to point at, so "where did this come from" would
	// otherwise be unanswerable for exactly the projects a reader has never seen
	// declared anywhere. Named Origin rather than Source because Sources below is the
	// unrelated glob list, and one letter is not enough distance between "where this
	// project came from" and "the files it is built from".
	Origin    ProjectOrigin
	Dir       string // absolute filesystem path
	Spell     string // primary spell name; use Spells for fan-out dispatch
	Spells    []string
	Bindings  []*Binding // parallel to Spells, in registration order
	Sources   []Glob     // relative to Dir; key the cache
	Outputs   []Glob     // relative to Dir; snapshotted into and replayed from cache
	DependsOn []string
	// NoLanguage is the reason a project binds no toolchain spell ON PURPOSE, from
	// magus.project's "no_language" key. A spell-less project is legal and common, so
	// doctor's language-coverage check cannot tell an intentional one (a polyglot
	// harness no single pack describes) from a real gap (someone forgot to import the
	// go spell) without being told. Carrying the REASON rather than a bare bool is what
	// keeps the opt-out honest: it has to say what it is instead of silencing a check.
	NoLanguage string
	// ToolBounds is the version window THIS project requires of each binary its spells
	// drive, keyed by bin name, from magus.project's "tools" key. Intersected with what
	// the spell itself declares, narrower bound winning on each side, so neither can
	// loosen the other. The intersection happens once at run start, in checkToolWindows,
	// NOT at op dispatch, so a project whose targets never dispatch a spell op is held to
	// its window all the same.
	//
	// On the project rather than in magus.yaml, and that is not a filing preference.
	// config.Load merges a user-global tier ($XDG_CONFIG_HOME/magus/) beneath the
	// workspace, so a bound living there could be set in one person's private file and
	// silently gate every workspace on their machine. A magusfile is committed, is read
	// by everyone who reads the project, and is per project, which also means `console`
	// and `docs` can hold different policies instead of sharing one workspace-wide map.
	//
	// Sharing is an explicit import of a shared MODULE, never ambient inheritance: see
	// hack/toolchain-policy.buzz, imported the same way hack/advisories.buzz already is.
	// There is no special root project and nothing is inherited by position in the tree.
	//
	// Deliberately not `import "project/.." as root`. That handle exposes only a
	// project's `export fun` targets, read statically from the AST so an import can
	// never trigger a VM load and recurse; an exported VALUE there reads as null and
	// this key would be silently skipped. A policy several projects share is a shared
	// module, not a side effect of one project's magusfile.
	ToolBounds map[string]spells.VersionBounds
	// ReviewRequired are the globs where a person actually reading a change matters, from
	// magus.project's "review_required" key. Empty is the default and means magus reports
	// read receipts without singling anything out.
	//
	// It exists so the finding can be QUIET. "Nobody read this" is true of nearly every
	// file in nearly every changeset, and a report that says so everywhere is one people
	// learn to skip, taking the signing code and the cache-key logic with it. Naming the
	// few places where an unread change is a real risk is what makes the report worth
	// reading, and only the workspace knows which those are.
	//
	// Declared, never inferred. magus could guess from churn or from a security-sounding
	// path, and a guess here would be magus asserting whose code is dangerous, which is
	// the judgment this key exists to leave with the people who own it.
	ReviewRequired []string
	// GateLowRisk are the globs whose changes the ci-gate redundancy check treats
	// as prose (low-risk on their own) from magus.project's "gate_low_risk" key.
	// Globs are project-relative, like ReviewRequired. GateLowRiskDeclared is what
	// separates "not declared" (magus's built-in markdown defaults apply) from
	// "declared empty" (the prose class is off): the moment any project declares
	// the key, the built-in defaults stop applying workspace-wide and only
	// declared globs classify.
	//
	// Only the prose class is a glob list. Generated output stays structural
	// (declared outputs), and comment-only stays a mechanism (a token-stream
	// comparison), because a glob cannot assert either honestly.
	GateLowRisk         []string
	GateLowRiskDeclared bool
	// MergeLowRisk are the project-relative globs of code files a merge may settle
	// without a person, from magus.project's "merge_low_risk" key. A conflicted file
	// MergeThreeWay settles already qualifies when the change classifier classes it low risk
	// (generated, prose, comment-only); this is the opt-in for code. Empty, the
	// default, opts nothing in.
	MergeLowRisk []string
	// Layers maps a WORKSPACE-relative directory or glob to the layer name declared for
	// it, from magus.project's "layers" key. Every entry names at least one existing
	// directory; the load refuses one that does not (LayerDeclarationInvalid).
	Layers map[string]string
	// GateInheritOff is magus.project's "gate_inherit" key declared false: this
	// workspace's CI plan never inherits a green run's verdict, however the
	// delta classifies. One declaration turns it off workspace-wide (the same
	// reach a gate_low_risk declaration has) because inheritance is one
	// decision over the whole plan, not a per-project one.
	GateInheritOff bool
	// IgnoredOptions are magus.project keys this binary did not recognize and dropped,
	// rather than failing the load over (see hint.CheckKeys).
	//
	// Recorded because ignoring a key is not neutral. Several options are opt-OUTS whose
	// absence is the permissive answer: dropping `gate_inherit = false` turns CI verdict
	// inheritance back on, and dropping `gate_low_risk = []` restores the shipped prose
	// globs the author deleted. A binary too old to read the key is exactly the one that
	// cannot know which kind it dropped, so the decision has to be made without knowing:
	// see ci.InheritOff.
	IgnoredOptions []string
	WatchIgnores   []IgnorePattern
	TargetPolicies map[string]Target // per-target execution policy; values carry only the policy fields of Target
	// TargetInputs are per-target file inputs declared in a target body via
	// ctx.readsFiles(...), keyed by normalized target name (DefaultTargetNameNormalizer,
	// matching the TargetPolicies key space buildStep looks up). ONE representation
	// covers both a same-project glob and a cross-project file: each InputRef carries its
	// owning project (workspace-relative once resolved) and the glob/file relative to
	// that project. When present they DEFINE the target's file footprint: buildStep
	// retains the owning magusfiles and target-specific spell sources, then folds these
	// refs to workspace-relative globs via path.Join(Project, Rel). A cross-project input's owning project
	// is also unioned into DependsOn so a change to it marks this project affected; a
	// same-project input needs no such edge (it seeds by directory containment).
	// Populated statically at load from describe.Extract.
	TargetInputs map[string][]InputRef
	// TargetOutputs are per-target ctx.writesFiles refs. When a target has any, they
	// replace the broad project/spell output baseline for that target's replay set.
	TargetOutputs map[string][]OutputRef
	// TargetUpdates are per-target ctx.modifiesExistingFiles refs: existing files the
	// target changes in place.
	// Deliberately absent from AllOutputs, which is what makes magus clean skip them and
	// the cache neither snapshot nor replay them. See types.UpdateRef.
	TargetUpdates map[string][]UpdateRef
	// TargetExecOverrides are per-target ctx.withEnv / ctx.withCwd overrides, in
	// declaration order, folded into the cache key. See TargetGraphNode.ExecOverrides.
	TargetExecOverrides map[string][]string
	// MagusfileTargets are the target names this project's magusfile exports, normalized.
	// They live here rather than on the magusfile spell because that spell is ONE global
	// instance shared by every project, so it cannot know what any particular magusfile
	// declares, which is why its Targets() is empty and why nothing could previously ask
	// whether a magusfile shadows a spell op of the same name.
	MagusfileTargets []string
	// TargetCrossDeps are the cross-project targets each target depends on, declared
	// via a project import (<alias>.<target>). The descendant-write audit reads them:
	// when a parent target depends on a target INSIDE a descendant project, the writes
	// that descendant makes are its own, not the parent reaching across a boundary.
	TargetCrossDeps map[string][]CrossTargetRef
	// TargetChains are each composed target's ctx.needs steps in INVOCATION ORDER, local
	// and cross-project alike. TargetCrossDeps above answers a different question (which
	// other projects a target reaches into) and drops both the ordering and every local
	// step, so it cannot serve this one. See TargetGraphNode.Chain.
	TargetChains map[string][]ChainStep
	// TargetEnvAllow are per-target ctx.env declarations: env var NAMES whose process
	// values fold into the cache key. See TargetGraphNode.EnvAllow.
	TargetEnvAllow map[string][]string
	// TargetObservations are per-target ctx.observes declarations: external facts the
	// target's answer depends on, as "key=value", folded into the cache key. See
	// TargetGraphNode.Observations.
	TargetObservations map[string][]string
	// TargetSpellOps are the spell ops each target's body invokes, statically extracted;
	// see TargetGraphNode.Spells. It is what scopes an observation probe to the targets
	// that actually drive the binary holding the external data: a project-wide
	// observation would put a vulnerability database's publication time into the key of
	// every target in the project, so an unrelated build would miss the cache every six
	// hours.
	TargetSpellOps map[string][]TargetSpellUse
	// DispatchOnlyTargets are the magusfile targets whose body does nothing but call the
	// ops in TargetSpellOps; see TargetGraphNode.DispatchOnly. Normalized names.
	DispatchOnlyTargets []string
	// InboundOutputs are output globs OTHER projects declare INTO this project's tree
	// via ctx.writesFiles(<alias>.file(...)), keyed by the WRITING project's path. Globs are
	// relative to THIS project's root, so they compose with Outputs directly, which is
	// the whole reason they are filed here rather than left on the writer, whose own
	// globs are relative to a different root. Without this a cross-project output would
	// be invisible to every consumer that asks a project what lands in its tree: clean,
	// watch's rebuild-loop guard, ownership lookup, and the merge driver. The writer key
	// is what lets the merge driver regenerate the file, since only the writer can.
	// Populated at load, after the walk, once every project is known (the owner may not
	// be discovered yet when the writer declares it).
	InboundOutputs map[string][]Glob
	ResolvedSpells []*spells.Spell // set at the end of magus.Open; immutable thereafter
}

// AllOutputs is every output glob that lands in this project's tree, deduplicated and
// PROJECT-ROOT RELATIVE: the project-wide Outputs, every per-target ctx.writesFiles glob
// this project declares for itself (TargetOutputs), and every glob another project
// declares into it (InboundOutputs). It is the "what files appear in this tree" view
// (consumed by `magus clean --outputs`, watch's rebuild-loop guard, output-ownership
// lookup, and the merge driver), as opposed to the per-target cache view (buildStep's
// step.Outputs), which stays scoped to the one target being run.
//
// A cross-project ref in TargetOutputs is skipped here and counted on the OWNER instead,
// through that project's InboundOutputs: its glob is relative to the tree it writes
// into, so returning it from the writer would have every caller resolve it against the
// wrong root. The two halves meet on the owner, where the glob is already relative.
//
// The result never aliases p.Outputs. Callers treat it as their own slice (the merge
// driver builds workspace-relative globs from it, clean ranges it), and p.Outputs carries
// spare capacity from AttachSpell, so handing the live backing array out of an exported
// method lets one append reach into the project record.
//
// Every Glob carries its own exclusions, so the union needs no ordering: the result is
// sorted and deduplicated.
func (p *Project) AllOutputs() []Glob {
	out := slices.Clone(p.Outputs)
	for _, refs := range p.TargetOutputs {
		for _, ref := range refs {
			if ref.Project != "" && ref.Project != p.Path {
				continue // written into another tree; that project counts it
			}
			out = append(out, Glob{Pattern: ref.Glob, Except: ref.Except})
		}
	}
	for _, globs := range p.InboundOutputs {
		out = append(out, globs...)
	}
	return CompactGlobs(out)
}

// RootGlob roots a glob declared against projectPath at the WORKSPACE, which is the
// frame every consumer of a declaration matches in: the cache walks from the workspace
// root and yields workspace-relative paths, and DeclaredGlobs and `magus describe file`
// compare against the same.
//
// It CLEANS the join rather than concatenating, and that is the whole point. A
// project-wide source glob may legitimately reach out of its own tree ("../proto/**"
// declared by docs/); reaching across a boundary is what the affordance is FOR, and
// plain concatenation leaves "docs/../proto/**", which matches nothing, because ".." is
// an ordinary path segment to doublestar and no walked path ever contains one. That is
// a declaration that keys nothing and attributes nothing while reading as supported:
// an input that never invalidates. Cleaning resolves it to "proto/**", the spelling the
// walk actually produces.
//
// A glob reaching PAST the workspace root is rejected where it is declared
// (workspace.WithSources), not here: this is a pure path operation with one answer, and
// only the declaration site can name the option that wrote it.
func RootGlob(projectPath, glob string) string {
	if projectPath == "" || projectPath == "." {
		return path.Clean(glob)
	}
	return path.Clean(projectPath + "/" + glob)
}

// Glob is one declared doublestar pattern and the exclusions declared with it.
//
// A magusfile spells an exclusion in-band, as a "!pattern" argument to the same call:
// ctx.writesFiles("gen/*.go", "!gen/runtime.go"). [ParseGlobs] reads that call once,
// where it is declared, into one Glob per positive pattern, each carrying every
// exclusion of the call. Order within the call does not matter, and an exclusion never
// reaches a pattern declared by another call, so a list of Globs is a plain union that
// can be merged, sorted and deduplicated freely. Pattern and Except are bare: no "!".
type Glob struct {
	Pattern string   `json:"pattern" yaml:"pattern"`
	Except  []string `json:"except,omitempty" yaml:"except,omitempty"`
}

// Match reports whether Pattern claims path and no entry of Except does. A literal
// pattern (no glob metacharacter) also claims every file beneath it, on both sides:
// "dist" declares dist/app.js, the way a snapshot walks a named directory, and an
// exclusion of "dist/vendor" carves out dist/vendor/lib.js.
//
// An unparsable pattern matches nothing, as the cache walk treats one; [InvalidGlobs]
// is where it gets reported.
func (g Glob) Match(path string) bool {
	return claims(g.Pattern, path) && !g.Excludes(path)
}

// Excludes reports whether an entry of Except claims path, whether or not Pattern does.
func (g Glob) Excludes(path string) bool {
	return slices.ContainsFunc(g.Except, func(e string) bool { return claims(e, path) })
}

// GlobsOverlap conservatively reports whether two doublestar patterns can match a common
// path. False only when provable: a literal path one side rejects, diverging literal
// prefixes, or incompatible literal filename suffixes. Everything else answers true, so a
// caller ordering on it or keying on it errs toward more, never less.
func GlobsOverlap(a, b string) bool {
	aMeta, bMeta := IsGlobMeta(a), IsGlobMeta(b)
	switch {
	case !aMeta && !bMeta:
		return a == b
	case !aMeta:
		ok, err := doublestar.Match(b, a)
		return ok || err != nil
	case !bMeta:
		ok, err := doublestar.Match(a, b)
		return ok || err != nil
	}
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if IsGlobMeta(as[i]) || IsGlobMeta(bs[i]) {
			break
		}
		if as[i] != bs[i] {
			return false
		}
		// Both consumed a literal segment. The shorter side, exhausted, may name a
		// directory the longer one descends into, which this cannot rule out.
		if i == len(as)-1 || i == len(bs)-1 {
			return true
		}
	}
	if as[len(as)-1] == "**" || bs[len(bs)-1] == "**" {
		return true
	}
	sa, sb := literalSuffix(as[len(as)-1]), literalSuffix(bs[len(bs)-1])
	return strings.HasSuffix(sa, sb) || strings.HasSuffix(sb, sa)
}

// IsGlobMeta reports whether s holds a doublestar metacharacter.
func IsGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[{") }

// literalSuffix returns the literal tail of one glob segment: everything after the last
// metacharacter ("" when the segment ends in one).
func literalSuffix(seg string) string {
	if i := strings.LastIndexAny(seg, "*?[]{}"); i >= 0 {
		return seg[i+1:]
	}
	return seg
}

// Root roots g, declared against projectPath, at the workspace (see [RootGlob]).
func (g Glob) Root(projectPath string) Glob {
	out := Glob{Pattern: RootGlob(projectPath, g.Pattern)}
	if len(g.Except) > 0 {
		out.Except = make([]string, len(g.Except))
		for i, e := range g.Except {
			out.Except[i] = RootGlob(projectPath, e)
		}
	}
	return out
}

// String renders g the way a declaration spells it: the pattern, then each exclusion
// with its "!", space-separated.
func (g Glob) String() string {
	if len(g.Except) == 0 {
		return g.Pattern
	}
	return g.Pattern + " !" + strings.Join(g.Except, " !")
}

// GlobStrings renders each glob with [Glob.String], for a display field that lists one
// declared glob per entry.
func GlobStrings(globs []Glob) []string {
	if globs == nil {
		return nil
	}
	out := make([]string, len(globs))
	for i, g := range globs {
		out[i] = g.String()
	}
	return out
}

// Compare orders globs by pattern, then by exclusions, for sorting and deduplication.
func (g Glob) Compare(other Glob) int {
	if c := strings.Compare(g.Pattern, other.Pattern); c != 0 {
		return c
	}
	return slices.Compare(g.Except, other.Except)
}

// IsLiteralGlob reports whether pattern has no glob metacharacter, so it names one path:
// a file, or a directory whose whole tree it claims. A backslash escape counts as a
// metacharacter, leaving `\!x` to doublestar.
func IsLiteralGlob(pattern string) bool {
	return !strings.ContainsAny(pattern, `*?[{\`)
}

func claims(pattern, path string) bool {
	if IsLiteralGlob(pattern) {
		return path == pattern || strings.HasPrefix(path, pattern+"/")
	}
	ok, _ := doublestar.Match(pattern, path)
	return ok
}

// MatchGlobs reports whether some glob in globs matches path, each under its own
// exclusions: the union every declared glob list means.
func MatchGlobs(globs []Glob, path string) bool {
	return slices.ContainsFunc(globs, func(g Glob) bool { return g.Match(path) })
}

// CompactGlobs sorts globs and drops duplicates in place, returning the shortened slice.
func CompactGlobs(globs []Glob) []Glob {
	slices.SortFunc(globs, Glob.Compare)
	return slices.CompactFunc(globs, func(a, b Glob) bool { return a.Compare(b) == 0 })
}

// ParseGlobs reads the arguments of ONE declaration call, in the magusfile syntax: a
// leading "!" marks an exclusion, and `\!` (doublestar's escape) spells a pattern that
// starts with a literal bang. Every positive pattern becomes a Glob, in the order
// written and each once, carrying all of the call's exclusions, sorted, wherever in the
// call they were written.
//
// It refuses an empty pattern and a call made only of exclusions, since either would be
// kept and match nothing while the author believes it declares or carves out a file.
func ParseGlobs(declared []string) ([]Glob, error) {
	var patterns, except []string
	for _, raw := range declared {
		pattern, exclusion := strings.CutPrefix(raw, "!")
		if pattern == "" {
			return nil, fmt.Errorf("glob %q names no pattern", raw)
		}
		switch {
		case exclusion:
			except = append(except, pattern)
		case !slices.Contains(patterns, pattern):
			patterns = append(patterns, pattern)
		}
	}
	if len(patterns) == 0 && len(except) > 0 {
		return nil, fmt.Errorf("exclusion %q has no glob to narrow", "!"+except[0])
	}
	slices.Sort(except)
	except = slices.Compact(except)
	out := make([]Glob, len(patterns))
	for i, p := range patterns {
		out[i] = Glob{Pattern: p, Except: except}
	}
	return out, nil
}

// MustParseGlobs is ParseGlobs for a declaration fixed at compile time; it panics on a
// malformed one.
func MustParseGlobs(declared ...string) []Glob {
	globs, err := ParseGlobs(declared)
	if err != nil {
		panic(err)
	}
	return globs
}

// InvalidGlobs returns the patterns doublestar cannot parse, exclusions included,
// deduplicated and in the order given. It is what lets a caller SAY that a declaration
// matches nothing before it silently matches nothing for the rest of the run: an
// unparsable glob declares an input that can never key, and MGS1028 would then advise
// declaring a path that is already declared, by a pattern that never matches it.
//
// The error is not returned with it because doublestar has only one (ErrBadPattern, with
// no position), so the pattern itself is the whole of the information.
func InvalidGlobs(globs []Glob) []string {
	var bad []string
	check := func(pattern string) {
		if !doublestar.ValidatePattern(pattern) && !slices.Contains(bad, pattern) {
			bad = append(bad, pattern)
		}
	}
	for _, g := range globs {
		check(g.Pattern)
		for _, e := range g.Except {
			check(e)
		}
	}
	return bad
}

// DeclaredGlobs is every glob this project declares, rooted at the WORKSPACE rather
// than at the project: the project-wide Sources and AllOutputs, plus the per-target
// ctx.readsFiles, ctx.writesFiles, and ctx.modifiesExistingFiles refs, each anchored
// on the project its glob is relative to. Sorted and deduplicated.
//
// It answers "does this project declare that path", which is the question affected
// attribution asks before falling back to directory containment, and the one doctor
// asks about the tree standing still. The rooting goes through RootGlob, which is also
// what the cache step and `magus describe file` root with, so the three agreeing is a
// shared function rather than three parallel implementations that happen to match.
// Measured, they do not: plain concatenation leaves a reaching "../" glob at a path
// nothing can match, while joining the same glob with filepath.Join resolves it.
//
// Dedup compares the ROOTED form, so two spellings that resolve to one path collapse to
// one entry: a project-wide "../proto/**" and a per-target ctx.readsFiles of proto's
// "**" are the same declaration and count once.
//
// Deliberately NOT the magusfile globs the cache step layers on top. Every project's
// key carries the ROOT magusfile, so counting those here would make one magusfile
// edit read as a declaration by every project in the workspace, and attribution
// would then seed all of them where directory containment seeds exactly one.
func (p *Project) DeclaredGlobs() []Glob {
	var out []Glob
	for _, g := range p.Sources {
		out = append(out, g.Root(p.Path))
	}
	for _, g := range p.AllOutputs() {
		out = append(out, g.Root(p.Path))
	}
	for _, refs := range p.TargetInputs {
		for _, ref := range refs {
			out = append(out, ref.Rooted(p.Path))
		}
	}
	for _, refs := range p.TargetOutputs {
		for _, ref := range refs {
			out = append(out, ref.Rooted(p.Path))
		}
	}
	for _, refs := range p.TargetUpdates {
		for _, ref := range refs {
			out = append(out, ref.Rooted(p.Path))
		}
	}
	return CompactGlobs(out)
}

// SpellGlobs parses what a spell contributes to every project it binds: its Sources and
// its Outputs, each one declaration (see [ParseGlobs]).
func SpellGlobs(spell *spells.Spell) (sources, outputs []Glob, err error) {
	if sources, err = ParseGlobs(spell.Sources()); err != nil {
		return nil, nil, fmt.Errorf("spell %q: sources: %w", spell.Name(), err)
	}
	if outputs, err = ParseGlobs(spell.Outputs()); err != nil {
		return nil, nil, fmt.Errorf("spell %q: outputs: %w", spell.Name(), err)
	}
	return sources, outputs, nil
}

// AttachSpell associates spell with p without applying registration overrides. Its
// callers re-attach spells a registration already bound, so a contribution
// [SpellGlobs] refuses was refused there, and one that fails here contributes nothing.
func (p *Project) AttachSpell(spell *spells.Spell) {
	// Internal plumbing never claims the primary slot; see the same rule in
	// magus.bindSpell. The magusfile registration attaches on every project (it is
	// how a project is discovered), so it won this race everywhere and `magus ls`
	// answered "spell: magusfile" for almost every project: true by construction,
	// and therefore no answer at all.
	if p.Spell == "" && !spell.Internal() {
		p.Spell = spell.Name()
	}
	p.Spells = append(p.Spells, spell.Name())
	p.Bindings = append(p.Bindings, &Binding{Name: spell.Name()})
	if sources, outputs, err := SpellGlobs(spell); err == nil {
		p.Sources = append(p.Sources, sources...)
		p.Outputs = append(p.Outputs, outputs...)
	}
}

// layerNameRe is the layer-name grammar: a lowercase slug, so `layer=<name>` in a query
// and a Buzz string compare it without quoting or case folding.
var layerNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// CheckLayer validates the shape of one magus.project "layers" entry: dir is a clean
// workspace-relative directory or glob inside the workspace, and name matches the layer
// grammar. It does not check that dir exists, so it needs no filesystem. Errors carry
// LayerDeclarationInvalid.
func CheckLayer(dir, name string) error {
	fail := func(format string, args ...any) error {
		return DiagnosticErrorf(LayerDeclarationInvalid, `magus.project: "layers"[%q]: `+format, append([]any{dir}, args...)...)
	}
	switch {
	case strings.TrimSpace(dir) == "":
		return DiagnosticErrorf(LayerDeclarationInvalid, `magus.project: "layers" has a blank directory; name one relative to the workspace root, e.g. "internal/handler"`)
	case path.IsAbs(dir) || filepath.IsAbs(dir):
		return fail("is absolute; name the directory relative to the workspace root")
	case dir == ".." || strings.HasPrefix(dir, "../"):
		return fail("escapes the workspace root")
	case path.Clean(dir) != dir:
		return fail("is not in clean form; write %q", path.Clean(dir))
	case !doublestar.ValidatePattern(dir):
		return fail("is not a valid glob")
	case !layerNameRe.MatchString(name):
		return fail("layer name %q must be a lowercase slug matching %s, e.g. \"handler\"", name, layerNameRe)
	}
	return nil
}

// LayerFor reports the layer layers declares for the workspace-relative directory dir.
// An exact path names that one directory; a glob names every directory it matches
// ("internal/handler/**" includes internal/handler itself). Where several entries match,
// an exact path wins, then the longest pattern, then the lexically first, so every
// reader of one declaration agrees.
func LayerFor(layers map[string]string, dir string) (string, bool) {
	best, found := "", false
	for pattern := range layers {
		exact := pattern == dir
		if !exact {
			if IsLiteralGlob(pattern) {
				continue
			}
			if ok, _ := doublestar.Match(pattern, dir); !ok {
				continue
			}
		}
		if !found || layerPatternBeats(pattern, best, dir) {
			best, found = pattern, true
		}
	}
	return layers[best], found
}

func layerPatternBeats(a, b, dir string) bool {
	if (a == dir) != (b == dir) {
		return a == dir
	}
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a < b
}
