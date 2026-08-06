package main

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
	{Name: "ExecResult", Type: reflect.TypeFor[types.ExecResult](), RuntimeObject: true},
	{Name: "CommitAuthor", Type: reflect.TypeFor[types.CommitAuthor](), RuntimeObject: true},
	{Name: "Commit", Type: reflect.TypeFor[types.CommitRecord](), RuntimeObject: true},
	{Name: "Status", Type: reflect.TypeFor[types.StatusRecord](), RuntimeObject: true},
	{Name: "DriftVerdict", Type: reflect.TypeFor[types.DriftVerdictRecord](), RuntimeObject: true},
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
	// magus.insightReport's bundle, leaf-first. Every ELEMENT row below is an identity:
	// the Go name and the Buzz name agree, so only the bundle rows rename across the
	// boundary (Output -> the short plural name), the shape Projects/ProjectsOutput set.
	//
	// The element NAMES are deliberately not uniform, and this is the honest version of a
	// comment that used to claim they were. OwnershipEntry and TrendEntry take the *Entry
	// suffix because the bare noun is the bundle's name and the two would collide;
	// FileHotspot and CoChange are domain nouns that say more than "entry" would, and
	// nothing collides, so they keep their own names. Renaming either pair would change a
	// PUBLIC Buzz object name that magusfiles annotate with, which is why the split
	// stands rather than being tidied.
	{Name: "Node", Type: reflect.TypeFor[types.Node](), RuntimeObject: true},
	{Name: "FileHotspot", Type: reflect.TypeFor[types.FileHotspot](), RuntimeObject: true},
	{Name: "Hotspots", Type: reflect.TypeFor[types.HotspotOutput](), RuntimeObject: true},
	{Name: "CoChange", Type: reflect.TypeFor[types.CoChange](), RuntimeObject: true},
	{Name: "Affinity", Type: reflect.TypeFor[types.AffinityOutput](), RuntimeObject: true},
	{Name: "OwnershipEntry", Type: reflect.TypeFor[types.OwnershipEntry](), RuntimeObject: true},
	{Name: "Ownership", Type: reflect.TypeFor[types.OwnershipOutput](), RuntimeObject: true},
	{Name: "TrendEntry", Type: reflect.TypeFor[types.TrendEntry](), RuntimeObject: true},
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
