package spellruntime

import _ "embed"

// This file holds the generated Buzz `object` mirrors of every host-METHOD return type
// (target.go's mirrors are what a spell op WRITES). Each ships with the declarations of
// the import path whose method returns it - proc.exec returns ExecResult, so ExecResult
// ships with "os" - so a spell already doing `import "os";` can annotate `> ExecResult`
// with no second import.
//
// Ordering within each bundle matters: a struct-valued field mirrors as its Go type's
// bare name, which must be declared before the object referencing it.

// ExecResultSource is the generated Buzz `object ExecResult` mirror of
// types.ExecResult (see cmd/magus-utils types). Ships with "os": proc.exec /
// os.execSh return it, and magus's own describe-style methods (magus.build, ...)
// that also return ExecResult reuse the same mirror once "os" is imported for the
// exec call that produced it in the first place.
//
//go:generate go run ../../cmd/magus-utils types -type ExecResult -out gen/types/execresult.buzz
//go:generate go run ../../cmd/magus-utils types -type ShellCommand -out gen/types/shellcommand.buzz
//go:embed gen/types/execresult.buzz
var ExecResultSource string

// FileInfoSource is the generated Buzz mirror of the fs.stat result. Ships with "fs".
//
//go:generate go run ../../cmd/magus-utils types -type FileInfo -out gen/types/fileinfo.buzz
//go:generate go run ../../cmd/magus-utils types -type Status -out gen/types/status.buzz
//go:embed gen/types/fileinfo.buzz
var FileInfoSource string

// HTTPResponseSource is the generated Buzz mirror of an http.* response. Ships
// with "http".
//
//go:generate go run ../../cmd/magus-utils types -type HttpResponse -out gen/types/httpresponse.buzz
//go:embed gen/types/httpresponse.buzz
var HTTPResponseSource string

// URLSource is the generated Buzz mirror of encoding.parseUrl's result. Ships
// with "encoding".
//
//go:generate go run ../../cmd/magus-utils types -type URL -out gen/types/url.buzz
//go:embed gen/types/url.buzz
var URLSource string

// SemverVersionSource / SemverNextSource are the generated Buzz mirrors of
// semver.parse's and semver.next's results. Ship with "semver". No ordering
// dependency between the two (SemverNext's fields are plain strings), but they
// live next to each other since the two host methods are a pair.
//
// SemverVersionSource is ALSO co-located into the "vcs" bundle: vcs.tags returns [Tag]
// and Tag.version is a SemverVersion, so `import "vcs";` alone still needs it in scope.
// An import line inside vcs's bundle cannot reach "semver" - a synthetic module's
// companion source is only collected, never executed, so an import inside it is inert -
// hence duplicating the generated string into both bundles at assembly time.
//
//go:generate go run ../../cmd/magus-utils types -type SemverVersion -out gen/types/semverversion.buzz
//go:embed gen/types/semverversion.buzz
var SemverVersionSource string

//go:generate go run ../../cmd/magus-utils types -type SemverNext -out gen/types/semvernext.buzz
//go:embed gen/types/semvernext.buzz
var SemverNextSource string

// CommitAuthorSource / CommitSource / TagSource are the generated Buzz mirrors of
// types.CommitAuthor, types.CommitRecord, and types.VCSTag. Ship with "vcs":
// vcs.commit/vcs.history return Commit, vcs.tags returns [Tag]. CommitAuthor must
// precede Commit (Commit.author is CommitAuthor); the co-located SemverVersion (see
// above) must precede Tag (Tag.version is SemverVersion).
//
//go:generate go run ../../cmd/magus-utils types -type CommitAuthor -out gen/types/commitauthor.buzz
//go:embed gen/types/commitauthor.buzz
var CommitAuthorSource string

//go:generate go run ../../cmd/magus-utils types -type Commit -out gen/types/commit.buzz
//go:embed gen/types/commit.buzz
var CommitSource string

//go:generate go run ../../cmd/magus-utils types -type Tag -out gen/types/tag.buzz
//go:embed gen/types/tag.buzz
var TagSource string

// ProjectEntrySource / ProjectsSource are the generated Buzz mirrors of
// types.ProjectEntry and types.ProjectsOutput: what magus.ls returns. They close
// a documented gap - magus.ls's own doc told readers to annotate `> Projects`
// while no such type existed, so the annotation it recommended did not compile.
// Ship with "magus" (magus.ls is a magus.* method, not a bare-import host module).
// ProjectEntry must precede Projects (Projects.projects is [ProjectEntry]).
//
//go:generate go run ../../cmd/magus-utils types -type ProjectEntry -out gen/types/projectentry.buzz
//go:embed gen/types/projectentry.buzz
var ProjectEntrySource string

//go:generate go run ../../cmd/magus-utils types -type Projects -out gen/types/projects.buzz
//go:embed gen/types/projects.buzz
var ProjectsSource string

// AffectedSource / GraphSource are magus.affected's and magus.graph's returns -
// the in-process verbs beside ls, which had the same annotation gap Projects did.
// Ship with "magus".
//
//go:generate go run ../../cmd/magus-utils types -type Affected -out gen/types/affected.buzz
//go:embed gen/types/affected.buzz
var AffectedSource string

//go:generate go run ../../cmd/magus-utils types -type Graph -out gen/types/graph.buzz
//go:embed gen/types/graph.buzz
var GraphSource string

// ModuleFieldEntrySource / ModuleMethodEntrySource / ModuleSource are magus.modules
// / magus.module's returns. Ship with "magus". ModuleFieldEntry and
// ModuleMethodEntry must precede Module (its fields/methods are lists of them).
//
//go:generate go run ../../cmd/magus-utils types -type ModuleFieldEntry -out gen/types/modulefieldentry.buzz
//go:embed gen/types/modulefieldentry.buzz
var ModuleFieldEntrySource string

//go:generate go run ../../cmd/magus-utils types -type ModuleMethodEntry -out gen/types/modulemethodentry.buzz
//go:embed gen/types/modulemethodentry.buzz
var ModuleMethodEntrySource string

//go:generate go run ../../cmd/magus-utils types -type Module -out gen/types/module.buzz
//go:embed gen/types/module.buzz
var ModuleSource string

// CrossTargetRefSource / TargetSpellUseSource / InputRefSource / OutputRefSource / UpdateRefSource /
// ChainStepSource / TargetGraphNodeSource / TargetGraphProjectSource / TargetGraphSource are the
// generated Buzz mirrors of magus.targets's result (types.TargetGraphOutput and the
// node/ref types it nests). Ship with "magus" (magus.targets is a magus.* method).
// Declare-before-use order, since each nested type is referenced by the next: the six
// leaves have no struct-valued fields so their relative order does not matter; then
// TargetGraphNode (referencing all six), TargetGraphProject, then TargetGraph.
//
//go:generate go run ../../cmd/magus-utils types -type CrossTargetRef -out gen/types/crosstargetref.buzz
//go:embed gen/types/crosstargetref.buzz
var CrossTargetRefSource string

//go:generate go run ../../cmd/magus-utils types -type TargetSpellUse -out gen/types/targetspelluse.buzz
//go:embed gen/types/targetspelluse.buzz
var TargetSpellUseSource string

//go:generate go run ../../cmd/magus-utils types -type InputRef -out gen/types/inputref.buzz
//go:embed gen/types/inputref.buzz
var InputRefSource string

//go:generate go run ../../cmd/magus-utils types -type OutputRef -out gen/types/outputref.buzz
//go:embed gen/types/outputref.buzz
var OutputRefSource string

//go:generate go run ../../cmd/magus-utils types -type UpdateRef -out gen/types/updateref.buzz
//go:embed gen/types/updateref.buzz
var UpdateRefSource string

//go:generate go run ../../cmd/magus-utils types -type ChainStep -out gen/types/chainstep.buzz
//go:embed gen/types/chainstep.buzz
var ChainStepSource string

//go:generate go run ../../cmd/magus-utils types -type TargetGraphNode -out gen/types/targetgraphnode.buzz
//go:embed gen/types/targetgraphnode.buzz
var TargetGraphNodeSource string

//go:generate go run ../../cmd/magus-utils types -type TargetGraphProject -out gen/types/targetgraphproject.buzz
//go:embed gen/types/targetgraphproject.buzz
var TargetGraphProjectSource string

//go:generate go run ../../cmd/magus-utils types -type TargetGraph -out gen/types/targetgraph.buzz
//go:embed gen/types/targetgraph.buzz
var TargetGraphSource string

// TargetRunSource / RunSource are the generated Buzz mirrors of one run and the
// targets in it (types.StatusTargetRun and types.StatusRun), the same shape
// `magus status` reports. They exist so a caller can ITERATE a run - each target's
// state (queued/running/passed/failed/cached), how long it took, and the output ref
// it minted - rather than parsing magus's own console output back out of a string.
//
// TargetRun precedes Run, because Run.targets is a list of it and a struct-valued
// field mirrors as its bare type name, which must already be declared.
//
//go:generate go run ../../cmd/magus-utils types -type TargetRun -out gen/types/targetrun.buzz
//go:embed gen/types/targetrun.buzz
var TargetRunSource string

//go:generate go run ../../cmd/magus-utils types -type Run -out gen/types/run.buzz
//go:embed gen/types/run.buzz
var RunSource string

// FileEntrySource / FileReportSource mirror magus.describeFile's result: one entry per
// path with its role (output | source | maintained | unclaimed) and the projects claiming it.
//
//go:generate go run ../../cmd/magus-utils types -type FileEntry -out gen/types/fileentry.buzz
//go:embed gen/types/fileentry.buzz
var FileEntrySource string

//go:generate go run ../../cmd/magus-utils types -type FileReport -out gen/types/filereport.buzz
//go:embed gen/types/filereport.buzz
var FileReportSource string

// DiagnosticSource mirrors the value a coded failure presents to a Buzz `catch`, so a
// magusfile can narrow one instead of indexing an undeclared map.
//
//go:generate go run ../../cmd/magus-utils types -type Diagnostic -out gen/types/diagnostic.buzz
//go:embed gen/types/diagnostic.buzz
var DiagnosticSource string

// DoctorCheckSource / DoctorSummarySource / DoctorReportSource mirror magus.doctor's
// result. Leaf-first: DoctorReport carries a list of DoctorCheck and one DoctorSummary.
//
//go:generate go run ../../cmd/magus-utils types -type DoctorCheck -out gen/types/doctorcheck.buzz
//go:embed gen/types/doctorcheck.buzz
var DoctorCheckSource string

//go:generate go run ../../cmd/magus-utils types -type DoctorSummary -out gen/types/doctorsummary.buzz
//go:embed gen/types/doctorsummary.buzz
var DoctorSummarySource string

//go:generate go run ../../cmd/magus-utils types -type DoctorReport -out gen/types/doctorreport.buzz
//go:embed gen/types/doctorreport.buzz
var DoctorReportSource string

// The magus.insight bundle, declared leaf-first: every lens (hotspots, affinity,
// ownership, trend, volatility) plus the knowledge-graph axis, as typed values rather
// than a Markdown report to scrape. Order matters - a struct-valued field mirrors as its
// Buzz name and that name must already be declared.

//go:generate go run ../../cmd/magus-utils types -type Node -out gen/types/node.buzz
//go:embed gen/types/node.buzz
var NodeSource string

//go:generate go run ../../cmd/magus-utils types -type FileHotspot -out gen/types/filehotspot.buzz
//go:embed gen/types/filehotspot.buzz
var FileHotspotSource string

//go:generate go run ../../cmd/magus-utils types -type Hotspots -out gen/types/hotspots.buzz
//go:embed gen/types/hotspots.buzz
var HotspotsSource string

//go:generate go run ../../cmd/magus-utils types -type CoChange -out gen/types/cochange.buzz
//go:embed gen/types/cochange.buzz
var CoChangeSource string

//go:generate go run ../../cmd/magus-utils types -type Affinity -out gen/types/affinity.buzz
//go:embed gen/types/affinity.buzz
var AffinitySource string

//go:generate go run ../../cmd/magus-utils types -type OwnershipEntry -out gen/types/ownershipentry.buzz
//go:embed gen/types/ownershipentry.buzz
var OwnershipEntrySource string

//go:generate go run ../../cmd/magus-utils types -type Ownership -out gen/types/ownership.buzz
//go:embed gen/types/ownership.buzz
var OwnershipSource string

//go:generate go run ../../cmd/magus-utils types -type TrendEntry -out gen/types/trendentry.buzz
//go:embed gen/types/trendentry.buzz
var TrendEntrySource string

//go:generate go run ../../cmd/magus-utils types -type Trend -out gen/types/trend.buzz
//go:embed gen/types/trend.buzz
var TrendSource string

//go:generate go run ../../cmd/magus-utils types -type VolatilityTarget -out gen/types/volatilitytarget.buzz
//go:embed gen/types/volatilitytarget.buzz
var VolatilityTargetSource string

//go:generate go run ../../cmd/magus-utils types -type Volatility -out gen/types/volatility.buzz
//go:embed gen/types/volatility.buzz
var VolatilitySource string

//go:generate go run ../../cmd/magus-utils types -type KnowledgeGodNode -out gen/types/knowledgegodnode.buzz
//go:embed gen/types/knowledgegodnode.buzz
var KnowledgeGodNodeSource string

//go:generate go run ../../cmd/magus-utils types -type KnowledgeOrphan -out gen/types/knowledgeorphan.buzz
//go:embed gen/types/knowledgeorphan.buzz
var KnowledgeOrphanSource string

//go:generate go run ../../cmd/magus-utils types -type KnowledgeDocCoverage -out gen/types/knowledgedoccoverage.buzz
//go:embed gen/types/knowledgedoccoverage.buzz
var KnowledgeDocCoverageSource string

//go:generate go run ../../cmd/magus-utils types -type KnowledgeStats -out gen/types/knowledgestats.buzz
//go:embed gen/types/knowledgestats.buzz
var KnowledgeStatsSource string

// The unreferenced lens and the coverage verdict it carries. ProjectRef leads because a
// gap names a project the same way every other structured output does - by path AND
// display name, so the workspace root reads as its repo name rather than a bare ".".

//go:generate go run ../../cmd/magus-utils types -type ProjectRef -out gen/types/projectref.buzz
//go:embed gen/types/projectref.buzz
var ProjectRefSource string

//go:generate go run ../../cmd/magus-utils types -type KnowledgeSymbolGap -out gen/types/knowledgesymbolgap.buzz
//go:embed gen/types/knowledgesymbolgap.buzz
var KnowledgeSymbolGapSource string

//go:generate go run ../../cmd/magus-utils types -type KnowledgeAnswer -out gen/types/knowledgeanswer.buzz
//go:embed gen/types/knowledgeanswer.buzz
var KnowledgeAnswerSource string

//go:generate go run ../../cmd/magus-utils types -type UnreferencedEntry -out gen/types/unreferencedentry.buzz
//go:embed gen/types/unreferencedentry.buzz
var UnreferencedEntrySource string

//go:generate go run ../../cmd/magus-utils types -type Unreferenced -out gen/types/unreferenced.buzz
//go:embed gen/types/unreferenced.buzz
var UnreferencedSource string

//go:generate go run ../../cmd/magus-utils types -type InsightReport -out gen/types/insightreport.buzz
//go:embed gen/types/insightreport.buzz
var InsightReportSource string

// magus.affectedImpact's report: the affected set and why each project is in it, declared
// leaf-first.

//go:generate go run ../../cmd/magus-utils types -type ImpactCoverage -out gen/types/impactcoverage.buzz
//go:embed gen/types/impactcoverage.buzz
var ImpactCoverageSource string

//go:generate go run ../../cmd/magus-utils types -type ImpactSymbol -out gen/types/impactsymbol.buzz
//go:embed gen/types/impactsymbol.buzz
var ImpactSymbolSource string

//go:generate go run ../../cmd/magus-utils types -type ImpactFileCoverage -out gen/types/impactfilecoverage.buzz
//go:embed gen/types/impactfilecoverage.buzz
var ImpactFileCoverageSource string

//go:generate go run ../../cmd/magus-utils types -type ImpactProject -out gen/types/impactproject.buzz
//go:embed gen/types/impactproject.buzz
var ImpactProjectSource string

//go:generate go run ../../cmd/magus-utils types -type Impact -out gen/types/impact.buzz
//go:embed gen/types/impact.buzz
var ImpactSource string
