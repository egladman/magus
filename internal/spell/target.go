package spell

import (
	_ "embed"
	"strings"
)

// SpellModulePath is the import path of the canonical spell-authored value-types
// module: the shapes a spell op WRITES (Target, Command, Service, Charm, PatchOp),
// as opposed to the shapes a host method RETURNS (ExecResult, Tag, Projects, ...),
// which now ship with the host module that returns them (see host_types.go and
// internal/interp/bindings/modules.go). A spell does `import "magus/spell";` to
// bring Target into scope so its mgs_listTargets can be typed as a map of
// fun(Target) Command handlers, and its provider ops as fun(Target, Req) Resp. The runtime registers the
// module as embedded source (see the buzz bindings' registerMagusModules), and so
// does the session that loads the built-ins.
//
// "magus/spell" does not collide with the "magus/spell/<name>" path each built-in
// spell handle is reachable under (spells.ModulePrefix): those are different exact
// map keys in the session's native-module/declaration tables, and a bare "magus/spell"
// import reads naturally as "the types a spell is built from".
const SpellModulePath = "magus/spell"

// pathSource is the generated lexical filesystem-reference type used by spell
// metadata. It belongs in magus/spell because declarations such as inputs and
// manifests are authored by spells before any host module is invoked.
//
//go:embed gen/types/path.buzz
var pathSource string

// manifestSource is the generated mirror of spells.Manifest: the file a project's
// dependencies are declared in, plus the lockfiles its ecosystem resolves them into.
// It belongs in magus/spell for the same reason Path does (mgs_listManifests is
// authored before any host module is invoked). It must FOLLOW installSource in the
// bundle (Manifest.installs is {str: Install}), which follows commandSource.
//
//go:embed gen/types/manifest.buzz
var manifestSource string

// installSource is the generated mirror of spells.Install: how one lockfile's
// dependencies are materialized.
//
//go:embed gen/types/install.buzz
var installSource string

// targetModuleSource is the generated Buzz `object Target` mirror of types.Target
// (see cmd/magus-utils types), the canonical work-unit value type. It is consumed
// as part of the magus/spell declarations, by workspace spells and built-ins alike.
//
//go:embed gen/types/target.buzz
var targetModuleSource string

// patchOpSource / charmTypeSource / commandSource are the generated Buzz `object`
// mirrors of spells.PatchOp, spells.Charm, and types.Run: the {cmd, args, charms}
// command a command target's handler hands to its cb callback, down to the RFC 6902
// ops. Unlike the other object mirrors they are inlined into self-contained
// built-ins (every command spell references Run), so they ship in the magus/spell
// bundle (see builtinModuleSources). Order matters in that bundle: PatchOp precedes
// Charm (Charm.ops is [PatchOp]) precedes Run (Run.charms is {str: Charm}).
//
//go:embed gen/types/patchop.buzz
var patchOpSource string

//go:embed gen/types/charm.buzz
var charmTypeSource string

// hintSource is the generated mirror of spells.Hint: one {match, then} failure
// classification a command op declares. It must PRECEDE commandSource in the bundle
// (Command.hints is [Hint]), and it references nothing itself.
//
//go:embed gen/types/hint.buzz
var hintSource string

//go:embed gen/types/command.buzz
var commandSource string

// secretSource is the generated mirror of spells.Secret: what a provider spell's
// resolve_secret op returns. It references nothing, so it has no ordering constraint in
// the bundle.
//
//go:embed gen/types/secret.buzz
var secretSource string

// versionKeySource is the generated mirror of spells.VersionKey (what a probed tool
// contributes to the cache key), together with the VersionComponent enum its upTo
// field is typed as. It ships in the magus/spell bundle so a spell can declare
// mgs_getVersionKey, and it carries its own enum, so it has no ordering constraint
// against the others.
//
//go:embed gen/types/versionkey.buzz
var versionKeySource string

// versionBoundsSource is the generated mirror of spells.VersionBounds: the window of
// versions a probed tool is allowed to report, as an inclusive min and an exclusive
// below. It ships in the magus/spell bundle so a spell can declare what its ops need,
// and it references nothing, so it has no ordering constraint against the others.
//
//go:embed gen/types/versionbounds.buzz
var versionBoundsSource string

// toolSource is the generated mirror of spells.Tool: everything a spell declares about
// one binary it drives. It must FOLLOW versionKeySource and versionBoundsSource in the
// bundle (Tool.key is a VersionKey and Tool.supported is a VersionBounds), and Command
// is already declared ahead of all three.
//
//go:embed gen/types/tool.buzz
var toolSource string

// sandboxAllowSource, sandboxEnvSource, sandboxCacheSource and sandboxSource are the
// generated mirrors of what a spell's mgs_getSandbox returns. sandboxSource must follow
// the other three in the bundle: its fields are typed by them.
//
//go:embed gen/types/sandboxallow.buzz
var sandboxAllowSource string

//go:embed gen/types/sandboxenv.buzz
var sandboxEnvSource string

//go:embed gen/types/sandboxcache.buzz
var sandboxCacheSource string

//go:embed gen/types/sandbox.buzz
var sandboxSource string

// commentBlockSource / quoteSource / commentSyntaxSource are the generated mirrors of
// the comment/string syntax a spell declares in mgs_getLanguage's syntax.comments. The
// two leaves must PRECEDE commentSyntaxSource in the bundle (its fields are
// [CommentBlock] and [Quote]); nothing else references them.
//
//go:embed gen/types/commentblock.buzz
var commentBlockSource string

//go:embed gen/types/quote.buzz
var quoteSource string

//go:embed gen/types/commentsyntax.buzz
var commentSyntaxSource string

// stubSyntaxSource and syntaxSource are the generated mirrors of
// spells.StubSyntax and spells.Syntax. Syntax carries a CommentSyntax and a
// StubSyntax and Language carries a Syntax, so the bundle orders them
// CommentSyntax, StubSyntax, Syntax, Language.
//
//go:generate go run ../../cmd/magus-utils types -type StubSyntax -out gen/types/stubsyntax.buzz
//go:embed gen/types/stubsyntax.buzz
var stubSyntaxSource string

// These precede syntaxSource: Syntax carries each, and MutationSyntax
// carries [Swap].
//
//go:generate go run ../../cmd/magus-utils types -type DecisionSyntax -out gen/types/decisionsyntax.buzz
//go:embed gen/types/decisionsyntax.buzz
var decisionSyntaxSource string

//go:generate go run ../../cmd/magus-utils types -type Swap -out gen/types/swap.buzz
//go:embed gen/types/swap.buzz
var swapSource string

//go:generate go run ../../cmd/magus-utils types -type MutationSyntax -out gen/types/mutationsyntax.buzz
//go:embed gen/types/mutationsyntax.buzz
var mutationSyntaxSource string

//go:generate go run ../../cmd/magus-utils types -type Syntax -out gen/types/syntax.buzz
//go:embed gen/types/syntax.buzz
var syntaxSource string

//go:embed gen/types/language.buzz
var languageSource string

// serviceSource is the generated Buzz `object Service` mirror of spells.Service: the
// {command, readiness, stop} a service op returns, each field a Command (command is the
// process; readiness/stop are optional). It ships in the magus/spell bundle so a spell
// can author a service op; it must follow commandSource there (Service's fields are
// typed Command).
//
//go:embed gen/types/service.buzz
var serviceSource string

// symbolIndexerSource is the generated Buzz `object SymbolIndexer` mirror of
// spells.SymbolIndexer: the {format, command} a spell's mgs_getSymbolIndexer returns.
// It must follow commandSource in the bundle (its command field is typed Command).
//
//go:embed gen/types/symbolindexer.buzz
var symbolIndexerSource string

// coverageSource follows commandSource: Coverage.command is a Command.
//
//go:generate go run ../../cmd/magus-utils types -type Coverage -out gen/types/coverage.buzz
//go:embed gen/types/coverage.buzz
var coverageSource string

// projectSource is the generated Buzz `object Project` mirror of spells.Project: one
// project a workspace-provider spell's list_projects contract returns. It ships in
// the magus/spell bundle beside the other shapes a spell AUTHORS; it references no
// other mirror, so its position in that bundle is free.
//
//go:embed gen/types/project.buzz
var projectSource string

// releaseCycleSource and lifecycleSource are the generated mirrors of spells.ReleaseCycle
// and spells.Lifecycle: what a lifecycle provider's list_lifecycles contract returns.
// ReleaseCycle must precede Lifecycle in the bundle (Lifecycle.cycles is [ReleaseCycle]).
//
//go:embed gen/types/releasecycle.buzz
var releaseCycleSource string

//go:embed gen/types/lifecycle.buzz
var lifecycleSource string

// The discover_projects records, leaves first: DiscoveryQuery.files is [DiscoveryFile] and
// DiscoveredProject.notInferred is [DiscoveryGap].
var (
	//go:embed gen/types/discoveryfile.buzz
	discoveryFileSource string
	//go:embed gen/types/discoveryquery.buzz
	discoveryQuerySource string
	//go:embed gen/types/discoverygap.buzz
	discoveryGapSource string
	//go:embed gen/types/discoveredproject.buzz
	discoveredProjectSource string
)

// The provider op records (types/provider_ops.go): what magus hands a provider op and what
// the op returns. None references another, so their order in the bundle is free.
//
//go:generate go run ../../cmd/magus-utils types -type Empty -out gen/types/empty.buzz
//go:generate go run ../../cmd/magus-utils types -type Ack -out gen/types/ack.buzz
//go:generate go run ../../cmd/magus-utils types -type SecretRef -out gen/types/secretref.buzz
//go:generate go run ../../cmd/magus-utils types -type GreenRun -out gen/types/greenrun.buzz
//go:generate go run ../../cmd/magus-utils types -type Strings -out gen/types/strings.buzz
//go:generate go run ../../cmd/magus-utils types -type CIGroup -out gen/types/cigroup.buzz
//go:generate go run ../../cmd/magus-utils types -type CIGroupEnd -out gen/types/cigroupend.buzz
//go:generate go run ../../cmd/magus-utils types -type CIAnnotation -out gen/types/ciannotation.buzz
//go:generate go run ../../cmd/magus-utils types -type ArtifactRequest -out gen/types/artifactrequest.buzz
//go:generate go run ../../cmd/magus-utils types -type PruneRequest -out gen/types/prunerequest.buzz
//go:generate go run ../../cmd/magus-utils types -type PruneResult -out gen/types/pruneresult.buzz
//go:generate go run ../../cmd/magus-utils types -type LifecycleQuery -out gen/types/lifecyclequery.buzz
//go:generate go run ../../cmd/magus-utils types -type FindReviewRequest -out gen/types/findreviewrequest.buzz
//go:generate go run ../../cmd/magus-utils types -type ReviewDraft -out gen/types/reviewdraft.buzz
//go:generate go run ../../cmd/magus-utils types -type PublishReviewRequest -out gen/types/publishreviewrequest.buzz
//go:generate go run ../../cmd/magus-utils types -type ReplyReviewRequest -out gen/types/replyreviewrequest.buzz
//go:generate go run ../../cmd/magus-utils types -type ReviewRef -out gen/types/reviewref.buzz
//go:generate go run ../../cmd/magus-utils types -type ReviewTarget -out gen/types/reviewtarget.buzz
//go:generate go run ../../cmd/magus-utils types -type ReviewComment -out gen/types/reviewcomment.buzz
//go:generate go run ../../cmd/magus-utils types -type ProjectQuery -out gen/types/projectquery.buzz
var (
	//go:embed gen/types/empty.buzz
	emptySource string
	//go:embed gen/types/ack.buzz
	ackSource string
	//go:embed gen/types/secretref.buzz
	secretRefSource string
	//go:embed gen/types/greenrun.buzz
	greenRunSource string
	//go:embed gen/types/strings.buzz
	stringsSource string
	//go:embed gen/types/cigroup.buzz
	ciGroupSource string
	//go:embed gen/types/cigroupend.buzz
	ciGroupEndSource string
	//go:embed gen/types/ciannotation.buzz
	ciAnnotationSource string
	//go:embed gen/types/artifactrequest.buzz
	artifactRequestSource string
	//go:embed gen/types/prunerequest.buzz
	pruneRequestSource string
	//go:embed gen/types/pruneresult.buzz
	pruneResultSource string
	//go:embed gen/types/lifecyclequery.buzz
	lifecycleQuerySource string
	//go:embed gen/types/findreviewrequest.buzz
	findReviewRequestSource string
	//go:embed gen/types/reviewdraft.buzz
	reviewDraftSource string
	//go:embed gen/types/publishreviewrequest.buzz
	publishReviewRequestSource string
	//go:embed gen/types/replyreviewrequest.buzz
	replyReviewRequestSource string
	//go:embed gen/types/reviewref.buzz
	reviewRefSource string
	//go:embed gen/types/reviewtarget.buzz
	reviewTargetSource string
	//go:embed gen/types/reviewcomment.buzz
	reviewCommentSource string
	//go:embed gen/types/projectquery.buzz
	projectQuerySource string
)

// The harness contract's records (types/harness_contract.go): what a harness spell's mgs_
// functions return and the ShippedAgent mgs_renderAgents takes. Each file carries the enum
// its fields name; ModelRow follows Price and PriceStep, and HarnessTools HarnessTool.
//
//go:generate go run ../../cmd/magus-utils types -type HarnessConfig -out gen/types/harnessconfig.buzz
//go:generate go run ../../cmd/magus-utils types -type HarnessAsset -out gen/types/harnessasset.buzz
//go:generate go run ../../cmd/magus-utils types -type Harness -out gen/types/harness.buzz
//go:generate go run ../../cmd/magus-utils types -type HarnessSkills -out gen/types/harnessskills.buzz
//go:generate go run ../../cmd/magus-utils types -type HookCommand -out gen/types/hookcommand.buzz
//go:generate go run ../../cmd/magus-utils types -type HookBinding -out gen/types/hookbinding.buzz
//go:generate go run ../../cmd/magus-utils types -type ManagedEntries -out gen/types/managedentries.buzz
//go:generate go run ../../cmd/magus-utils types -type McpServer -out gen/types/mcpserver.buzz
//go:generate go run ../../cmd/magus-utils types -type Price -out gen/types/price.buzz
//go:generate go run ../../cmd/magus-utils types -type PriceStep -out gen/types/pricestep.buzz
//go:generate go run ../../cmd/magus-utils types -type ModelRow -out gen/types/modelrow.buzz
//go:generate go run ../../cmd/magus-utils types -type HarnessTool -out gen/types/harnesstool.buzz
//go:generate go run ../../cmd/magus-utils types -type HarnessTools -out gen/types/harnesstools.buzz
//go:generate go run ../../cmd/magus-utils types -type AgentFile -out gen/types/agentfile.buzz
//go:generate go run ../../cmd/magus-utils types -type Prompt -out gen/types/prompt.buzz
//go:generate go run ../../cmd/magus-utils types -type Setting -out gen/types/setting.buzz
//go:generate go run ../../cmd/magus-utils types -type Limits -out gen/types/limits.buzz
//go:generate go run ../../cmd/magus-utils types -type ShippedAgent -out gen/types/shippedagent.buzz
var (
	//go:embed gen/types/harnessconfig.buzz
	harnessConfigSource string
	//go:embed gen/types/harnessasset.buzz
	harnessAssetSource string
	//go:embed gen/types/harness.buzz
	harnessSource string
	//go:embed gen/types/harnessskills.buzz
	harnessSkillsSource string
	//go:embed gen/types/hookcommand.buzz
	hookCommandSource string
	//go:embed gen/types/hookbinding.buzz
	hookBindingSource string
	//go:embed gen/types/managedentries.buzz
	managedEntriesSource string
	//go:embed gen/types/mcpserver.buzz
	mcpServerSource string
	//go:embed gen/types/price.buzz
	priceSource string
	//go:embed gen/types/pricestep.buzz
	priceStepSource string
	//go:embed gen/types/modelrow.buzz
	modelRowSource string
	//go:embed gen/types/harnesstool.buzz
	harnessToolSource string
	//go:embed gen/types/harnesstools.buzz
	harnessToolsSource string
	//go:embed gen/types/agentfile.buzz
	agentFileSource string
	//go:embed gen/types/prompt.buzz
	promptSource string
	//go:embed gen/types/setting.buzz
	settingSource string
	//go:embed gen/types/limits.buzz
	limitsSource string
	//go:embed gen/types/shippedagent.buzz
	shippedAgentSource string
)

// HarnessModulePath is the import path of the pure-Buzz harness module: the quoting and
// hook-command builders every harness spell renders its host's files with.
const HarnessModulePath = "magus/harness"

// HarnessModuleSource is the magus/harness module. It imports magus/spell for the
// HookCommand it builds, and calls no host module, so the built-in session offers it.
//
//go:embed harness.buzz
var HarnessModuleSource string

// LintModulePath is the import path of the lint value-types module: the Finding a Buzz
// lint rule returns. Like magus/spell it is source only, so importing it makes Finding
// constructible, which a type declared on the magus namespace is not.
const LintModulePath = "magus/lint"

// LintModuleSource is the magus/lint bundle.
//
//go:generate go run ../../cmd/magus-utils types -type Finding -out gen/types/finding.buzz
//go:embed gen/types/finding.buzz
var LintModuleSource string

// CharmModulePath is the import path of the pure-Buzz charm module.
const CharmModulePath = "magus/charm"

// CharmModuleSource is the pure-Buzz mirror of the charm host module
// (std/charm.go), shipped as the magus/charm declarations. Unlike the
// type mirrors it is hand-written (charm's constructors are logic, not a struct),
// kept in lockstep with the Go module by charm_parity_test. A self-contained
// built-in command spell imports it (`import "magus/charm"`) to build patches with
// charm.after / charm.set / ... instead of hand-written positional pointers; it is
// pure Buzz with no host calls, so the built-in session offers it.
//
//go:embed charm.buzz
var CharmModuleSource string

// SpellModuleSource is the magus/spell bundle: the spell-authored value types in
// their declare-before-use order (PatchOp before Charm before Command before
// Service and SymbolIndexer, each referencing the prior; Target, Project and the provider
// op records have no cross-references so their position is free). Shared by the runtime
// registration (modules.go) and the built-in loader (builtins.go), so the two can't drift
// apart.
var SpellModuleSource = strings.Join([]string{pathSource, targetModuleSource, patchOpSource, charmTypeSource, hintSource, commandSource, installSource, manifestSource, serviceSource, symbolIndexerSource, coverageSource, versionKeySource, versionBoundsSource, toolSource, sandboxAllowSource, sandboxEnvSource, sandboxCacheSource, sandboxSource, commentBlockSource, quoteSource, commentSyntaxSource, stubSyntaxSource, decisionSyntaxSource, swapSource, mutationSyntaxSource, syntaxSource, languageSource, projectSource, secretSource, releaseCycleSource, lifecycleSource,
	discoveryFileSource, discoveryQuerySource, discoveryGapSource, discoveredProjectSource,
	emptySource, ackSource, secretRefSource, greenRunSource, stringsSource, ciGroupSource, ciGroupEndSource, ciAnnotationSource,
	artifactRequestSource, pruneRequestSource, pruneResultSource, lifecycleQuerySource,
	findReviewRequestSource, reviewDraftSource, publishReviewRequestSource, replyReviewRequestSource, reviewRefSource,
	reviewTargetSource, reviewCommentSource, projectQuerySource,
	harnessConfigSource, harnessAssetSource, harnessSource, harnessSkillsSource, hookCommandSource, hookBindingSource, managedEntriesSource, mcpServerSource,
	priceSource, priceStepSource, modelRowSource, harnessToolSource, harnessToolsSource,
	agentFileSource, promptSource, settingSource, limitsSource, shippedAgentSource}, "\n")
