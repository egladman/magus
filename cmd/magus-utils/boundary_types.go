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
