package main

//go:generate go run . enums -package types -out ../../types/enum_gen.go
//go:generate go run . enums -package spells -out ../../spells/enum_gen.go

import (
	"reflect"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// boundaryTypes is the one declaration of every Go value that crosses the
// typed Buzz boundary. The mirror generator reads every entry; runtimeObject
// marks the values returned by a host method and therefore requiring a direct
// Go-to-Buzz encoder too. Keeping the two outputs on this one registry means a
// new mirror cannot silently describe a shape the runtime fails to produce.
var boundaryTypes = []boundaryType{
	{Name: "Path", Type: reflect.TypeFor[types.Path]()},
	{Name: "Target", Type: reflect.TypeFor[types.Target]()},
	{Name: "Command", Type: reflect.TypeFor[spells.Command]()},
	{Name: "Service", Type: reflect.TypeFor[spells.Service]()},
	{Name: "Charm", Type: reflect.TypeFor[spells.Charm]()},
	{Name: "PatchOp", Type: reflect.TypeFor[spells.PatchOp]()},
	{Name: "VersionKey", Type: reflect.TypeFor[spells.VersionKey]()},
	{Name: "ExecResult", Type: reflect.TypeFor[types.ExecResult](), RuntimeObject: true},
	{Name: "CommitAuthor", Type: reflect.TypeFor[types.CommitAuthor](), RuntimeObject: true},
	{Name: "Commit", Type: reflect.TypeFor[types.CommitRecord](), RuntimeObject: true},
	{Name: "FileInfo", Type: reflect.TypeFor[types.FileInfo](), RuntimeObject: true},
	{Name: "HttpResponse", Type: reflect.TypeFor[types.HTTPResponse](), RuntimeObject: true},
	{Name: "SemverVersion", Type: reflect.TypeFor[types.SemverVersion](), RuntimeObject: true},
	{Name: "SemverNext", Type: reflect.TypeFor[types.SemverNext](), RuntimeObject: true},
	{Name: "URL", Type: reflect.TypeFor[types.URL](), RuntimeObject: true},
	{Name: "Tag", Type: reflect.TypeFor[types.VCSTag](), RuntimeObject: true},
	{Name: "Affected", Type: reflect.TypeFor[types.AffectedResult](), RuntimeObject: true},
	{Name: "Graph", Type: reflect.TypeFor[types.GraphView](), RuntimeObject: true},
	{Name: "ModuleFieldEntry", Type: reflect.TypeFor[types.ModuleFieldEntry](), RuntimeObject: true},
	{Name: "ModuleMethodEntry", Type: reflect.TypeFor[types.ModuleMethodEntry](), RuntimeObject: true},
	{Name: "Module", Type: reflect.TypeFor[types.ModuleEntry](), RuntimeObject: true},
	{Name: "ProjectEntry", Type: reflect.TypeFor[types.ProjectEntry](), RuntimeObject: true},
	{Name: "Projects", Type: reflect.TypeFor[types.ProjectsOutput](), RuntimeObject: true},
	{Name: "CrossTargetRef", Type: reflect.TypeFor[types.CrossTargetRef](), RuntimeObject: true},
	{Name: "TargetSpellUse", Type: reflect.TypeFor[types.TargetSpellUse](), RuntimeObject: true},
	{Name: "InputRef", Type: reflect.TypeFor[types.InputRef](), RuntimeObject: true},
	{Name: "OutputRef", Type: reflect.TypeFor[types.OutputRef](), RuntimeObject: true},
	{Name: "UpdateRef", Type: reflect.TypeFor[types.UpdateRef](), RuntimeObject: true},
	{Name: "TargetGraphNode", Type: reflect.TypeFor[types.TargetGraphNode](), RuntimeObject: true},
	{Name: "TargetGraphProject", Type: reflect.TypeFor[types.TargetGraphProject](), RuntimeObject: true},
	{Name: "TargetGraph", Type: reflect.TypeFor[types.TargetGraphOutput](), RuntimeObject: true},
	// A run and the targets in it, as magus already models them for `magus status`:
	// per-target state (queued/running/passed/failed/cached), duration, and the output
	// ref each one minted. Mirrored so a caller can ITERATE a run - `t.state == "failed"`
	// then `t.outputRef` - instead of parsing magus's own console output back out of a
	// string. TargetRun precedes Run: Run.targets is a list of it.
	// Leaf-first: an envelope's list field mirrors as its element's Buzz name, which
	// must already be declared.
	{Name: "FileEntry", Type: reflect.TypeFor[types.FileEntry](), RuntimeObject: true},
	{Name: "FileReport", Type: reflect.TypeFor[types.FileReport](), RuntimeObject: true},
	{Name: "DoctorCheck", Type: reflect.TypeFor[types.DoctorCheck](), RuntimeObject: true},
	{Name: "DoctorSummary", Type: reflect.TypeFor[types.DoctorSummary](), RuntimeObject: true},
	{Name: "DoctorReport", Type: reflect.TypeFor[types.DoctorReport](), RuntimeObject: true},
	// magus.insightReport's bundle, leaf-first. A bundle takes the short plural name
	// and its element the *Entry suffix, the shape Projects/ProjectEntry already set,
	// which is also what keeps Go's Ownership and OwnershipOutput from colliding here.
	{Name: "Node", Type: reflect.TypeFor[types.Node](), RuntimeObject: true},
	{Name: "FileHotspot", Type: reflect.TypeFor[types.FileHotspot](), RuntimeObject: true},
	{Name: "Hotspots", Type: reflect.TypeFor[types.HotspotOutput](), RuntimeObject: true},
	{Name: "CoChange", Type: reflect.TypeFor[types.CoChange](), RuntimeObject: true},
	{Name: "Affinity", Type: reflect.TypeFor[types.AffinityOutput](), RuntimeObject: true},
	{Name: "OwnershipEntry", Type: reflect.TypeFor[types.Ownership](), RuntimeObject: true},
	{Name: "Ownership", Type: reflect.TypeFor[types.OwnershipOutput](), RuntimeObject: true},
	{Name: "TrendEntry", Type: reflect.TypeFor[types.Trend](), RuntimeObject: true},
	{Name: "Trend", Type: reflect.TypeFor[types.TrendOutput](), RuntimeObject: true},
	{Name: "VolatilityTarget", Type: reflect.TypeFor[types.VolatilityTarget](), RuntimeObject: true},
	{Name: "Volatility", Type: reflect.TypeFor[types.VolatilityReport](), RuntimeObject: true},
	{Name: "KnowledgeGodNode", Type: reflect.TypeFor[types.KnowledgeGodNode](), RuntimeObject: true},
	{Name: "KnowledgeOrphan", Type: reflect.TypeFor[types.KnowledgeOrphan](), RuntimeObject: true},
	{Name: "KnowledgeDocCoverage", Type: reflect.TypeFor[types.KnowledgeDocCoverage](), RuntimeObject: true},
	{Name: "KnowledgeStats", Type: reflect.TypeFor[types.KnowledgeStats](), RuntimeObject: true},
	{Name: "InsightReport", Type: reflect.TypeFor[types.InsightReport](), RuntimeObject: true},
	// magus.affectedImpact's report, leaf-first.
	{Name: "ImpactCoverage", Type: reflect.TypeFor[types.ImpactCoverage](), RuntimeObject: true},
	{Name: "ImpactSymbol", Type: reflect.TypeFor[types.ImpactSymbol](), RuntimeObject: true},
	{Name: "ImpactFileCoverage", Type: reflect.TypeFor[types.ImpactFileCoverage](), RuntimeObject: true},
	{Name: "ImpactProject", Type: reflect.TypeFor[types.ImpactProject](), RuntimeObject: true},
	{Name: "Impact", Type: reflect.TypeFor[types.ImpactResult](), RuntimeObject: true},
	{Name: "TargetRun", Type: reflect.TypeFor[types.StatusTargetRun](), RuntimeObject: true},
	{Name: "Run", Type: reflect.TypeFor[types.StatusRun](), RuntimeObject: true},
}

// boundaryEnums declares the Go named string types that mirror as Buzz `enum<str>`
// rather than as a bare `str`.
//
// The cases are listed here rather than derived because reflect cannot enumerate a
// named type's constants - it sees only the underlying kind. That is the whole reason
// a registry exists: without it every one of these crosses as an untyped string, and a
// magusfile typo is a silent miss instead of a compile error.
//
// A case must be a legal Buzz identifier, and the first entry is the field's default,
// so a zero-valued case belongs first.
var boundaryEnums = []boundaryEnum{
	// A case NAME must be a legal Buzz identifier while its VALUE is whatever the Go
	// constant carries, which is why the two are separate: "up-to-date" and
	// "both-deleted" are perfectly good JSON and impossible identifiers.
	{
		Name:  "DoctorCheckStatus",
		Type:  reflect.TypeFor[types.DoctorCheckStatus](),
		Cases: []enumCase{{"none", ""}, {"ok", "ok"}, {"fail", "fail"}, {"advice", "advice"}},
	},
	{
		Name: "EventOutcome",
		Type: reflect.TypeFor[types.EventOutcome](),
		Cases: []enumCase{{"none", ""}, {"waiting", "waiting"}, {"permission", "permission"}, {"failed", "failed"},
			{"finished", "finished"}, {"diagnostic", "diagnostic"}, {"update", "update"}, {"other", "other"}},
	},
	{
		Name: "EventSeverity",
		Type: reflect.TypeFor[types.EventSeverity](),
		Cases: []enumCase{{"none", ""}, {"info", "info"}, {"notice", "notice"}, {"warning", "warning"},
			{"critical", "critical"}},
	},
	{
		Name: "ServiceState",
		Type: reflect.TypeFor[types.ServiceState](),
		Cases: []enumCase{{"none", ""}, {"starting", "starting"}, {"running", "running"},
			{"idle", "idle"}, {"failed", "failed"}},
	},
	{
		Name:  "PatternType",
		Type:  reflect.TypeFor[types.PatternType](),
		Cases: []enumCase{{"none", ""}, {"glob", "glob"}, {"regex", "regex"}, {"literal", "literal"}},
	},
	{
		Name: "SymbolIndexFreshness",
		Type: reflect.TypeFor[types.SymbolIndexFreshness](),
		Cases: []enumCase{{"none", ""}, {"upToDate", "up-to-date"}, {"outOfDate", "out-of-date"},
			{"notIndexed", "not-indexed"}},
	},
	{
		Name: "TargetRunState",
		Type: reflect.TypeFor[types.TargetRunState](),
		Cases: []enumCase{{"none", ""}, {"queued", "queued"}, {"running", "running"}, {"passed", "passed"},
			{"failed", "failed"}, {"cached", "cached"}},
	},
	{
		Name: "VCSSource",
		Type: reflect.TypeFor[types.VCSSource](),
		Cases: []enumCase{{"none", ""}, {"explicit", "explicit"}, {"auto", "auto"}, {"default", "default"},
			{"disabled", "disabled"}},
	},
	{
		Name: "ConflictKind",
		Type: reflect.TypeFor[types.ConflictKind](),
		Cases: []enumCase{{"none", ""}, {"content", "content"}, {"deleted", "deleted"},
			{"bothDeleted", "both-deleted"}},
	},
	{
		Name: "PatchOpKind",
		Type: reflect.TypeFor[spells.PatchOpKind](),
		Cases: []enumCase{{"none", ""}, {"add", "add"}, {"remove", "remove"},
			{"replace", "replace"}, {"move", "move"}, {"copy", "copy"}, {"test", "test"}},
	},
	{
		Name:  "VersionComponent",
		Type:  reflect.TypeFor[spells.VersionComponent](),
		Cases: []enumCase{{"none", ""}, {"major", "major"}, {"minor", "minor"}, {"patch", "patch"}},
	},
}

type boundaryEnum struct {
	Name  string
	Type  reflect.Type
	Cases []enumCase
}

// enumCase is one case: the Buzz identifier and the string it carries.
type enumCase struct {
	Name  string
	Value string
}

// buzzEnumFor returns the enum a Go type mirrors as, if any.
func buzzEnumFor(rt reflect.Type) (boundaryEnum, bool) {
	for _, e := range boundaryEnums {
		if e.Type == rt {
			return e, true
		}
	}
	return boundaryEnum{}, false
}

type boundaryType struct {
	Name          string
	Type          reflect.Type
	RuntimeObject bool
}

// buzzNameFor returns the BUZZ name a Go type mirrors as, which is the registry key
// and not always the Go type's own name: types.ProjectsOutput is `Projects`,
// types.StatusTargetRun is `TargetRun`. A struct-valued field must reference the Buzz
// name, so the registry is what resolves it - falling back to the Go name only for a
// type no entry claims, which the caller then reports as undeclared.
func buzzNameFor(rt reflect.Type) string {
	for _, entry := range boundaryTypes {
		if entry.Type == rt {
			return entry.Name
		}
	}
	return rt.Name()
}

func boundaryTypeNamed(name string) (boundaryType, bool) {
	for _, entry := range boundaryTypes {
		if entry.Name == name {
			return entry, true
		}
	}
	return boundaryType{}, false
}
