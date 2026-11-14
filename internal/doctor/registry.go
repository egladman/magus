package doctor

import (
	"context"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
)

// checkDef declares one doctor check as data, so the suite can be listed without being
// run.
type checkDef struct {
	// Name identifies the check AND is what the report prints, deliberately one string:
	// an identifier meant to be typed back at magus has to be the one the reader saw,
	// and a separate display title would drift from it. Kebab-case and stable.
	Name string
	// Doc names the check's subject, where the report carries its finding. It is what
	// `magus doctor --list` renders, so a reader can tell whether a check concerns them
	// without triggering it.
	Doc string
	// Code is the MGS diagnostic this check reports under, empty when it reports none.
	Code types.DiagnosticCode
	// Evidence is what this check's verdict normally rests on, a default a
	// run may change. One that could not look sets check.EvidenceUnknown.
	Evidence check.Evidence
	// NeedsWorkspace marks a check that reads r.ws, skipped when the workspace fails to
	// load. The ones that run regardless ask about the host (this process, this
	// machine's sockets, this terminal), which is exactly what is still worth answering
	// when the magusfile is unparsable.
	NeedsWorkspace bool
	// run takes the projects slice even where the check ignores it, keeping the table
	// flat rather than a union of shapes.
	run func(ctx context.Context, r *runner, projects []*types.Project) check.Check
}

// allChecks is every doctor check, in report order.
//
// The order is the reader's, not the scheduler's: host checks answer "is this magus
// working" before workspace checks answer "is this workspace right". `workspace` sits at
// the boundary because everything below it depends on that load succeeding.
var allChecks = []checkDef{
	{
		Name:     "json-codec",
		Doc:      "which encoding/json implementation this binary was built against",
		Evidence: check.EvidenceMeasured,
		run:      func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkJSONCodec() },
	},
	{
		Name:     "sockets",
		Doc:      "the server, broker and per-process pool sockets in the magus socket directory, live and leftover",
		Evidence: check.EvidenceMeasured,
		run:      func(ctx context.Context, r *runner, _ []*types.Project) check.Check { return r.checkStaleSockets(ctx) },
	},
	{
		Name:     "tokens",
		Doc:      "the operator token and the stored tokens: whether each loads, and which are expiring",
		Evidence: check.EvidenceMeasured,
		run:      func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkTokens() },
	},
	{
		Name:           "mcp-client",
		Doc:            "wired harness spells that declare user-owned MCP setup hints (Magus does not write host MCP config)",
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            func(ctx context.Context, r *runner, _ []*types.Project) check.Check { return r.checkMCPClient(ctx) },
	},
	{
		Name:     "terminal-capabilities",
		Doc:      "what the terminal in front of magus can render",
		Evidence: check.EvidenceMeasured,
		run:      func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkTerminal() },
	},
	{
		Name:     "workspace",
		Doc:      "the magusfile loads and its projects are discoverable",
		Evidence: check.EvidenceMeasured,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkWorkspace(projects)
		},
	},
	{
		Name:           "config-file",
		Doc:            "magus.yaml parses and declares no key magus does not know",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkConfigFile() },
	},
	{
		Name:           "cache-writable",
		Doc:            "the cache directory and the entry store every checkout shares exist, and this user can write them",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkCacheWritable() },
	},
	{
		Name:           "concurrency-sizing",
		Doc:            "the configured concurrency fits this machine",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkConcurrencySizing()
		},
	},
	{
		Name:           "memory-declarations",
		Doc:            "a target's declared memory_mb against the peak magus has measured for it",
		Code:           types.MemoryDeclarationDrift,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkMemoryDeclarations(ctx, projects)
		},
	},
	{
		Name:           gateFitsCheck,
		Doc:            "each step of the ci gate claims no more than this machine's whole build budget",
		Code:           types.MachineBudgetExhausted,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkGateFits(ctx, projects)
		},
	},
	{
		Name:     budgetAdmissionsCheck,
		Doc:      "the refusals and long waits the broker recorded for the build budget",
		Code:     types.MachineBudgetExhausted,
		Evidence: check.EvidenceMeasured,
		run: func(ctx context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkBudgetAdmissions(ctx)
		},
	},
	{
		Name:           "timeout-declarations",
		Doc:            "a target's declared timeout against the durations magus has recorded for it",
		Code:           types.TimeoutDeclarationDrift,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkTimeoutDeclarations(ctx, projects)
		},
	},
	{
		Name:           "retry-declarations",
		Doc:            "a target's declared retry_on_volatile against whether its retries have ever rescued a run",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkRetryDeclarations(ctx, projects)
		},
	},
	{
		Name:           "model-tier-fit",
		Doc:            "a model tier's recorded pass rate on a kind of job, and the line that routed those jobs to it",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkModelTierFit(ctx, projects)
		},
	},
	{
		Name:           "conformance-ceiling",
		Doc:            "configured conformance anchors the code now measures tighter than",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkConformanceCeiling(ctx, projects)
		},
	},
	{
		Name:           "unexplained-hotspots",
		Doc:            "whether the files this workspace edits hardest are explained by any note",
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkUnexplainedHotspots(ctx, projects)
		},
	},
	{
		Name:           "cache-yield",
		Doc:            "targets running uncached without declaring skip_cache and a reason",
		Code:           types.TargetNeverReplays,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkCacheYield(projects)
		},
	},
	{
		Name:           "language-coverage",
		Doc:            "projects binding no toolchain spell, so their work is invisible to affected tracking",
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkLanguageCoverage(projects)
		},
	},
	{
		Name:           "ci-target",
		Doc:            "some project declares the ci target that `magus affected ci` keys off",
		Code:           types.NoCITarget,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkCITarget(projects)
		},
	},
	{
		Name:           "magusfile-options-understood",
		Doc:            "magus.project keys this binary dropped, so the policy each declares is not in force",
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkIgnoredOptions(projects)
		},
	},
	{
		Name:           "required-version-covers-schema",
		Doc:            "every magus.project key the workspace uses is covered by its declared required_version",
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkSchemaFloor(projects)
		},
	},
	{
		Name:           "service-duplication",
		Doc:            "service targets across projects that look like copies of one service",
		Code:           types.NearDuplicateServices,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkNearDuplicateServices(projects)
		},
	},
	{
		Name:           "service-suppressions",
		Doc:            "services marked distinct whose near-duplicate no longer exists, so the reason is dead",
		Code:           types.NearDuplicateServices,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkStaleServiceSuppressions(projects)
		},
	},
	{
		Name:           "magusfile-syntax",
		Doc:            "every magusfile parses, with strict-parity errors reported at once",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkMagusfileSyntax(projects)
		},
	},
	{
		Name:           "spell-target-docs",
		Doc:            "every function-handler target of a workspace-local spell carries a doc comment",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkSpellDocs(project.DefaultSpellRegistry().All())
		},
	},
	{
		Name:           "spell-contract",
		Doc:            "what each registered spell implements of the mgs_ contract",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkSpellContract() },
	},
	{
		Name:           "diagnostic-docs",
		Doc:            "every MGS code magus prints routes to a docs page that names a next step",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkDiagnosticDocs() },
	},
	{
		Name:     workspaceDiagnosticDocsCheck,
		Doc:      "every page diagnostics.docs names is a code under the workspace's prefix and names a next step",
		Evidence: check.EvidenceMeasured,
		run: func(_ context.Context, r *runner, _ []*types.Project) check.Check {
			return checkWorkspaceDiagnosticDocs(r.root, r.opts.cfg.Diagnostics)
		},
	},
	{
		Name:           "dependency-graph",
		Doc:            "the target dependency graph builds without a cycle",
		Code:           types.TargetDependencyCycle,
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkGraphCycles() },
	},
	{
		Name:           "merge-driver-loads-workspace",
		Doc:            "whether the registered merge driver can read this workspace, or leaves every generated conflict to a hand merge",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkMergeDriverLoads(ctx)
		},
	},
	{
		Name:           "owed-regeneration",
		Doc:            "whether a merge kept one side of a generated file whose regeneration has not run",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkOwedRegeneration(ctx)
		},
	},
	{
		Name:           "guard-binary",
		Doc:            "which magus an agent-host guard hook would execute, and whether it predates the tree",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkGuardBinaryEverywhere(ctx, projects)
		},
	},
	{
		Name:           servedScriptsCheck,
		Doc:            "whether every script the guard serves by situation still exists",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkServedScripts() },
	},
	{
		Name:           "agent-observer",
		Doc:            "whether the wired observer hook is recording anything",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkObserverRecording()
		},
	},
	{
		Name:           "session-load",
		Doc:            "whether a host transcript has ever been loaded, and how stale the newest of it is",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkSessionLoad() },
	},
	{
		Name:           "guard-wiring",
		Doc:            "whether anything in this checkout actually hands the guard a command to judge",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(ctx context.Context, r *runner, _ []*types.Project) check.Check { return r.checkGuardWiring(ctx) },
	},
	{
		Name:           hookBindingsCheck,
		Doc:            "whether every wired harness binds each hook point, or says why its host cannot",
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            func(ctx context.Context, r *runner, _ []*types.Project) check.Check { return r.checkHookBindings(ctx) },
	},
	{
		Name:           "checkpoint-wiring",
		Doc:            "whether a wired agent host records where the work stood when a session stopped",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkCheckpointWiring(ctx)
		},
	},
	{
		Name:           "bound-lease",
		Doc:            "whether the lease this checkout is bound to is a live, registered row, and whether a host hook is wired to judge it",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(ctx context.Context, r *runner, _ []*types.Project) check.Check { return r.checkBoundLease(ctx) },
	},
	{
		Name:           "job-tree",
		Doc:            "live jobs a holder took and left quiet past jobs.stale_after, or blocked on a dependency that ended without passing",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkJobTree() },
	},
	{
		Name:     "server-version",
		Doc:      "whether the server answering this workspace is the same build as the binary asking",
		Evidence: check.EvidenceMeasured,
		run:      func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkServerVersion() },
	},
	{
		Name:           "agent-skills",
		Doc:            "installed agent skills still current with this binary",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(ctx context.Context, r *runner, _ []*types.Project) check.Check { return r.checkAgentSkills(ctx) },
	},
	{
		Name:           "release-index",
		Doc:            "the signed release index has not passed its expires_at",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkReleaseIndexExpiry()
		},
	},
	{
		Name:           "symlinks",
		Doc:            "symlinks whose resolved target escapes the workspace root",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkSymlinks() },
	},
	{
		Name:           "graph-bounds",
		Doc:            "the knowledge graph holds no node naming a location outside the workspace",
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run:            func(ctx context.Context, r *runner, _ []*types.Project) check.Check { return r.checkGraphBounds(ctx) },
	},
	{
		Name: "generated-output",
		Doc:  "declared outputs that moved with no declared input dirty to account for them",
		// types.ClassifyDrift picks between this and EnvironmentalDrift per run, and only
		// the binary's own version decides which. The defect one is listed: a dev build
		// disagreeing with the pinned release is the reader's environment, not their tree.
		Code:           types.NondeterministicOutput,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkGeneratedDrift(ctx)
		},
	},
	{
		Name:           "stale-worktrees",
		Doc:            "checkout directories under .claude/worktrees that the VCS no longer tracks",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkStaleWorktrees() },
	},
	{
		Name:           "environment-variables",
		Doc:            "exported MAGUS_* variables magus retired or that misspell one it reads (a failure, MGS1046), and ones it does not read at all (advice)",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkEnvVars() },
	},
	{
		Name:           "target-name-conventions",
		Doc:            "the workspace declares target names in one casing convention rather than several",
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkTargetNameConventions(projects)
		},
	},
	{
		Name:           "bespoke-phase-fragment-targets",
		Doc:            "targets named after a static-analysis or formatting subset rather than a phase",
		Code:           types.BespokePhaseFragmentName,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkBespokePhaseFragmentTargets(projects)
		},
	},
	{
		Name:           "unreached-footprint-declarations",
		Doc:            "a ctx.readsFiles/writesFiles call the extractor cannot reach, so it never keys the cache",
		Code:           types.UnreachedFootprintDecl,
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkUnreachedFootprintDecls(projects)
		},
	},
	{
		Name:           "cacheable-secret-reads",
		Doc:            "a cacheable target reading a secret, which rotating then invalidates nothing",
		Code:           types.CacheableSecretRead,
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkCacheableSecretReads(projects)
		},
	},
	{
		Name:           "cacheable-external-ops",
		Doc:            "a cacheable target composing an op that reads a live feed or has an effect outside the tree",
		Code:           types.CacheableExternalOp,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkCacheableExternalOps(projects)
		},
	},
	{
		Name:           "manifest-scripts",
		Doc:            "a target running a script a manifest defines, whose steps, inputs and outputs the cache key cannot see",
		Code:           types.ManifestScriptDelegation,
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkManifestScripts(ctx, projects)
		},
	},
	{
		Name:           "observation-keyed-as-version",
		Doc:            "a tool whose observation probe is also its version probe, so the feed keys every target",
		Code:           types.ObservationKeyedAsVersion,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkObservationKeyedAsVersion(projects)
		},
	},
	{
		Name:           "redundant-footprint-globs",
		Doc:            "a per-target output glob already declared project-wide",
		Code:           types.RedundantFootprintGlob,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkRedundantFootprintGlobs(projects)
		},
	},
	{
		Name:           "dead-output-globs",
		Doc:            "an output glob matching nothing while its siblings in the same project match files",
		Code:           types.DeadOutputGlob,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkDeadOutputGlobs(ctx, projects)
		},
	},
	{
		Name:           "undeclared-seeding-files",
		Doc:            "committed files no project declares that still pull one into the affected set",
		Code:           types.UndeclaredSeedingFile,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkUndeclaredSeedingFiles(ctx, projects)
		},
	},
	{
		Name:           "output-is-another-projects-source",
		Doc:            "a file one project generates that another project declares as a source",
		Code:           types.OutputIsAnotherProjectsSource,
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkOutputIsAnotherProjectsSource(projects)
		},
	},
	{
		Name:           "unmatchable-source-globs",
		Doc:            "a source glob rooted inside a tree the expansion walk prunes, so it matches nothing",
		Code:           types.UnmatchableSourceGlob,
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkUnmatchableSourceGlobs(projects)
		},
	},
	{
		Name:           "recurring-guard-denials",
		Doc:            "a guard rule an agent hit repeatedly across sessions",
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkRecurringGuardDenials()
		},
	},
	{
		Name:           "writes-without-rw-charm",
		Doc:            "a target with an rw branch that writes the tree outside it",
		Code:           types.WriteWithoutRWCharm,
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkWriteWithoutRWCharm(projects)
		},
	},
	{
		Name:           "source-is-also-output",
		Doc:            "one target declaring the same path as both a source and an output",
		Code:           types.SourceIsAlsoOutput,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkSourceIsAlsoOutput(projects)
		},
	},
	{
		Name:           "footprint-drops-op-globs",
		Doc:            "a target that narrows its footprint and then runs an op over files it no longer names",
		Code:           types.FootprintDropsOpGlobs,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkFootprintDropsOpGlobs(projects)
		},
	},
	{
		Name:           "output-ownership",
		Doc:            "one output glob declared by two targets in the same project",
		Code:           types.OutputOwnedByTwoTargets,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkOutputOwnedByTwoTargets(projects)
		},
	},
	{
		Name:           "same-step-writes",
		Doc:            "a composed target running a reader and a writer of the same files with no ctx.needs between them",
		Code:           types.UnorderedSameStepWrite,
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkSameStepWrites(projects)
		},
	},
	{
		Name:           "self-staling-outputs",
		Doc:            "a committed generated file containing this repository's own HEAD commit",
		Code:           types.SelfStalingOutput,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkSelfStalingOutputs(ctx, projects)
		},
	},
	{
		Name:           "charm-target-collisions",
		Doc:            "a charm name that also names a target, making invocations ambiguous to read",
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkCharmTargetCollision(projects)
		},
	},
	{
		Name:           "has-charm-typos",
		Doc:            "a has_charm read whose name is a near-miss of a real charm, so its branch is dead",
		Evidence:       check.EvidenceInferred,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkHasCharmTypos(projects)
		},
	},
	{
		Name: "tool-readiness",
		// The one check whose evidence a FLAG changes: without --probe it repeats what
		// the spells declare, and with it magus actually runs each gate. The check
		// raises its own evidence to measured in that case.
		Doc:            "which tools this workspace's spells gate on, and what each gate asks",
		Evidence:       check.EvidenceDeclared,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkReadinessProbes(ctx, projects)
		},
	},
	{
		Name: "toolchain-lifecycle",
		// Measured by the provider, not by doctor: it reads the answer `magus describe
		// tools` stored and never fetches, so a workspace with no stored answer reports
		// unknown rather than fine.
		Doc:            "an installed tool, or a workspace window floor, in a release cycle past its end of life",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, projects []*types.Project) check.Check {
			return r.checkToolchainLifecycle(ctx, projects)
		},
	},
	{
		Name:     "disk-trend",
		Doc:      "the free space the reclaim hook last sampled, how fast it fell over the last day, and its last reclaim chunks, against the disk.* thresholds",
		Evidence: check.EvidenceMeasured,
		run:      func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkDiskTrend() },
	},
	{
		Name:           "stale-spell-shadow-acknowledgments",
		Doc:            "a spells.allow_shadow entry whose shadow no longer exists, so the reason is dead config",
		Code:           types.SpellShadowed,
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkStaleShadowAcks() },
	},
	{
		Name:           "spell-overrides",
		Doc:            "a workspace copy of a built-in spell, against the built-in this binary ships and the one the copy was pulled from",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(_ context.Context, r *runner, _ []*types.Project) check.Check { return r.checkSpellOverrides() },
	},
	{
		Name:           "vcs-base-ref",
		Doc:            "the configured VCS base ref resolves, so affected tracking has something to diff",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(ctx context.Context, r *runner, _ []*types.Project) check.Check { return r.checkVCSBaseRef(ctx) },
	},
	{
		Name:           "workspace-registration",
		Doc:            "whether this workspace is loaded in the server, and what else is",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(_ context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkWorkspaceRegistration()
		},
	},
	{
		Name:           "bridge-reachability",
		Doc:            "the console endpoint answers, proving the guarded route exists",
		Evidence:       check.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(ctx context.Context, r *runner, _ []*types.Project) check.Check {
			return r.checkBridgeReachability(ctx)
		},
	},
}

// CheckInfo describes one check without running it: the Name its findings are reported
// under, its subject, and the MGS code it reports under where it has one.
type CheckInfo struct {
	Name string `json:"name" yaml:"name"`
	Doc  string `json:"doc" yaml:"doc"`
	Code string `json:"code,omitempty" yaml:"code,omitempty"`
	// Evidence is what this check's verdict normally rests on. A listing states the
	// usual case; a run can report less (the check could not look) or more (--probe).
	Evidence string `json:"evidence" yaml:"evidence"`
	// NeedsWorkspace is false for the checks that still run when the magusfile does not
	// load, which is the set worth knowing about when that is the problem.
	NeedsWorkspace bool `json:"needs_workspace" yaml:"needs_workspace"`
}

// Checks describes every doctor check without running any of them, in report order.
//
// A function rather than a Run flag because listing is not a cheap run: several checks
// walk the tree, dial a socket, or issue an HTTP GET, and asking what the checks ARE
// should not cost that.
func Checks() []CheckInfo {
	out := make([]CheckInfo, 0, len(allChecks))
	for _, def := range allChecks {
		out = append(out, CheckInfo{
			Name:           def.Name,
			Doc:            def.Doc,
			Code:           string(def.Code),
			Evidence:       string(def.Evidence),
			NeedsWorkspace: def.NeedsWorkspace,
		})
	}
	return out
}
