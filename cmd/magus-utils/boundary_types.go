package main

//go:generate go run . boundarylist -out ../../internal/interp/bindings/gen/boundary_list.go
//go:generate go run . boundaryobjects -out ../../internal/interp/bindings/gen/boundary_objects.go

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
	// Leaf first: Command.hints is [Hint], so Hint must already be declared.
	{Name: "Hint", Type: reflect.TypeFor[spells.Hint]()},
	// RuntimeObject on both: a resolved spell is handed back to Buzz as a handle, and the
	// encoder for that has to be GENERATED from this registry rather than hand-written
	// beside it. A hand-written one drops a field the moment the struct grows one, silently
	// and only on the round trip, which is how SymbolIndexer went missing from a handle
	// while the decoder still demanded it.
	{Name: "Command", Type: reflect.TypeFor[spells.Command](), RuntimeObject: true},
	// Must follow Command (Install.command is one) and precede Manifest (Manifest.installs
	// is {str: Install}).
	{Name: "Install", Type: reflect.TypeFor[spells.Install]()},
	{Name: "Manifest", Type: reflect.TypeFor[spells.Manifest]()},
	{Name: "Service", Type: reflect.TypeFor[spells.Service]()},
	// Must follow Command: SymbolIndexer.command is one.
	{Name: "SymbolIndexer", Type: reflect.TypeFor[spells.SymbolIndexer](), RuntimeObject: true},
	{Name: "Charm", Type: reflect.TypeFor[spells.Charm]()},
	{Name: "PatchOp", Type: reflect.TypeFor[spells.PatchOp]()},
	{Name: "VersionKey", Type: reflect.TypeFor[spells.VersionKey]()},
	{Name: "VersionBounds", Type: reflect.TypeFor[spells.VersionBounds]()},
	{Name: "Tool", Type: reflect.TypeFor[spells.Tool]()},
	// Leaves first: CommentSyntax carries [CommentBlock] and [Quote], and
	// Language carries a CommentSyntax.
	{Name: "CommentBlock", Type: reflect.TypeFor[spells.CommentBlock]()},
	{Name: "Quote", Type: reflect.TypeFor[spells.Quote]()},
	{Name: "CommentSyntax", Type: reflect.TypeFor[spells.CommentSyntax]()},
	{Name: "Language", Type: reflect.TypeFor[spells.Language]()},
	// A provider spell WRITES this one, like Project below: resolve_secret constructs it
	// and returns it, so it needs the mirror but no Go-to-Buzz encoder.
	{Name: "Secret", Type: reflect.TypeFor[spells.Secret]()},
	// A spell WRITES this one, so it takes the bare Buzz name; the Go side carries
	// the adjective because types.Project and types.ProjectEntry already exist. The
	// registry keys on the Buzz name, which is what makes that split expressible.
	{Name: "Project", Type: reflect.TypeFor[spells.ProvidedProject]()},
	{Name: "ExecResult", Type: reflect.TypeFor[types.ExecResult](), RuntimeObject: true},
	{Name: "ShellCommand", Type: reflect.TypeFor[types.ShellCommand](), RuntimeObject: true},
	{Name: "CommitAuthor", Type: reflect.TypeFor[types.CommitAuthor](), RuntimeObject: true},
	{Name: "Commit", Type: reflect.TypeFor[types.CommitRecord](), RuntimeObject: true},
	{Name: "Status", Type: reflect.TypeFor[types.StatusRecord](), RuntimeObject: true},
	{Name: "DriftResult", Type: reflect.TypeFor[types.DriftResultRecord](), RuntimeObject: true},
	{Name: "FileInfo", Type: reflect.TypeFor[types.FileInfo](), RuntimeObject: true},
	{Name: "UncompressResult", Type: reflect.TypeFor[types.UncompressResult](), RuntimeObject: true},
	{Name: "ArchiveEntry", Type: reflect.TypeFor[types.ArchiveEntry](), RuntimeObject: true},
	{Name: "CompressResult", Type: reflect.TypeFor[types.CompressResult](), RuntimeObject: true},
	{Name: "HttpResponse", Type: reflect.TypeFor[types.HTTPResponse](), RuntimeObject: true},
	{Name: "TermSize", Type: reflect.TypeFor[types.TermSize](), RuntimeObject: true},
	// NOT a RuntimeObject: HttpRetry only ever crosses INBOUND (a magusfile hands
	// one in and the Impl receives the plain map), so nothing on the Go side has to
	// encode one back out. Registering it here is purely so its declaration travels
	// with the http module's signatures.
	{Name: "HttpRetry", Type: reflect.TypeFor[types.HTTPRetry]()},
	{Name: "SemverVersion", Type: reflect.TypeFor[types.SemverVersion](), RuntimeObject: true},
	{Name: "SemverNext", Type: reflect.TypeFor[types.SemverNext](), RuntimeObject: true},
	{Name: "URL", Type: reflect.TypeFor[types.URL](), RuntimeObject: true},
	{Name: "FlagParse", Type: reflect.TypeFor[types.FlagParse](), RuntimeObject: true},
	{Name: "PipeRecord", Type: reflect.TypeFor[types.PipeRecord](), RuntimeObject: true},
	{Name: "Artifact", Type: reflect.TypeFor[types.TargetArtifact](), RuntimeObject: true},
	{Name: "ArtifactVersion", Type: reflect.TypeFor[types.ArtifactVersion](), RuntimeObject: true},
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
	{Name: "ChainStep", Type: reflect.TypeFor[types.ChainStep](), RuntimeObject: true},
	{Name: "TargetGraphNode", Type: reflect.TypeFor[types.TargetGraphNode](), RuntimeObject: true},
	{Name: "TargetGraphProject", Type: reflect.TypeFor[types.TargetGraphProject](), RuntimeObject: true},
	{Name: "TargetGraph", Type: reflect.TypeFor[types.TargetGraphOutput](), RuntimeObject: true},
	// Leaf first: FileEntry.claims and FileReport.overlaps are both [FileClaim].
	{Name: "FileClaim", Type: reflect.TypeFor[types.FileClaim](), RuntimeObject: true},
	{Name: "FileEntry", Type: reflect.TypeFor[types.FileEntry](), RuntimeObject: true},
	{Name: "FileReport", Type: reflect.TypeFor[types.FileReport](), RuntimeObject: true},
	// magus.review's bundle, leaf-first. A Buzz advisor annotating `> Review` gets
	// compile-checked field access on the same shape the console and the CLI read, which is
	// what keeps one definition of review order serving all three.
	// DiffSymbol.checks are [Check], shared with DoctorReport.
	{Name: "Check", Type: reflect.TypeFor[types.Check](), RuntimeObject: true},
	{Name: "DiffSymbol", Type: reflect.TypeFor[types.DiffSymbol](), RuntimeObject: true},
	{Name: "DiffChurn", Type: reflect.TypeFor[types.DiffChurn](), RuntimeObject: true},
	{Name: "DiffTouch", Type: reflect.TypeFor[types.DiffTouch](), RuntimeObject: true},
	{Name: "DiffFile", Type: reflect.TypeFor[types.DiffFile](), RuntimeObject: true},
	{Name: "DiffReviewed", Type: reflect.TypeFor[types.DiffReviewed](), RuntimeObject: true},
	{Name: "DiffAPI", Type: reflect.TypeFor[types.DiffAPI](), RuntimeObject: true},
	{Name: "VCSCheckpoint", Type: reflect.TypeFor[types.VCSCheckpoint](), RuntimeObject: true},
	// A thrown error's shape, and also returned: Diff.conformanceError is one.
	{Name: "Diagnostic", Type: reflect.TypeFor[types.Diagnostic](), RuntimeObject: true},
	{Name: "DiffUncovered", Type: reflect.TypeFor[types.DiffUncovered](), RuntimeObject: true},
	{Name: "Diff", Type: reflect.TypeFor[types.Diff](), RuntimeObject: true},
	{Name: "DoctorSummary", Type: reflect.TypeFor[types.DoctorSummary](), RuntimeObject: true},
	{Name: "DoctorReport", Type: reflect.TypeFor[types.DoctorReport](), RuntimeObject: true},
	// magus.insight's bundle, leaf-first. Element names are not uniform on purpose:
	// *Entry only where the bare noun collides with the bundle's own name. These are
	// public Buzz names magusfiles annotate with, so do not tidy them.
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
	// Not RuntimeObject: unlike their insight-bundle siblings above, nothing declares
	// these for Buzz (no gen/decls entry reaches them), so no method call ever surfaces
	// one as a typed return; module_decls.go's KnowledgeGodNode comment is the fossil
	// of that gap (an `in:` field that shipped unparsable because nothing checked it).
	{Name: "KnowledgeGodNode", Type: reflect.TypeFor[types.KnowledgeGodNode]()},
	{Name: "KnowledgeOrphan", Type: reflect.TypeFor[types.KnowledgeOrphan]()},
	{Name: "KnowledgeDocCoverage", Type: reflect.TypeFor[types.KnowledgeDocCoverage]()},
	{Name: "KnowledgeStats", Type: reflect.TypeFor[types.KnowledgeStats]()},
	{Name: "ProjectRef", Type: reflect.TypeFor[types.ProjectRef](), RuntimeObject: true},
	{Name: "KnowledgeSymbolGap", Type: reflect.TypeFor[types.KnowledgeSymbolGap](), RuntimeObject: true},
	// Registered because KnowledgeAnswer carries it: a struct field on a registered Buzz
	// object must itself be registered, or the generated encoder calls one the field's
	// type does not have.
	{Name: "KnowledgeTextPresence", Type: reflect.TypeFor[types.KnowledgeTextPresence](), RuntimeObject: true},
	{Name: "KnowledgeAnswer", Type: reflect.TypeFor[types.KnowledgeAnswer](), RuntimeObject: true},
	{Name: "UnreferencedEntry", Type: reflect.TypeFor[types.UnreferencedEntry](), RuntimeObject: true},
	{Name: "Unreferenced", Type: reflect.TypeFor[types.UnreferencedOutput](), RuntimeObject: true},
	{Name: "DuplicationSite", Type: reflect.TypeFor[types.DuplicationSite](), RuntimeObject: true},
	{Name: "DuplicationGroup", Type: reflect.TypeFor[types.DuplicationGroup](), RuntimeObject: true},
	{Name: "DuplicationHistory", Type: reflect.TypeFor[types.DuplicationHistory](), RuntimeObject: true},
	{Name: "Duplication", Type: reflect.TypeFor[types.DuplicationOutput](), RuntimeObject: true},
	{Name: "InsightReport", Type: reflect.TypeFor[types.InsightReport](), RuntimeObject: true},
	{Name: "ImpactCoverage", Type: reflect.TypeFor[types.ImpactCoverage](), RuntimeObject: true},
	{Name: "ImpactSymbol", Type: reflect.TypeFor[types.ImpactSymbol](), RuntimeObject: true},
	{Name: "ImpactFileCoverage", Type: reflect.TypeFor[types.ImpactFileCoverage](), RuntimeObject: true},
	{Name: "ImpactProject", Type: reflect.TypeFor[types.ImpactProject](), RuntimeObject: true},
	{Name: "Impact", Type: reflect.TypeFor[types.ImpactResult](), RuntimeObject: true},
	{Name: "TargetRun", Type: reflect.TypeFor[types.StatusTargetRun](), RuntimeObject: true},
	{Name: "Run", Type: reflect.TypeFor[types.StatusRun](), RuntimeObject: true},
	// magus\job's bundle (put/list), leaf-first: Job.releases and
	// JobList.overlaps are each a list of the other two, and Job.registeredBy is
	// an Origin.
	{Name: "JobRelease", Type: reflect.TypeFor[types.JobRelease](), RuntimeObject: true},
	{Name: "JobUnattributedWrite", Type: reflect.TypeFor[types.JobUnattributedWrite](), RuntimeObject: true},
	// Leaf first: Origin.credential is a Credential, whose grant is a Grant.
	{Name: "Grant", Type: reflect.TypeFor[types.Grant](), RuntimeObject: true},
	{Name: "Credential", Type: reflect.TypeFor[types.Credential](), RuntimeObject: true},
	{Name: "Origin", Type: reflect.TypeFor[types.Origin](), RuntimeObject: true},
	{Name: "LeaseCheck", Type: reflect.TypeFor[types.LeaseCheck](), RuntimeObject: true},
	{Name: "CompletionGate", Type: reflect.TypeFor[types.CompletionGate](), RuntimeObject: true},
	{Name: "GateEvidence", Type: reflect.TypeFor[types.GateEvidence](), RuntimeObject: true},
	{Name: "JobResult", Type: reflect.TypeFor[types.JobResult](), RuntimeObject: true},
	{Name: "JobResultValidation", Type: reflect.TypeFor[types.JobResultValidation](), RuntimeObject: true},
	{Name: "JobAttempt", Type: reflect.TypeFor[types.JobAttempt](), RuntimeObject: true},
	{Name: "JobGateAttempt", Type: reflect.TypeFor[types.JobGateAttempt](), RuntimeObject: true},
	{Name: "JobRun", Type: reflect.TypeFor[types.JobRun](), RuntimeObject: true},
	{Name: "Job", Type: reflect.TypeFor[types.Job](), RuntimeObject: true},
	// Declaration is Job's INPUT twin: a magusfile can construct one, but nothing hands one
	// back out, so it carries no RuntimeObject encoder.
	{Name: "Declaration", Type: reflect.TypeFor[types.Declaration]()},
	{Name: "FileChange", Type: reflect.TypeFor[types.FileChange](), RuntimeObject: true},
	{Name: "RegionChange", Type: reflect.TypeFor[types.RegionChange](), RuntimeObject: true},
	{Name: "JobOverlapFootprint", Type: reflect.TypeFor[types.JobOverlapFootprint](), RuntimeObject: true},
	{Name: "JobOverlap", Type: reflect.TypeFor[types.JobOverlap](), RuntimeObject: true},
	{Name: "JobBlock", Type: reflect.TypeFor[types.JobBlock](), RuntimeObject: true},
	{Name: "JobList", Type: reflect.TypeFor[types.JobList](), RuntimeObject: true},
	{Name: "JobStatus", Type: reflect.TypeFor[types.JobStatus](), RuntimeObject: true},
	{Name: "GateStatus", Type: reflect.TypeFor[types.GateStatus](), RuntimeObject: true},
	// magus\guard.spawn's and magus\guard.command's requests, after Job because each
	// request's lease is one, and the verdict both return. The guard hands a rule the
	// request and the rule hands back the verdict, so all need encoders: allow/advise/deny
	// build the verdict on the Go side.
	{Name: "SpawnTarget", Type: reflect.TypeFor[types.SpawnTarget](), RuntimeObject: true},
	{Name: "SpawnRequest", Type: reflect.TypeFor[types.SpawnRequest](), RuntimeObject: true},
	{Name: "CommandInvocation", Type: reflect.TypeFor[types.CommandInvocation](), RuntimeObject: true},
	{Name: "CheckoutState", Type: reflect.TypeFor[types.CheckoutState](), RuntimeObject: true},
	{Name: "CommandRequest", Type: reflect.TypeFor[types.CommandRequest](), RuntimeObject: true},
	{Name: "WriteRequest", Type: reflect.TypeFor[types.WriteRequest](), RuntimeObject: true},
	{Name: "GuardVerdict", Type: reflect.TypeFor[types.GuardVerdict](), RuntimeObject: true},
	{Name: "GuardBinary", Type: reflect.TypeFor[types.GuardBinary](), RuntimeObject: true},
	{Name: "Skill", Type: reflect.TypeFor[types.Skill](), RuntimeObject: true},
}

// boundaryEnums declares the Go named string types that mirror as Buzz `enum<str>`
// rather than as a bare `str`.
//
// The cases are listed here rather than derived because reflect cannot enumerate a
// named type's constants: it sees only the underlying kind. That is the whole reason
// a registry exists: without it every one of these crosses as an untyped string, and a
// magusfile typo is a silent miss instead of a compile error.
//
// A case must be a legal Buzz identifier, and the first entry is the field's default,
// so a zero-valued case belongs first.
var boundaryEnums = []boundaryEnum{
	{
		Name:  "SignAlgorithm",
		Type:  reflect.TypeFor[types.SignAlgorithm](),
		Cases: []enumCase{{"Ed25519", "ed25519"}},
	},
	{
		Name: "TermStyle",
		Type: reflect.TypeFor[types.TermStyle](),
		Cases: []enumCase{{"none", ""}, {"bold", "1"}, {"dim", "2"}, {"red", "31"}, {"green", "32"},
			{"yellow", "33"}, {"dimGreen", "2;32"}, {"dimGrey", "2;37"}, {"brightGreen", "1;32"}},
	},
	{
		Name: "TimeLayout",
		Type: reflect.TypeFor[types.TimeLayout](),
		Cases: []enumCase{{"rfc3339", "2006-01-02T15:04:05Z07:00"},
			{"rfc3339Nano", "2006-01-02T15:04:05.999999999Z07:00"},
			{"dateOnly", "2006-01-02"}, {"timeOnly", "15:04:05"},
			{"dateTime", "2006-01-02 15:04:05"},
			{"rfc1123", "Mon, 02 Jan 2006 15:04:05 MST"}, {"kitchen", "3:04PM"}},
	},
	{
		Name:  "LogLevel",
		Type:  reflect.TypeFor[types.LogLevel](),
		Cases: []enumCase{{"trace", "trace"}, {"debug", "debug"}, {"info", "info"}, {"warn", "warn"}, {"error", "error"}},
	},
	{
		Name:  "PlatformStyle",
		Type:  reflect.TypeFor[types.PlatformStyle](),
		Cases: []enumCase{{"none", ""}, {"go", "go"}, {"uname", "uname"}},
	},
	{
		Name:  "CheckStatus",
		Type:  reflect.TypeFor[types.CheckStatus](),
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
		Name:  "DiffUncoveredReason",
		Type:  reflect.TypeFor[types.DiffUncoveredReason](),
		Cases: []enumCase{{"none", ""}, {"noIndexer", "no-indexer"}},
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
	{
		Name:  "DiagnosticFormat",
		Type:  reflect.TypeFor[spells.DiagnosticFormat](),
		Cases: []enumCase{{"none", ""}, {"gnu", "gnu"}},
	},
	{
		Name:  "SymbolFormat",
		Type:  reflect.TypeFor[spells.SymbolFormat](),
		Cases: []enumCase{{"none", ""}, {"scip", "scip"}},
	},
	{
		Name:  "External",
		Type:  reflect.TypeFor[spells.External](),
		Cases: []enumCase{{"none", ""}, {"reads", "reads-external"}, {"mutates", "mutates-external"}},
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

// buzzEnum returns the enum a Go type mirrors as, if any.
func buzzEnum(rt reflect.Type) (boundaryEnum, bool) {
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

// buzzName returns the BUZZ name a Go type mirrors as, which is the registry key
// and not always the Go type's own name: types.ProjectsOutput is `Projects`,
// types.StatusTargetRun is `TargetRun`. A struct-valued field must reference the Buzz
// name, so the registry is what resolves it, falling back to the Go name only for a
// type no entry claims, which the caller then reports as undeclared.
func buzzName(rt reflect.Type) string {
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

// boundaryEnumNamed finds a declared enum by its Buzz name. The counterpart to
// boundaryTypeNamed, for the generator that has to declare an enum a signature
// references.
func boundaryEnumNamed(name string) (boundaryEnum, bool) {
	for _, e := range boundaryEnums {
		if e.Name == name {
			return e, true
		}
	}
	return boundaryEnum{}, false
}
