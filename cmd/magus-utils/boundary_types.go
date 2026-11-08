package main

//go:generate go run . boundarylist -out ../../internal/interp/bindings/gen/boundary_list.go -pkg bindgen
//go:generate go run . boundaryobjects -out ../../internal/interp/bindings/gen/boundary_objects.go -pkg bindgen

import (
	"github.com/egladman/magus/types/events"
	"github.com/egladman/magus/types/trail"
	"github.com/egladman/magus/types/daemon"
	"github.com/egladman/magus/types/image"
	"fmt"
	"reflect"
	"strings"

	"github.com/egladman/magus/internal/hostmodules"
	queuetypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// boundaryTypes is the one declaration of every Go value that crosses the
// typed Buzz boundary. The mirror generator reads every entry; runtimeObject
// marks the values returned by a host method and therefore requiring a direct
// Go-to-Buzz encoder too. Keeping the two outputs on this one registry means a
// new mirror cannot silently describe a shape the runtime fails to produce.
var boundaryTypes = []boundaryType{
	{Name: "Path", Type: reflect.TypeFor[types.Path]()},
	// RuntimeObject because magus\describe.evaluatedTarget hands one back as a policy.
	{Name: "Target", Type: reflect.TypeFor[types.Target](), RuntimeObject: true},
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
	{Name: "Coverage", Type: reflect.TypeFor[spells.Coverage]()},
	{Name: "Charm", Type: reflect.TypeFor[spells.Charm]()},
	{Name: "PatchOp", Type: reflect.TypeFor[spells.PatchOp]()},
	{Name: "VersionKey", Type: reflect.TypeFor[spells.VersionKey]()},
	{Name: "VersionBounds", Type: reflect.TypeFor[spells.VersionBounds]()},
	{Name: "Tool", Type: reflect.TypeFor[spells.Tool]()},
	// Leaves first: Sandbox.allow is [SandboxAllow], Sandbox.env a SandboxEnv and
	// Sandbox.caches [SandboxCache]. RuntimeObject because a workspace-local spell's
	// handle carries its Sandbox back into Buzz.
	{Name: "SandboxAllow", Type: reflect.TypeFor[spells.SandboxAllow](), RuntimeObject: true},
	{Name: "SandboxEnv", Type: reflect.TypeFor[spells.SandboxEnv](), RuntimeObject: true},
	{Name: "SandboxCache", Type: reflect.TypeFor[spells.SandboxCache](), RuntimeObject: true},
	{Name: "Sandbox", Type: reflect.TypeFor[spells.Sandbox](), RuntimeObject: true},
	// Leaves first: CommentSyntax carries [CommentBlock] and [Quote], and
	// Language carries a Syntax.
	// RuntimeObject because magus\describe.spell hands them back inside a Spell.
	{Name: "CommentBlock", Type: reflect.TypeFor[spells.CommentBlock](), RuntimeObject: true},
	{Name: "Quote", Type: reflect.TypeFor[spells.Quote](), RuntimeObject: true},
	{Name: "CommentSyntax", Type: reflect.TypeFor[spells.CommentSyntax](), RuntimeObject: true},
	{Name: "StubSyntax", Type: reflect.TypeFor[spells.StubSyntax](), RuntimeObject: true},
	{Name: "DecisionSyntax", Type: reflect.TypeFor[spells.DecisionSyntax](), RuntimeObject: true},
	{Name: "Syntax", Type: reflect.TypeFor[spells.Syntax](), RuntimeObject: true},
	{Name: "Language", Type: reflect.TypeFor[spells.Language]()},
	// A provider spell WRITES this one, like Project below: resolve_secret constructs it
	// and returns it, so it needs the mirror but no Go-to-Buzz encoder.
	{Name: "Secret", Type: reflect.TypeFor[spells.Secret]()},
	// A spell WRITES this one, so it takes the bare Buzz name; the Go side carries
	// the adjective because types.Project and types.ProjectEntry already exist. The
	// registry keys on the Buzz name, which is what makes that split expressible.
	{Name: "Project", Type: reflect.TypeFor[spells.ProvidedProject]()},
	// A lifecycle provider WRITES these, like Project. Leaf first: Lifecycle.cycles is
	// [ReleaseCycle].
	{Name: "ReleaseCycle", Type: reflect.TypeFor[spells.ReleaseCycle]()},
	{Name: "Lifecycle", Type: reflect.TypeFor[spells.Lifecycle]()},
	// discover_projects: magus encodes the query and a spell writes the claims. Leaves
	// first: DiscoveryQuery.files is [DiscoveryFile], DiscoveredProject.notInferred is
	// [DiscoveryGap].
	{Name: "DiscoveryFile", Type: reflect.TypeFor[spells.DiscoveryFile](), RuntimeObject: true},
	{Name: "DiscoveryQuery", Type: reflect.TypeFor[spells.DiscoveryQuery](), RuntimeObject: true},
	{Name: "DiscoveryGap", Type: reflect.TypeFor[spells.DiscoveryGap]()},
	{Name: "DiscoveredProject", Type: reflect.TypeFor[spells.DiscoveredProject]()},
	// A provider op's records (provider_ops.go). magus encodes each request, so it is a
	// RuntimeObject; it decodes each response, so that one is listed in inboundTypes.
	{Name: "Empty", Type: reflect.TypeFor[types.Empty](), RuntimeObject: true},
	{Name: "SecretRef", Type: reflect.TypeFor[types.SecretRef](), RuntimeObject: true},
	{Name: "CIGroup", Type: reflect.TypeFor[types.CIGroup](), RuntimeObject: true},
	{Name: "CIGroupEnd", Type: reflect.TypeFor[types.CIGroupEnd](), RuntimeObject: true},
	{Name: "CIAnnotation", Type: reflect.TypeFor[types.CIAnnotation](), RuntimeObject: true},
	{Name: "ArtifactRequest", Type: reflect.TypeFor[types.ArtifactRequest](), RuntimeObject: true},
	{Name: "PruneRequest", Type: reflect.TypeFor[types.PruneRequest](), RuntimeObject: true},
	{Name: "LifecycleQuery", Type: reflect.TypeFor[types.LifecycleQuery](), RuntimeObject: true},
	{Name: "FindReviewRequest", Type: reflect.TypeFor[types.FindReviewRequest](), RuntimeObject: true},
	// Leaf first: PublishReviewRequest.drafts is [ReviewDraft].
	{Name: "ReviewDraft", Type: reflect.TypeFor[types.ReviewDraft](), RuntimeObject: true},
	{Name: "PublishReviewRequest", Type: reflect.TypeFor[types.PublishReviewRequest](), RuntimeObject: true},
	{Name: "ReplyReviewRequest", Type: reflect.TypeFor[types.ReplyReviewRequest](), RuntimeObject: true},
	{Name: "ReviewRef", Type: reflect.TypeFor[types.ReviewRef](), RuntimeObject: true},
	{Name: "ProjectQuery", Type: reflect.TypeFor[types.ProjectQuery](), RuntimeObject: true},
	{Name: "ListPullsRequest", Type: reflect.TypeFor[types.ListPullsRequest](), RuntimeObject: true},
	{Name: "ReviewTarget", Type: reflect.TypeFor[types.ReviewTarget]()},
	{Name: "ReviewComment", Type: reflect.TypeFor[types.ReviewComment]()},
	// list_pulls answer, leaf first: PullList holds BaseBranch and ForgePull, which hold
	// ForgeCheck, BaseCommit and RateLimit.
	{Name: "RateLimit", Type: reflect.TypeFor[types.RateLimit]()},
	{Name: "ForgeCheck", Type: reflect.TypeFor[types.ForgeCheck]()},
	{Name: "BaseCommit", Type: reflect.TypeFor[types.BaseCommit]()},
	{Name: "BaseBranch", Type: reflect.TypeFor[types.BaseBranch]()},
	{Name: "ForgePull", Type: reflect.TypeFor[types.ForgePull]()},
	{Name: "PullList", Type: reflect.TypeFor[types.PullList]()},
	// The merge queue provider script's records, leaf first, each taking a Queue prefix
	// because Change, Check and Setting already name other mirrors. host.go provides them to
	// the queue's session; nothing else imports them.
	{Name: "Person", Type: reflect.TypeFor[types.Person]()},
	{Name: "QueueIntegration", Type: reflect.TypeFor[queuetypes.Integration]()},
	{Name: "QueueRequiredCheck", Type: reflect.TypeFor[queuetypes.RequiredCheck]()},
	{Name: "QueueSetting", Type: reflect.TypeFor[queuetypes.Setting]()},
	{Name: "QueueApp", Type: reflect.TypeFor[queuetypes.App]()},
	{Name: "QueueSetupStep", Type: reflect.TypeFor[queuetypes.SetupStep]()},
	{Name: "QueueSetup", Type: reflect.TypeFor[queuetypes.Setup]()},
	{Name: "QueueSetupRefusal", Type: reflect.TypeFor[queuetypes.SetupRefusedError]()},
	{Name: "QueueCapabilities", Type: reflect.TypeFor[queuetypes.Capabilities]()},
	{Name: "QueueDescribeResult", Type: reflect.TypeFor[queuetypes.DescribeResult]()},
	{Name: "QueueChange", Type: reflect.TypeFor[queuetypes.Change]()},
	{Name: "QueueMergedChange", Type: reflect.TypeFor[queuetypes.MergedChange]()},
	{Name: "QueueUnqueuedChange", Type: reflect.TypeFor[queuetypes.UnqueuedChange]()},
	{Name: "QueueClosedChange", Type: reflect.TypeFor[queuetypes.ClosedChange]()},
	{Name: "QueueChanges", Type: reflect.TypeFor[queuetypes.Changes]()},
	{Name: "QueueApproval", Type: reflect.TypeFor[queuetypes.Approval]()},
	{Name: "QueueCheck", Type: reflect.TypeFor[queuetypes.Check]()},
	{Name: "QueueGreenChange", Type: reflect.TypeFor[queuetypes.GreenChange]()},
	{Name: "QueueMergeAnswer", Type: reflect.TypeFor[queuetypes.MergeAnswer]()},
	{Name: "QueueRunOrigin", Type: reflect.TypeFor[queuetypes.RunOrigin]()},
	{Name: "QueueArtifact", Type: reflect.TypeFor[queuetypes.Artifact]()},
	{Name: "QueueArtifactListing", Type: reflect.TypeFor[queuetypes.ArtifactListing]()},
	{Name: "QueueReview", Type: reflect.TypeFor[queuetypes.Review]()},
	{Name: "QueueReviews", Type: reflect.TypeFor[queuetypes.Reviews]()},
	{Name: "QueuePinnedChange", Type: reflect.TypeFor[queuetypes.PinnedChange](), RuntimeObject: true},
	{Name: "QueueReproduction", Type: reflect.TypeFor[queuetypes.Reproduction](), RuntimeObject: true},
	{Name: "QueueDescribeRequest", Type: reflect.TypeFor[queuetypes.DescribeRequest](), RuntimeObject: true},
	{Name: "QueueListChangesRequest", Type: reflect.TypeFor[queuetypes.ListChangesRequest](), RuntimeObject: true},
	{Name: "QueueApprovalRequest", Type: reflect.TypeFor[queuetypes.ApprovalRequest](), RuntimeObject: true},
	{Name: "QueueRequiredChecksRequest", Type: reflect.TypeFor[queuetypes.RequiredChecksRequest](), RuntimeObject: true},
	{Name: "QueueListGreenRequest", Type: reflect.TypeFor[queuetypes.ListGreenRequest](), RuntimeObject: true},
	{Name: "QueueStatusRequest", Type: reflect.TypeFor[queuetypes.StatusRequest](), RuntimeObject: true},
	{Name: "QueueRetargetRequest", Type: reflect.TypeFor[queuetypes.RetargetRequest](), RuntimeObject: true},
	{Name: "QueueMergeRequest", Type: reflect.TypeFor[queuetypes.MergeRequest](), RuntimeObject: true},
	{Name: "QueueKickBackRequest", Type: reflect.TypeFor[queuetypes.KickBackRequest](), RuntimeObject: true},
	{Name: "QueueMarkRequest", Type: reflect.TypeFor[queuetypes.MarkRequest](), RuntimeObject: true},
	{Name: "QueueListArtifactsRequest", Type: reflect.TypeFor[queuetypes.ListArtifactsRequest](), RuntimeObject: true},
	{Name: "QueueReviewsRequest", Type: reflect.TypeFor[queuetypes.ReviewsRequest](), RuntimeObject: true},
	{Name: "QueueDismissReviewRequest", Type: reflect.TypeFor[queuetypes.DismissReviewRequest](), RuntimeObject: true},
	{Name: "Ack", Type: reflect.TypeFor[types.Ack]()},
	{Name: "GreenRun", Type: reflect.TypeFor[types.GreenRun]()},
	{Name: "Strings", Type: reflect.TypeFor[types.Strings]()},
	{Name: "PruneResult", Type: reflect.TypeFor[types.PruneResult]()},
	{Name: "ExecResult", Type: reflect.TypeFor[types.ExecResult](), RuntimeObject: true},
	{Name: "ShellCommand", Type: reflect.TypeFor[types.ShellCommand](), RuntimeObject: true},
	{Name: "CommitAuthor", Type: reflect.TypeFor[types.CommitAuthor](), RuntimeObject: true},
	{Name: "Commit", Type: reflect.TypeFor[types.CommitRecord](), RuntimeObject: true},
	{Name: "Status", Type: reflect.TypeFor[types.StatusRecord](), RuntimeObject: true},
	{Name: "DriftResult", Type: reflect.TypeFor[types.DriftResultRecord](), RuntimeObject: true},
	{Name: "FileInfo", Type: reflect.TypeFor[types.FileInfo](), RuntimeObject: true},
	{Name: "DiskUsage", Type: reflect.TypeFor[types.DiskUsage](), RuntimeObject: true},
	{Name: "TrimReport", Type: reflect.TypeFor[types.TrimReport](), RuntimeObject: true},
	{Name: "SpellCache", Type: reflect.TypeFor[types.SpellCache](), RuntimeObject: true},
	{Name: "DiskReclaim", Type: reflect.TypeFor[types.DiskReclaim](), RuntimeObject: true},
	{Name: "UncompressResult", Type: reflect.TypeFor[types.UncompressResult](), RuntimeObject: true},
	{Name: "ArchiveEntry", Type: reflect.TypeFor[types.ArchiveEntry](), RuntimeObject: true},
	{Name: "MarkdownSection", Type: reflect.TypeFor[types.MarkdownSection](), RuntimeObject: true},
	{Name: "CompressResult", Type: reflect.TypeFor[types.CompressResult](), RuntimeObject: true},
	{Name: "Response", Module: "http", Type: reflect.TypeFor[types.HTTPResponse](), RuntimeObject: true},
	{Name: "TermSize", Type: reflect.TypeFor[types.TermSize](), RuntimeObject: true},
	{Name: "SemverVersion", Type: reflect.TypeFor[types.SemverVersion](), RuntimeObject: true},
	{Name: "SemverNext", Type: reflect.TypeFor[types.SemverNext](), RuntimeObject: true},
	{Name: "BuzzFunction", Type: reflect.TypeFor[types.BuzzFunction](), RuntimeObject: true},
	{Name: "BuzzStatement", Type: reflect.TypeFor[types.BuzzStatement](), RuntimeObject: true},
	{Name: "BuzzTest", Type: reflect.TypeFor[types.BuzzTest](), RuntimeObject: true},
	{Name: "BuzzFile", Type: reflect.TypeFor[types.BuzzFile](), RuntimeObject: true},
	{Name: "URL", Type: reflect.TypeFor[types.URL](), RuntimeObject: true},
	{Name: "YamlPositions", Type: reflect.TypeFor[types.YAMLPositions](), RuntimeObject: true},
	{Name: "FlagParse", Type: reflect.TypeFor[types.FlagParse](), RuntimeObject: true},
	{Name: "PipeRecord", Type: reflect.TypeFor[types.PipeRecord](), RuntimeObject: true},
	{Name: "Artifact", Type: reflect.TypeFor[types.TargetArtifact](), RuntimeObject: true},
	{Name: "ArtifactVersion", Type: reflect.TypeFor[types.ArtifactVersion](), RuntimeObject: true},
	{Name: "Tag", Type: reflect.TypeFor[types.VCSTag](), RuntimeObject: true},
	{Name: "Affected", Type: reflect.TypeFor[types.AffectedResult](), RuntimeObject: true},
	{Name: "ProjectGraph", Type: reflect.TypeFor[types.ProjectGraph](), RuntimeObject: true},
	{Name: "ModuleFieldEntry", Type: reflect.TypeFor[types.ModuleFieldEntry](), RuntimeObject: true},
	{Name: "ModuleMethodEntry", Type: reflect.TypeFor[types.ModuleMethodEntry](), RuntimeObject: true},
	{Name: "ModuleObjectEntry", Type: reflect.TypeFor[types.ModuleObjectEntry](), RuntimeObject: true},
	{Name: "Module", Type: reflect.TypeFor[types.ModuleEntry](), RuntimeObject: true},
	{Name: "ProjectEntry", Type: reflect.TypeFor[types.ProjectEntry](), RuntimeObject: true},
	{Name: "Projects", Type: reflect.TypeFor[types.ProjectsOutput](), RuntimeObject: true},
	// magus\describe.tool()'s report, leaves first.
	{Name: "ToolRow", Type: reflect.TypeFor[types.ToolRow](), RuntimeObject: true},
	{Name: "LifecycleStatus", Type: reflect.TypeFor[types.LifecycleStatus](), RuntimeObject: true},
	{Name: "ToolReport", Type: reflect.TypeFor[types.ToolReport](), RuntimeObject: true},
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
	// The magus\describe namespace's records, each after the records it carries.
	{Name: "SpellVersion", Type: reflect.TypeFor[types.SpellVersion](), RuntimeObject: true},
	{Name: "SpellToolchain", Type: reflect.TypeFor[types.SpellToolchain](), RuntimeObject: true},
	{Name: "Spell", Type: reflect.TypeFor[types.Spell](), RuntimeObject: true},
	{Name: "CharmDeclaration", Type: reflect.TypeFor[types.CharmDeclaration](), RuntimeObject: true},
	{Name: "CharmEntry", Type: reflect.TypeFor[types.CharmEntry](), RuntimeObject: true},
	{Name: "TargetEntry", Type: reflect.TypeFor[types.TargetEntry](), RuntimeObject: true},
	{Name: "TargetRef", Type: reflect.TypeFor[types.TargetRef](), RuntimeObject: true},
	{Name: "CharmTraceStep", Type: reflect.TypeFor[spells.CharmTraceStep](), RuntimeObject: true},
	{Name: "CharmConflict", Type: reflect.TypeFor[spells.CharmConflict](), RuntimeObject: true},
	{Name: "ServiceView", Type: reflect.TypeFor[spells.ServiceView](), RuntimeObject: true},
	{Name: "EvaluatedSpell", Type: reflect.TypeFor[types.EvaluatedSpell](), RuntimeObject: true},
	{Name: "EvaluatedTarget", Type: reflect.TypeFor[types.EvaluatedTarget](), RuntimeObject: true},
	{Name: "EvaluatedProject", Type: reflect.TypeFor[types.EvaluatedProject](), RuntimeObject: true},
	// Must precede WorkspaceEntry, which holds a [ListedWorkspace] list.
	{Name: "ListedWorkspace", Type: reflect.TypeFor[types.ListedWorkspace](), RuntimeObject: true},
	{Name: "WorkspaceEntry", Type: reflect.TypeFor[types.WorkspaceEntry](), RuntimeObject: true},
	{Name: "Rule", Type: reflect.TypeFor[types.RuleDoc](), RuntimeObject: true},
	{Name: "HarnessWired", Type: reflect.TypeFor[types.HarnessWired](), RuntimeObject: true},
	{Name: "HarnessChange", Type: reflect.TypeFor[types.HarnessChange](), RuntimeObject: true},
	{Name: "HarnessFile", Type: reflect.TypeFor[types.HarnessFile](), RuntimeObject: true},
	{Name: "HarnessInstallation", Type: reflect.TypeFor[types.HarnessInstallation](), RuntimeObject: true},
	{Name: "MCPToolParam", Type: reflect.TypeFor[types.MCPToolParam](), RuntimeObject: true},
	{Name: "MCPTool", Type: reflect.TypeFor[types.MCPTool](), RuntimeObject: true},
	// magus.review's bundle, leaf-first. A Buzz advisor annotating `> Review` gets
	// compile-checked field access on the same shape the console and the CLI read, which is
	// what keeps one definition of review order serving all three.
	// DiffSymbol.checks are [Check], shared with DoctorReport.
	{Name: "Check", Type: reflect.TypeFor[types.Check](), RuntimeObject: true},
	{Name: "DiffPublicPath", Type: reflect.TypeFor[types.DiffPublicPath](), RuntimeObject: true},
	{Name: "DiffSymbol", Type: reflect.TypeFor[types.DiffSymbol](), RuntimeObject: true},
	{Name: "DiffChurn", Type: reflect.TypeFor[types.DiffChurn](), RuntimeObject: true},
	{Name: "DiffTouch", Type: reflect.TypeFor[types.DiffTouch](), RuntimeObject: true},
	{Name: "DiffHunk", Type: reflect.TypeFor[types.DiffHunk](), RuntimeObject: true},
	{Name: "DiffThreadRef", Type: reflect.TypeFor[types.DiffThreadRef](), RuntimeObject: true},
	{Name: "DiffFile", Type: reflect.TypeFor[types.DiffFile](), RuntimeObject: true},
	{Name: "DiffHunkRef", Type: reflect.TypeFor[types.DiffHunkRef](), RuntimeObject: true},
	{Name: "DiffWhy", Type: reflect.TypeFor[types.DiffWhy](), RuntimeObject: true},
	{Name: "DiffStepHunk", Type: reflect.TypeFor[types.DiffStepHunk](), RuntimeObject: true},
	{Name: "DiffStep", Type: reflect.TypeFor[types.DiffStep](), RuntimeObject: true},
	{Name: "DiffGroup", Type: reflect.TypeFor[types.DiffGroup](), RuntimeObject: true},
	{Name: "DiffOrderCount", Type: reflect.TypeFor[types.DiffOrderCount](), RuntimeObject: true},
	{Name: "DiffOrder", Type: reflect.TypeFor[types.DiffOrder](), RuntimeObject: true},
	{Name: "DiffReviewed", Type: reflect.TypeFor[types.DiffReviewed](), RuntimeObject: true},
	{Name: "ReleaseStage", Type: reflect.TypeFor[types.ReleaseStage](), RuntimeObject: true},
	{Name: "DiffAPI", Type: reflect.TypeFor[types.DiffAPI](), RuntimeObject: true},
	{Name: "VCSCheckpoint", Type: reflect.TypeFor[types.VCSCheckpoint](), RuntimeObject: true},
	// A thrown error's shape, and also returned: Diff.conformanceError is one.
	{Name: "Diagnostic", Type: reflect.TypeFor[types.Diagnostic](), RuntimeObject: true, TS: true},
	// Must follow Diagnostic: Result.error is one. Not a RuntimeObject: the
	// encoder is generated per concrete Go type, and the placeholder T is none;
	// a host method returning Result[X] is what brings that instance's encoder.
	{Name: "Result", Type: reflect.TypeFor[types.Result[buzzgen.T]](), TS: true},
	{Name: "DiffUncovered", Type: reflect.TypeFor[types.DiffUncovered](), RuntimeObject: true},
	{Name: "DiffUnread", Type: reflect.TypeFor[types.DiffUnread](), RuntimeObject: true},
	{Name: "Diff", Type: reflect.TypeFor[types.Diff](), RuntimeObject: true},
	{Name: "DoctorSummary", Type: reflect.TypeFor[types.DoctorSummary](), RuntimeObject: true},
	{Name: "DoctorReport", Type: reflect.TypeFor[types.DoctorReport](), RuntimeObject: true},
	{Name: "CleanReport", Type: reflect.TypeFor[types.CleanReport](), RuntimeObject: true},
	// History metrics, leaf-first. Element names are not uniform on purpose:
	// *Entry only where the bare noun collides with the bundle's own name. These are
	// public Buzz names magusfiles annotate with, so do not tidy them.
	{Name: "Node", Type: reflect.TypeFor[types.Node](), RuntimeObject: true},
	{Name: "FileHotspot", Type: reflect.TypeFor[types.FileHotspot](), RuntimeObject: true},
	{Name: "Hotspots", Type: reflect.TypeFor[types.Hotspots](), RuntimeObject: true},
	{Name: "CoChange", Type: reflect.TypeFor[types.CoChange](), RuntimeObject: true},
	{Name: "Affinity", Type: reflect.TypeFor[types.Affinity](), RuntimeObject: true},
	{Name: "OwnershipEntry", Type: reflect.TypeFor[types.OwnershipEntry](), RuntimeObject: true},
	{Name: "Ownership", Type: reflect.TypeFor[types.Ownership](), RuntimeObject: true},
	{Name: "TrendEntry", Type: reflect.TypeFor[types.TrendEntry](), RuntimeObject: true},
	{Name: "Trend", Type: reflect.TypeFor[types.Trend](), RuntimeObject: true},
	{Name: "VolatilityTarget", Type: reflect.TypeFor[types.VolatilityTarget](), RuntimeObject: true},
	{Name: "Volatility", Type: reflect.TypeFor[types.Volatility](), RuntimeObject: true},
	// Graph reads, leaf-first. magus\graph().query and refs return these.
	// KnowledgeGodNode.in is a Buzz keyword; the mirror emits it as a free identifier.
	{Name: "KnowledgeTermScore", Type: reflect.TypeFor[types.KnowledgeTermScore](), RuntimeObject: true},
	{Name: "KnowledgeListRank", Type: reflect.TypeFor[types.KnowledgeListRank](), RuntimeObject: true},
	{Name: "KnowledgeMatchWhy", Type: reflect.TypeFor[types.KnowledgeMatchWhy](), RuntimeObject: true},
	{Name: "Match", Type: reflect.TypeFor[types.KnowledgeMatch](), RuntimeObject: true},
	{Name: "KnowledgeNode", Type: reflect.TypeFor[types.KnowledgeNode](), RuntimeObject: true},
	{Name: "KnowledgeEdge", Type: reflect.TypeFor[types.KnowledgeEdge](), RuntimeObject: true},
	{Name: "EdgeRef", Type: reflect.TypeFor[types.KnowledgeEdgeRef](), RuntimeObject: true},
	{Name: "PathStep", Type: reflect.TypeFor[types.KnowledgePathStep](), RuntimeObject: true},
	{Name: "RefSite", Type: reflect.TypeFor[types.KnowledgeRefSite](), RuntimeObject: true},
	{Name: "KnowledgeGodNode", Type: reflect.TypeFor[types.KnowledgeGodNode](), RuntimeObject: true},
	{Name: "KnowledgeOrphan", Type: reflect.TypeFor[types.KnowledgeOrphan](), RuntimeObject: true},
	{Name: "KnowledgeDocCoverage", Type: reflect.TypeFor[types.KnowledgeDocCoverage](), RuntimeObject: true},
	{Name: "KnowledgeRanking", Type: reflect.TypeFor[types.KnowledgeRanking](), RuntimeObject: true},
	{Name: "QueryResult", Type: reflect.TypeFor[types.KnowledgeQueryOutput](), RuntimeObject: true},
	{Name: "PublicSymbol", Type: reflect.TypeFor[types.PublicSymbol](), RuntimeObject: true},
	{Name: "ExplainResult", Type: reflect.TypeFor[types.KnowledgeExplainOutput](), RuntimeObject: true},
	{Name: "PathResult", Type: reflect.TypeFor[types.KnowledgePathOutput](), RuntimeObject: true},
	{Name: "RefsResult", Type: reflect.TypeFor[types.KnowledgeRefsOutput](), RuntimeObject: true},
	{Name: "KnowledgeStats", Type: reflect.TypeFor[types.KnowledgeStats](), RuntimeObject: true},
	{Name: "OutputRecord", Type: reflect.TypeFor[types.OutputRecord](), RuntimeObject: true},
	{Name: "ProjectRef", Type: reflect.TypeFor[types.ProjectRef](), RuntimeObject: true},
	{Name: "KnowledgeSymbolGap", Type: reflect.TypeFor[types.KnowledgeSymbolGap](), RuntimeObject: true},
	{Name: "Graph", Type: reflect.TypeFor[types.SymbolIndexDigest](), RuntimeObject: true},
	{Name: "DirCall", Type: reflect.TypeFor[types.DirCall](), RuntimeObject: true},
	{Name: "Dir", Type: reflect.TypeFor[types.Dir](), RuntimeObject: true},
	{Name: "Layer", Type: reflect.TypeFor[types.Layer](), RuntimeObject: true},
	{Name: "KnowledgeFold", Type: reflect.TypeFor[types.KnowledgeFold](), RuntimeObject: true},
	{Name: "NeighborhoodResult", Type: reflect.TypeFor[types.KnowledgeNeighborhoodOutput](), RuntimeObject: true},
	// Inbound only, like the option records below: a script hands these to magus and never reads one back.
	{Name: "DirsOptions", Type: reflect.TypeFor[types.DirsOptions]()},
	{Name: "NeighborhoodOptions", Type: reflect.TypeFor[types.KnowledgeNeighborhoodOptions]()},
	{Name: "PathOptions", Type: reflect.TypeFor[types.KnowledgePathOptions]()},
	// The host doors' option records (types/host_options.go), inbound only: a script hands
	// one to magus and never reads one back. A name another module's record also takes is
	// scoped to its module.
	{Name: "QueryOptions", Type: reflect.TypeFor[types.KnowledgeQueryOptions]()},
	{Name: "RefsOptions", Type: reflect.TypeFor[types.KnowledgeRefsOptions]()},
	{Name: "HistoryMetricsOptions", Type: reflect.TypeFor[types.HistoryMetricsOptions]()},
	{Name: "ExecOptions", Type: reflect.TypeFor[types.ExecOptions]()},
	{Name: "CommandOptions", Module: "magus", Type: reflect.TypeFor[types.MagusCommandOptions]()},
	{Name: "CommandOptions", Module: "vcs", Type: reflect.TypeFor[types.VCSCommandOptions]()},
	{Name: "DiffOptions", Module: "magus", Type: reflect.TypeFor[types.MagusDiffOptions]()},
	// RuntimeObject because magus\diagnosticCode hands one back; magus\raise takes it in.
	{Name: "DiagnosticCode", Type: reflect.TypeFor[types.DiagnosticCodeRef](), RuntimeObject: true},
	{Name: "RaiseOptions", Type: reflect.TypeFor[types.RaiseOptions]()},
	{Name: "SkillsOptions", Type: reflect.TypeFor[types.SkillsOptions]()},
	{Name: "Options", Module: "http", Type: reflect.TypeFor[types.HTTPOptions]()},
	{Name: "ServerOptions", Module: "http", Type: reflect.TypeFor[types.HTTPServerOptions]()},
	{Name: "Retry", Module: "http", Type: reflect.TypeFor[types.HTTPRetry]()},
	{Name: "Options", Module: "archive", Type: reflect.TypeFor[types.ArchiveOptions]()},
	{Name: "RetryOptions", Type: reflect.TypeFor[types.RetryOptions]()},
	// Registered because KnowledgeAnswer carries them: a struct field on a registered Buzz
	// object must itself be registered, or the generated encoder calls one the field's
	// type does not have.
	{Name: "KnowledgeTextPresence", Type: reflect.TypeFor[types.KnowledgeTextPresence](), RuntimeObject: true},
	{Name: "KnowledgeIndexCause", Type: reflect.TypeFor[types.KnowledgeIndexCause](), RuntimeObject: true},
	{Name: "KnowledgeAnswer", Type: reflect.TypeFor[types.KnowledgeAnswer](), RuntimeObject: true},
	{Name: "UnreferencedEntry", Type: reflect.TypeFor[types.UnreferencedEntry](), RuntimeObject: true},
	{Name: "Unreferenced", Type: reflect.TypeFor[types.Unreferenced](), RuntimeObject: true},
	{Name: "DuplicationSite", Type: reflect.TypeFor[types.DuplicationSite](), RuntimeObject: true},
	{Name: "DuplicationGroup", Type: reflect.TypeFor[types.DuplicationGroup](), RuntimeObject: true},
	{Name: "DuplicationHistory", Type: reflect.TypeFor[types.DuplicationHistory](), RuntimeObject: true},
	{Name: "Duplication", Type: reflect.TypeFor[types.Duplication](), RuntimeObject: true},
	// The conformance lens, leaf first: ConformanceRow carries a ConformanceKey and [ConformanceItem], and
	// Conformance carries [ConformanceRow], [ConformanceCeiling] and [ConformanceUnmeasured].
	// It rides the history-metrics board and magus\change().conformance.
	{Name: "ConformanceKey", Type: reflect.TypeFor[types.ConformanceKey](), RuntimeObject: true},
	{Name: "ConformanceItem", Type: reflect.TypeFor[types.ConformanceItem](), RuntimeObject: true},
	{Name: "ConformanceCeiling", Type: reflect.TypeFor[types.ConformanceCeiling](), RuntimeObject: true},
	{Name: "ConformanceUnmeasured", Type: reflect.TypeFor[types.ConformanceUnmeasured](), RuntimeObject: true},
	{Name: "ConformanceRow", Type: reflect.TypeFor[types.ConformanceRow](), RuntimeObject: true},
	{Name: "Conformance", Type: reflect.TypeFor[types.Conformance](), RuntimeObject: true},
	{Name: "FunctionGap", Type: reflect.TypeFor[types.FunctionGap](), RuntimeObject: true},
	{Name: "ChangeRiskFunction", Type: reflect.TypeFor[types.ChangeRiskFunction](), RuntimeObject: true},
	{Name: "ChangeRisk", Type: reflect.TypeFor[types.ChangeRisk](), RuntimeObject: true},
	// There is deliberately no ModelTier object: that name is the workspace's own Buzz enum.
	{Name: "ModelTierRecord", Type: reflect.TypeFor[types.ModelTierRecord](), RuntimeObject: true},
	{Name: "ModelTiers", Type: reflect.TypeFor[types.ModelTiers](), RuntimeObject: true},
	{Name: "GroupingRecord", Type: reflect.TypeFor[types.GroupingRecord](), RuntimeObject: true},
	{Name: "Grouping", Type: reflect.TypeFor[types.Grouping](), RuntimeObject: true},
	{Name: "ImpactCoverage", Type: reflect.TypeFor[types.ImpactCoverage](), RuntimeObject: true},
	{Name: "ImpactSymbol", Type: reflect.TypeFor[types.ImpactSymbol](), RuntimeObject: true},
	{Name: "ImpactFileCoverage", Type: reflect.TypeFor[types.ImpactFileCoverage](), RuntimeObject: true},
	{Name: "ImpactProject", Type: reflect.TypeFor[types.ImpactProject](), RuntimeObject: true},
	{Name: "Impact", Type: reflect.TypeFor[types.ImpactResult](), RuntimeObject: true},
	{Name: "ImportGraph", Type: reflect.TypeFor[types.ImportGraph](), RuntimeObject: true},
	{Name: "TargetRun", Type: reflect.TypeFor[daemon.TargetRun](), RuntimeObject: true},
	{Name: "Run", Type: reflect.TypeFor[daemon.Run](), RuntimeObject: true},
	// magus\job's bundle (put/list), leaf-first: Job.releases and
	// JobList.overlaps are each a list of the other two, and Job.registeredBy is
	// an Origin.
	{Name: "JobRelease", Type: reflect.TypeFor[types.JobRelease](), RuntimeObject: true},
	{Name: "JobDeferred", Type: reflect.TypeFor[types.JobDeferred](), RuntimeObject: true},
	{Name: "JobUnmerged", Type: reflect.TypeFor[types.JobUnmerged](), RuntimeObject: true},
	{Name: "JobUnmergedReport", Type: reflect.TypeFor[types.JobUnmergedReport](), RuntimeObject: true},
	{Name: "JobForkFacts", Type: reflect.TypeFor[types.JobForkFacts](), RuntimeObject: true},
	{Name: "JobUnattributedWrite", Type: reflect.TypeFor[types.JobUnattributedWrite](), RuntimeObject: true},
	// Leaf first: Origin.credential is a Credential, whose grant is a Grant.
	{Name: "Grant", Type: reflect.TypeFor[types.Grant](), RuntimeObject: true},
	{Name: "Credential", Type: reflect.TypeFor[types.Credential](), RuntimeObject: true},
	{Name: "Origin", Type: reflect.TypeFor[types.Origin](), RuntimeObject: true},
	{Name: "JobEntry", Type: reflect.TypeFor[types.JobEntry](), RuntimeObject: true},
	{Name: "LeaseCheck", Type: reflect.TypeFor[types.LeaseCheck](), RuntimeObject: true},
	{Name: "Goal", Type: reflect.TypeFor[types.Goal](), RuntimeObject: true},
	{Name: "VerificationEvidence", Type: reflect.TypeFor[types.VerificationEvidence](), RuntimeObject: true},
	{Name: "JobResult", Type: reflect.TypeFor[types.JobResult](), RuntimeObject: true},
	{Name: "JobResultValidation", Type: reflect.TypeFor[types.JobResultValidation](), RuntimeObject: true},
	{Name: "JobAttempt", Type: reflect.TypeFor[types.JobAttempt](), RuntimeObject: true},
	{Name: "JobGateAttempt", Type: reflect.TypeFor[types.JobGateAttempt](), RuntimeObject: true},
	{Name: "JobIntegration", Type: reflect.TypeFor[types.JobIntegration](), RuntimeObject: true},
	{Name: "JobRun", Type: reflect.TypeFor[types.JobRun](), RuntimeObject: true},
	{Name: "JobWorktree", Type: reflect.TypeFor[types.JobWorktree](), RuntimeObject: true},
	// Model tiers. A magusfile's bounds function and a harness spell build the first two,
	// so they carry no encoder; a matrix function is handed the request, whose history is
	// a list of records. There is deliberately no ModelTier object: that name is the
	// workspace's own Buzz enum.
	{Name: "ModelTierBounds", Type: reflect.TypeFor[types.ModelTierBounds]()},
	// The harness contract's records (types/harness_contract.go), leaf first: a harness
	// spell's mgs_ functions return all but ShippedAgent, which magus hands
	// mgs_renderAgents.
	{Name: "HarnessConfig", Type: reflect.TypeFor[types.HarnessConfig]()},
	{Name: "HarnessAsset", Type: reflect.TypeFor[types.HarnessAsset]()},
	{Name: "Harness", Type: reflect.TypeFor[types.Harness]()},
	{Name: "HarnessSkills", Type: reflect.TypeFor[types.HarnessSkills]()},
	{Name: "HookCommand", Type: reflect.TypeFor[types.HookCommand]()},
	{Name: "HookBinding", Type: reflect.TypeFor[types.HookBinding]()},
	{Name: "ManagedEntries", Type: reflect.TypeFor[types.ManagedEntries]()},
	{Name: "McpServer", Type: reflect.TypeFor[types.McpServer]()},
	{Name: "Price", Type: reflect.TypeFor[types.Price]()},
	{Name: "PriceStep", Type: reflect.TypeFor[types.PriceStep]()},
	{Name: "ModelRow", Type: reflect.TypeFor[types.ModelRow]()},
	{Name: "HarnessTool", Type: reflect.TypeFor[types.HarnessTool]()},
	{Name: "HarnessTools", Type: reflect.TypeFor[types.HarnessTools]()},
	{Name: "AgentFile", Type: reflect.TypeFor[types.AgentFile]()},
	{Name: "Prompt", Type: reflect.TypeFor[types.Prompt]()},
	{Name: "Setting", Type: reflect.TypeFor[types.Setting]()},
	{Name: "Limits", Type: reflect.TypeFor[types.Limits]()},
	// The image module's records, leaf first. The module namespaces them, so the Buzz names
	// drop the Image prefix.
	{Name: "Platform", Type: reflect.TypeFor[image.Platform](), RuntimeObject: true},
	{Name: "Digest", Type: reflect.TypeFor[image.Digest](), RuntimeObject: true},
	{Name: "Descriptor", Type: reflect.TypeFor[image.Descriptor](), RuntimeObject: true},
	{Name: "Entry", Type: reflect.TypeFor[image.Entry](), RuntimeObject: true},
	{Name: "AddedLayer", Type: reflect.TypeFor[image.AddedLayer](), RuntimeObject: true},
	{Name: "Layout", Type: reflect.TypeFor[image.Layout](), RuntimeObject: true},
	{Name: "File", Type: reflect.TypeFor[image.File]()},
	{Name: "Tree", Type: reflect.TypeFor[image.Tree]()},
	{Name: "Symlink", Type: reflect.TypeFor[image.Symlink]()},
	{Name: "Directory", Type: reflect.TypeFor[image.Directory]()},
	{Name: "Config", Type: reflect.TypeFor[image.Config]()},
	{Name: "Spec", Type: reflect.TypeFor[image.Spec]()},
	{Name: "RegistryAuth", Type: reflect.TypeFor[image.RegistryAuth]()},
	{Name: "Pushed", Type: reflect.TypeFor[image.Pushed](), RuntimeObject: true},
	{Name: "Dependency", Type: reflect.TypeFor[image.Dependency]()},
	{Name: "Provenance", Type: reflect.TypeFor[image.Provenance]()},
	{Name: "Document", Type: reflect.TypeFor[image.Document](), RuntimeObject: true},
	{Name: "ShippedAgent", Type: reflect.TypeFor[types.ShippedAgent](), RuntimeObject: true},
	{Name: "JobModelTier", Type: reflect.TypeFor[types.JobModelTier](), RuntimeObject: true},
	{Name: "ModelTierRequest", Type: reflect.TypeFor[types.ModelTierRequest](), RuntimeObject: true},
	// Leaf first: Job.prior is [PriorAttempt], whose denials and retry are these two.
	{Name: "JobDenial", Type: reflect.TypeFor[types.JobDenial](), RuntimeObject: true},
	{Name: "RetryVerdict", Type: reflect.TypeFor[types.RetryVerdict](), RuntimeObject: true},
	{Name: "PriorAttempt", Type: reflect.TypeFor[types.PriorAttempt](), RuntimeObject: true},
	{Name: "JobRejection", Type: reflect.TypeFor[types.JobRejection](), RuntimeObject: true},
	{Name: "Deviation", Type: reflect.TypeFor[types.Deviation](), RuntimeObject: true},
	{Name: "ClaimEvidence", Type: reflect.TypeFor[types.ClaimEvidence](), RuntimeObject: true},
	{Name: "JobClaim", Type: reflect.TypeFor[types.JobClaim](), RuntimeObject: true},
	{Name: "JobClaimRequest", Type: reflect.TypeFor[types.JobClaimRequest]()},
	{Name: "Job", Type: reflect.TypeFor[types.Job](), RuntimeObject: true},
	// Declaration is Job's INPUT twin: a magusfile can construct one, but nothing hands one
	// back out, so it carries no RuntimeObject encoder.
	{Name: "Declaration", Type: reflect.TypeFor[types.Declaration]()},
	// A lint rule WRITES Finding and the magus/lint source module carries its mirror, so
	// no Go-to-Buzz encoder is needed.
	{Name: "Finding", Type: reflect.TypeFor[types.Finding]()},
	{Name: "FileChange", Type: reflect.TypeFor[types.FileChange](), RuntimeObject: true},
	{Name: "RegionChange", Type: reflect.TypeFor[types.RegionChange](), RuntimeObject: true},
	{Name: "FileStat", Type: reflect.TypeFor[types.FileStat](), RuntimeObject: true},
	{Name: "JobOverlapFootprint", Type: reflect.TypeFor[types.JobOverlapFootprint](), RuntimeObject: true},
	{Name: "JobOverlap", Type: reflect.TypeFor[types.JobOverlap](), RuntimeObject: true},
	{Name: "JobBlock", Type: reflect.TypeFor[types.JobBlock](), RuntimeObject: true},
	{Name: "JobReadOnly", Type: reflect.TypeFor[types.JobReadOnly](), RuntimeObject: true},
	{Name: "InflightNeighbour", Type: reflect.TypeFor[types.InflightNeighbour](), RuntimeObject: true},
	{Name: "InflightChange", Type: reflect.TypeFor[types.InflightChange](), RuntimeObject: true},
	{Name: "InflightFetch", Type: reflect.TypeFor[types.InflightFetch](), RuntimeObject: true},
	{Name: "JobList", Type: reflect.TypeFor[types.JobList](), RuntimeObject: true},
	{Name: "JobStatus", Type: reflect.TypeFor[types.JobStatus](), RuntimeObject: true},
	{Name: "VerificationStatus", Type: reflect.TypeFor[types.VerificationStatus](), RuntimeObject: true},
	// magus\guard.spawn's and magus\guard.command's requests, after Job because each
	// request's lease is one, and the verdict both return. The guard hands a rule the
	// request and the rule hands back the verdict, so all need encoders: allow/advise/deny
	// build the verdict on the Go side.
	// Before the three requests, which each carry the caller's.
	{Name: "ContextWindow", Type: reflect.TypeFor[types.ContextWindow](), RuntimeObject: true},
	{Name: "SpawnTarget", Type: reflect.TypeFor[types.SpawnTarget](), RuntimeObject: true},
	{Name: "SpawnRequest", Type: reflect.TypeFor[types.SpawnRequest](), RuntimeObject: true},
	{Name: "VCSInvocation", Type: reflect.TypeFor[types.VCSInvocation](), RuntimeObject: true},
	{Name: "CommandInvocation", Type: reflect.TypeFor[types.CommandInvocation](), RuntimeObject: true},
	{Name: "CheckoutState", Type: reflect.TypeFor[types.CheckoutState](), RuntimeObject: true},
	{Name: "CommandRequest", Type: reflect.TypeFor[types.CommandRequest](), RuntimeObject: true},
	{Name: "WriteRequest", Type: reflect.TypeFor[types.WriteRequest](), RuntimeObject: true},
	{Name: "StopRequest", Type: reflect.TypeFor[types.StopRequest](), RuntimeObject: true},
	{Name: "GuardVerdict", Type: reflect.TypeFor[types.GuardVerdict](), RuntimeObject: true},
	{Name: "GuardBinary", Type: reflect.TypeFor[types.GuardBinary](), RuntimeObject: true},
	// Change.tier takes TierOptions and returns TierReport, leaf first.
	{Name: "TierOptions", Type: reflect.TypeFor[types.TierOptions]()},
	{Name: "TierEvidence", Type: reflect.TypeFor[types.TierEvidence](), RuntimeObject: true},
	{Name: "TierGateStep", Type: reflect.TypeFor[types.TierGateStep](), RuntimeObject: true},
	{Name: "TierReport", Type: reflect.TypeFor[types.TierReport](), RuntimeObject: true},
	{Name: "Skill", Type: reflect.TypeFor[types.Skill](), RuntimeObject: true},
	// magus\service.acquire returns it and magus\service.release takes it back.
	{Name: "ServiceLease", Type: reflect.TypeFor[types.ServiceLease](), RuntimeObject: true},
	// magus\trail's records, leaf first: Trail holds lists of the two before it.
	{Name: "TrailObservation", Type: reflect.TypeFor[trail.Observation](), RuntimeObject: true},
	{Name: "TrailSpawn", Type: reflect.TypeFor[trail.Spawn](), RuntimeObject: true},
	{Name: "Trail", Type: reflect.TypeFor[trail.Trail](), RuntimeObject: true},
	{Name: "TrailMark", Type: reflect.TypeFor[trail.Mark](), RuntimeObject: true},
	// A buzz-format brief is one magus\Brief literal the guard type-checks; nothing hands
	// one back, so none is a runtime object. Leaf first: Brief holds lists of the two.
	{Name: "BriefDecision", Type: reflect.TypeFor[types.BriefDecision]()},
	{Name: "BriefRef", Type: reflect.TypeFor[types.BriefRef]()},
	{Name: "Brief", Type: reflect.TypeFor[types.Brief]()},
	// The magus root constructors' records and their methods' reports.
	{Name: "Workspace", Type: reflect.TypeFor[types.MagusWorkspace](), RuntimeObject: true},
	{Name: "ChangeOptions", Type: reflect.TypeFor[types.ChangeOptions]()},
	{Name: "Change", Type: reflect.TypeFor[types.Change](), RuntimeObject: true},
	{Name: "History", Type: reflect.TypeFor[types.History](), RuntimeObject: true},
	{Name: "SessionOptions", Type: reflect.TypeFor[trail.ReadOptions]()},
	{Name: "Session", Type: reflect.TypeFor[types.Session](), RuntimeObject: true},
	{Name: "JobRegistration", Type: reflect.TypeFor[types.JobRegistration](), RuntimeObject: true},
	{Name: "PrecedentScope", Type: reflect.TypeFor[types.PrecedentScope](), RuntimeObject: true},
	{Name: "PrecedentKey", Type: reflect.TypeFor[types.PrecedentKey](), RuntimeObject: true},
	{Name: "SymbolIndexStatus", Type: reflect.TypeFor[types.SymbolIndexStatus](), RuntimeObject: true},
	{Name: "Case", Type: reflect.TypeFor[types.Case](), RuntimeObject: true},
	{Name: "Precedent", Type: reflect.TypeFor[types.Precedent](), RuntimeObject: true},
	{Name: "Holds", Type: reflect.TypeFor[types.Holds](), RuntimeObject: true},
	{Name: "SymbolDecl", Type: reflect.TypeFor[types.SymbolDecl](), RuntimeObject: true},
	{Name: "Symbols", Type: reflect.TypeFor[types.Symbols](), RuntimeObject: true},
	{Name: "ConformanceReport", Type: reflect.TypeFor[types.ConformanceReport](), RuntimeObject: true},
	{Name: "FileRef", Type: reflect.TypeFor[types.FileRef](), RuntimeObject: true},
	{Name: "AttentionRequest", Type: reflect.TypeFor[types.AttentionRequest](), RuntimeObject: true},
	{Name: "AttentionList", Type: reflect.TypeFor[types.AttentionList](), RuntimeObject: true},
}

// boundaryEnumTypes are the Go types .buzzgen.yaml lists as
// enums. buzzgen derives their cases; this only resolves each type.
var boundaryEnumTypes = []reflect.Type{
	reflect.TypeFor[types.KnowledgeResolution](),
	reflect.TypeFor[types.DiagnosticOrigin](),
	reflect.TypeFor[types.SignAlgorithm](),
	reflect.TypeFor[types.TermStyle](),
	reflect.TypeFor[types.TimeLayout](),
	reflect.TypeFor[types.WalkStep](),
	reflect.TypeFor[types.LogLevel](),
	reflect.TypeFor[types.PlatformStyle](),
	reflect.TypeFor[types.CheckStatus](),
	reflect.TypeFor[types.EventOutcome](),
	reflect.TypeFor[types.EventSeverity](),
	reflect.TypeFor[daemon.ServiceState](),
	reflect.TypeFor[types.PatternType](),
	reflect.TypeFor[types.SymbolIndexFreshness](),
	reflect.TypeFor[types.DiffUncoveredReason](),
	reflect.TypeFor[types.DiffReadState](),
	reflect.TypeFor[types.DiffGroupKind](),
	reflect.TypeFor[types.DiffWhyRelation](),
	reflect.TypeFor[daemon.TargetRunState](),
	reflect.TypeFor[types.VCSSource](),
	reflect.TypeFor[types.ConflictKind](),
	reflect.TypeFor[spells.PatchOpKind](),
	reflect.TypeFor[spells.VersionComponent](),
	reflect.TypeFor[spells.SymbolFormat](),
	reflect.TypeFor[spells.CoverageFormat](),
	reflect.TypeFor[spells.External](),
	reflect.TypeFor[spells.SandboxAccess](),
	reflect.TypeFor[types.SkillVariant](),
	reflect.TypeFor[types.JobKind](),
	reflect.TypeFor[types.SkillForm](),
	reflect.TypeFor[spells.DiscoveryRole](),
	reflect.TypeFor[types.ToolCapability](),
	reflect.TypeFor[types.HookPoint](),
	reflect.TypeFor[types.BindingStatus](),
	reflect.TypeFor[types.McpTransport](),
	reflect.TypeFor[types.ModelTierSource](),
	reflect.TypeFor[types.GroupingShape](),
	reflect.TypeFor[types.BriefFormat](),
	reflect.TypeFor[types.BriefRefKind](),
	reflect.TypeFor[types.ClaimKind](),
	reflect.TypeFor[image.OS](),
	reflect.TypeFor[image.Architecture](),
	reflect.TypeFor[image.Variant](),
	reflect.TypeFor[image.MediaType](),
	reflect.TypeFor[image.ModePolicy](),
	reflect.TypeFor[image.SymlinkPolicy](),
	reflect.TypeFor[image.EntryKind](),
	reflect.TypeFor[types.GroupingVerdict](),
	reflect.TypeFor[types.AgentBilling](),
	reflect.TypeFor[types.AgentRole](),
	reflect.TypeFor[types.AgentThread](),
	reflect.TypeFor[types.BrokerPolicy](),
	reflect.TypeFor[types.ChangeStatus](),
	reflect.TypeFor[types.CommentAnchorRung](),
	reflect.TypeFor[types.ConcurrencyProfile](),
	reflect.TypeFor[types.ConformanceMetric](),
	reflect.TypeFor[types.ConformancePopulation](),
	reflect.TypeFor[types.ContextSource](),
	reflect.TypeFor[types.CredentialKind](),
	reflect.TypeFor[types.DelegableClass](),
	reflect.TypeFor[types.DiffAuthor](),
	reflect.TypeFor[types.DriftPolicy](),
	reflect.TypeFor[types.DuplicationPlacement](),
	reflect.TypeFor[types.DuplicationShape](),
	reflect.TypeFor[types.EdgeDirection](),
	reflect.TypeFor[types.EntryPoint](),
	reflect.TypeFor[types.Evidence](),
	reflect.TypeFor[types.GoalExpect](),
	reflect.TypeFor[types.GoalKind](),
	reflect.TypeFor[types.GuardDecision](),
	reflect.TypeFor[types.GuardProfile](),
	reflect.TypeFor[types.HarnessChangeOp](),
	reflect.TypeFor[types.InflightAttention](),
	reflect.TypeFor[types.JobBaseVerdict](),
	reflect.TypeFor[types.JobHolder](),
	reflect.TypeFor[types.JobState](),
	reflect.TypeFor[types.JobWriteProof](),
	reflect.TypeFor[types.KnowledgeDefinitionStatus](),
	reflect.TypeFor[types.KnowledgeUnknownReason](),
	reflect.TypeFor[types.KnowledgeVerdict](),
	reflect.TypeFor[types.LeaseSource](),
	reflect.TypeFor[daemon.ListenerKind](),
	reflect.TypeFor[types.MarkerFamily](),
	reflect.TypeFor[types.MarkerVerb](),
	reflect.TypeFor[types.Orchestration](),
	reflect.TypeFor[types.PrecedentFamily](),
	reflect.TypeFor[daemon.ReadinessName](),
	reflect.TypeFor[daemon.Readiness](),
	reflect.TypeFor[types.RegionSide](),
	reflect.TypeFor[types.RelationID](),
	reflect.TypeFor[types.RetryChange](),
	reflect.TypeFor[types.Tier](),
	reflect.TypeFor[types.SandboxMode](),
	reflect.TypeFor[types.Scope](),
	reflect.TypeFor[types.SlotSource](),
	reflect.TypeFor[types.SpawnKind](),
	reflect.TypeFor[types.SuggestionStatus](),
	reflect.TypeFor[types.SymbolOccurrenceStatus](),
	reflect.TypeFor[types.ThreadSource](),
	reflect.TypeFor[trail.Section](),
	reflect.TypeFor[trail.Verdict](),
	reflect.TypeFor[types.VCSCapability](),
	reflect.TypeFor[daemon.WorkspaceState](),
}

// openStringTypes are the named string types that cross as a bare str on purpose, each
// with the reason its values are not a closed set. A named string type in neither this
// list nor boundaryEnums is refused by the mirror generator: crossing as str, a typo in
// a closed set compares false where the checker could have refused it.
var openStringTypes = map[reflect.Type]string{
	reflect.TypeFor[types.ProjectOrigin]():   "a provider-declared project carries its provider's name",
	reflect.TypeFor[diagnostics.Code]():      "a workspace mints its own codes through magus\\raise beside magus's catalog",
	reflect.TypeFor[events.EventType](): "a subscriber skips a type it does not know, so new types are added without a release of every reader",
	reflect.TypeFor[types.ReviewVerdict]():   "a review provider op carries it, and a provider op's request encoder writes no session enum case",
	// The merge queue provider script compares these as str. They become enums with that
	// script's typed rewrite (job queue-github-typed), which a str field here keeps checking.
	reflect.TypeFor[queuetypes.StackMerge]():  "the queue provider script reads it as str until its typed rewrite",
	reflect.TypeFor[queuetypes.MergeMethod](): "the queue provider script reads it as str until its typed rewrite",
	reflect.TypeFor[queuetypes.Mark]():        "the queue provider script reads it as str until its typed rewrite",
	reflect.TypeFor[queuetypes.CommitState](): "the queue provider script reads it as str until its typed rewrite",
	reflect.TypeFor[queuetypes.Code]():        "the queue provider script reads it as str until its typed rewrite",
	reflect.TypeFor[queuetypes.CarryTier]():   "the queue provider script reads it as str until its typed rewrite",
}

// checkNamedStrings refuses a field of rt whose type, or element type, is a named string
// in neither boundaryEnums nor openStringTypes.
func checkNamedStrings(rt reflect.Type) error {
	var unregistered []string
	for _, f := range boundaryFields(rt) {
		if t, found := unregisteredString(f.Type); found {
			unregistered = append(unregistered, fmt.Sprintf("%s (%s)", f.Name, t))
		}
	}
	if len(unregistered) > 0 {
		return fmt.Errorf("%s: field %s is a named string type: register it in boundaryEnums, or list it in openStringTypes with the reason its values are open (cmd/magus-utils/boundary_types.go)",
			rt, strings.Join(unregistered, ", "))
	}
	return nil
}

// unregisteredString is the named string type a field of type ft holds, looking through
// slices, arrays, pointers and maps, when that type is in neither boundaryEnums nor
// openStringTypes.
func unregisteredString(ft reflect.Type) (reflect.Type, bool) {
	ft = innermost(ft)
	_, enum := buzzEnum(ft)
	_, listed := openStringTypes[ft]
	return ft, ft.Kind() == reflect.String && ft.PkgPath() != "" && !enum && !listed
}

// innermost is the type ft holds through any slices, arrays, pointers and maps.
func innermost(ft reflect.Type) reflect.Type {
	for ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array || ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Map {
		ft = ft.Elem()
	}
	return ft
}

type boundaryEnum struct {
	Name  string
	Type  reflect.Type
	Cases []enumCase
	// Module declares the enum in that host module's bundle although no signature or
	// field names it: named values for a field that is a str because its set is open.
	Module string
}

// enumCase is one case: the Buzz identifier and the string it carries.
type enumCase struct {
	Name  string
	Value string
}

// buzzEnum returns the enum a Go type mirrors as, if any.
func buzzEnum(rt reflect.Type) (boundaryEnum, bool) {
	for _, e := range boundaryEnumList() {
		if e.Type == rt {
			return e, true
		}
	}
	return boundaryEnum{}, false
}

type boundaryType struct {
	Name string
	// Module scopes Name to one host module's declarations, for a mirror whose name
	// another module's mirror also takes: magus\CommandOptions and vcs\CommandOptions.
	// Empty declares Name for every module.
	Module        string
	Type          reflect.Type
	RuntimeObject bool
	// TS marks a type tstypes also writes as TypeScript for the console and docs.
	TS bool
}

// key is the registry's name for the entry: Name, qualified as module\Name when Module
// scopes it. inboundTypes lists keys.
func (b boundaryType) key() string {
	if b.Module == "" {
		return b.Name
	}
	return fmt.Sprintf(`%s\%s`, b.Module, b.Name)
}

// buzzName returns the Buzz name a Go type mirrors as: its registry key,
// not always its Go name (types.ProjectsOutput is `Projects`). An
// unclaimed type falls back to its Go name, which the caller reports.
func buzzName(rt reflect.Type) string {
	for _, entry := range boundaryTypes {
		if entry.Type == rt {
			return entry.Name
		}
	}
	return rt.Name()
}

// hasMethods reports whether a module declares methods on rt's mirror. Such a
// mirror is encoded as an instance of the session's object, never a map: a map
// has no methods to dispatch.
func hasMethods(rt reflect.Type) bool {
	for _, mod := range hostmodules.All() {
		if _, ok := mirrorObject(mod, rt); ok {
			return true
		}
	}
	return false
}

// mirrorObject finds the std.Object mod declares on rt's mirror.
func mirrorObject(mod std.Module, rt reflect.Type) (std.Object, bool) {
	for _, o := range mod.Objects {
		if entry, ok := boundaryTypeIn(mod.Name, o.Name); ok && entry.Type == rt {
			return o, true
		}
	}
	return std.Object{}, false
}

// boundaryTypeNamed finds an entry by its key: a bare name, or module\Name for one a
// module scopes.
func boundaryTypeNamed(key string) (boundaryType, bool) {
	for _, entry := range boundaryTypes {
		if entry.key() == key {
			return entry, true
		}
	}
	return boundaryType{}, false
}

// boundaryTypeIn finds the entry name means inside module's declarations: the one module
// scopes, else the workspace-wide one.
func boundaryTypeIn(module, name string) (boundaryType, bool) {
	if entry, ok := boundaryTypeNamed(fmt.Sprintf(`%s\%s`, module, name)); ok {
		return entry, true
	}
	return boundaryTypeNamed(name)
}

// boundaryEnumNamed finds a declared enum by its Buzz name. The counterpart to
// boundaryTypeNamed, for the generator that has to declare an enum a signature
// references.
func boundaryEnumNamed(name string) (boundaryEnum, bool) {
	for _, e := range boundaryEnumList() {
		if e.Name == name {
			return e, true
		}
	}
	return boundaryEnum{}, false
}
