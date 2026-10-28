package spells

import (
	"slices"

	"github.com/egladman/magus/types/enum"
)

// DiscoverProjectsContract is the contract function a language spell exports to report
// the projects its markers form, for `magus init` to write one magusfile per project:
//
//	fun discover_projects(target: Target, req: DiscoveryQuery) > [DiscoveredProject] !> any
//
// Not mgs_-prefixed, because it takes a request. Unlike [ListProjectsContract] it is
// pure: core reads the files and passes them in, so it needs no toolchain. A spell that
// does not export it is never asked.
const DiscoverProjectsContract = "discover_projects"

// DiscoveryQuery is the request [DiscoverProjectsContract] receives.
type DiscoveryQuery struct {
	// Root is the absolute workspace root, for messages only: every path the spell
	// reads or reports is relative to it.
	Root string `json:"root"`
	// Files is every path matching the spell's required globs, with the union of every
	// asked spell's ignore dirs pruned, sorted by path.
	Files []DiscoveryFile `json:"files"`
}

// DiscoveryFile is one path a [DiscoveryQuery] carries.
type DiscoveryFile struct {
	// Path is relative to the workspace root, forward slashes.
	Path string `json:"path"`
	// Content is set only when the glob that matched has a literal basename (go.mod,
	// package.json): such a file is a marker the spell parses. A wildcard glob passes
	// the path alone, so content is never read for every source file.
	Content *string `json:"content"`
}

// DiscoveryRole says what a [DiscoveredProject] claim does at its path.
type DiscoveryRole string

const (
	// DiscoveryRoleProject forms a project at the path.
	DiscoveryRoleProject DiscoveryRole = "project"
	// DiscoveryRoleMember binds the spell to the project owning the path by longest
	// prefix, forming the root project when none does. Shell scripts are members: they
	// never mark a project boundary of their own.
	DiscoveryRoleMember DiscoveryRole = "member"
)

var discoveryRoles = enum.Set[DiscoveryRole]{DiscoveryRoleProject, DiscoveryRoleMember}

func (v DiscoveryRole) Values() []string { return discoveryRoles.Strings() }
func (v DiscoveryRole) Valid() bool      { return discoveryRoles.Valid(v) }
func (v DiscoveryRole) String() string   { return enum.String(v) }

// DiscoveredProject is one claim a spell's [DiscoverProjectsContract] answer makes. Claims
// from several spells on one directory merge into one project with every claiming spell
// bound; same-named targets run in spell-name order.
type DiscoveredProject struct {
	// Path is the claimed directory relative to the workspace root, forward slashes;
	// "." is the root.
	Path string `json:"path"`
	// Role is required: the zero value is refused rather than read as a project.
	Role DiscoveryRole `json:"role"`
	// Name is the project's label; empty derives it from the path.
	Name string `json:"name"`
	// DependsOn names upstream project directories, relative to the workspace root.
	DependsOn []string `json:"dependsOn"`
	// Targets maps a name from [DiscoveryTargets] to this spell's op keys, run in order:
	// {"test": ["go-test"]}. ci is absent by contract: core composes it from the others.
	Targets map[string][]string `json:"targets"`
	// Spans names directories, relative to the workspace root, that this tool's
	// boundary covers and another project may claim (go ./... compiling web/embed.go).
	// Core turns each claimed one into a dependency of this project on that one.
	Spans []string `json:"spans"`
	// NotInferred is what the spell saw and could not turn into a target; each one is
	// reported as a gap naming the file.
	NotInferred []DiscoveryGap `json:"notInferred"`
}

// DiscoveryGap is one thing discovery saw and did not wire.
type DiscoveryGap struct {
	// File is the path that shows the gap, relative to the workspace root.
	File string `json:"file"`
	// Why says what was not inferred and why, as a phrase: "scripts.dev: meaning unknown".
	Why string `json:"why"`
}

// discoveryTargets is ordered by phase, the order a rendered magusfile declares them in.
var discoveryTargets = []string{"install", "generate", "format", "lint", "build", "test", "security"}

// DiscoveryTargets returns the target names a [DiscoveredProject] may key Targets by,
// in phase order. It is a subset of the conventional target names: clean is left out
// because no marker says how to clean, and ci because core composes it.
func DiscoveryTargets() []string { return slices.Clone(discoveryTargets) }
