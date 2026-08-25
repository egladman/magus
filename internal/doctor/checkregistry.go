package doctor

import (
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
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
	// Evidence is what this check's verdict normally rests on. A run that could not look
	// overrides it with types.EvidenceUnknown, and tool-readiness raises its own to
	// measured under --probe, so this is the default rather than a fixed property.
	Evidence types.Evidence
	// NeedsWorkspace marks a check that reads r.ws, skipped when the workspace fails to
	// load. The ones that run regardless ask about the host - this process, this
	// machine's sockets, this terminal - which is exactly what is still worth answering
	// when the magusfile is unparsable.
	NeedsWorkspace bool
	// run takes the projects slice even where the check ignores it, keeping the table
	// flat rather than a union of shapes.
	run func(r *runner, projects []*types.Project) types.DoctorCheck
}

// allChecks is every doctor check, in report order.
//
// The order is the reader's, not the scheduler's: host checks answer "is this magus
// working" before workspace checks answer "is this workspace right". `workspace` sits at
// the boundary because everything below it depends on that load succeeding.
var allChecks = []checkDef{
	{
		Name: "json-codec",
		Doc:      "which encoding/json implementation this binary was built against",
		Evidence: types.EvidenceMeasured,
		run:  func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkJSONCodec() },
	},
	{
		Name: "sockets",
		Doc:      "live and leftover daemon sockets in the magus socket directory",
		Evidence: types.EvidenceMeasured,
		run:  func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkStaleSockets() },
	},
	{
		Name: "mcp-tokens",
		Doc:      "the daemon's cli token and named connector tokens, and which are expiring",
		Evidence: types.EvidenceMeasured,
		run:  func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkMCPTokens() },
	},
	{
		Name: "terminal-capabilities",
		Doc:      "what the terminal in front of magus can render",
		Evidence: types.EvidenceMeasured,
		run:  func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkTerminal() },
	},
	{
		Name: "workspace",
		Doc:      "the magusfile loads and its projects are discoverable",
		Evidence: types.EvidenceMeasured,
		run:  func(r *runner, projects []*types.Project) types.DoctorCheck { return r.checkWorkspace(projects) },
	},
	{
		Name:           "config-file",
		Doc:            "magus.yaml parses and declares no key magus does not know",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkConfigFile() },
	},
	{
		Name:           "cache-writable",
		Doc:            "the cache directory exists and this user can write it",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkCacheWritable() },
	},
	{
		Name:           "concurrency-sizing",
		Doc:            "the configured concurrency fits this machine",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkConcurrencySizing() },
	},
	{
		Name:           "memory-declarations",
		Doc:            "a target's declared memory_mb against the peak magus has measured for it",
		Code:           types.MemoryDeclarationDrift,
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            (*runner).checkMemoryDeclarations,
	},
	{
		Name:           "cache-yield",
		Doc:            "targets running uncached without declaring skip_cache and a reason",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            (*runner).checkCacheYield,
	},
	{
		Name:           "language-coverage",
		Doc:            "projects binding no toolchain spell, so their work is invisible to affected tracking",
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkLanguageCoverage,
	},
	{
		Name:           "ci-target",
		Doc:            "some project declares the ci target that `magus affected ci` keys off",
		Code:           types.NoCITarget,
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkCITarget,
	},
	{
		Name:           "required-version-covers-schema",
		Doc:            "every magus.project key the workspace uses is covered by its declared required_version",
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkSchemaFloor,
	},
	{
		Name:           "service-duplication",
		Doc:            "service targets across projects that look like copies of one service",
		Code:           types.NearDuplicateServices,
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkNearDuplicateServices,
	},
	{
		Name:           "service-suppressions",
		Doc:            "services marked distinct whose near-duplicate no longer exists, so the reason is dead",
		Code:           types.NearDuplicateServices,
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkStaleServiceSuppressions,
	},
	{
		Name:           "magusfile-syntax",
		Doc:            "every magusfile parses, with strict-parity errors reported at once",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            (*runner).checkMagusfileSyntax,
	},
	{
		Name:           "spell-target-docs",
		Doc:            "every function-handler target of a workspace-local spell carries a doc comment",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run: func(r *runner, _ []*types.Project) types.DoctorCheck {
			return r.checkSpellDocs(project.DefaultSpellRegistry().All())
		},
	},
	{
		Name:           "spell-contract",
		Doc:            "what each registered spell implements of the mgs_ contract",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkSpellContract() },
	},
	{
		Name:           "diagnostic-docs",
		Doc:            "every MGS code magus prints routes to a docs page that names a next step",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkDiagnosticDocs() },
	},
	{
		Name:           "dependency-graph",
		Doc:            "the target dependency graph builds without a cycle",
		Code:           types.TargetDependencyCycle,
		Evidence:       types.EvidenceInferred,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkGraphCycles() },
	},
	{
		Name:           "guard-binary",
		Doc:            "which magus an agent-host guard hook would execute, and whether it predates the tree",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkGuardBinary() },
	},
	{
		Name:           "agent-observer",
		Doc:            "whether the wired observer hook is recording anything",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkObserverRecording() },
	},
	{
		Name:           "guard-wiring",
		Doc:            "whether anything in this checkout actually hands the guard a command to judge",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkGuardWiring() },
	},
	{
		Name:           "agent-skills",
		Doc:            "installed agent skills still current with this binary",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkAgentSkills() },
	},
	{
		Name:           "release-index",
		Doc:            "the signed release index has not passed its expires_at",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkReleaseIndexExpiry() },
	},
	{
		Name:           "registry-freshness",
		Doc:            "how long ago the tool registry was synced",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkRegistryFreshness() },
	},
	{
		Name:           "symlinks",
		Doc:            "symlinks whose resolved target escapes the workspace root",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkSymlinks() },
	},
	{
		Name:           "graph-bounds",
		Doc:            "the committed knowledge graph holds no node naming a location outside the workspace",
		Evidence:       types.EvidenceInferred,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkGraphBounds() },
	},
	{
		Name:           "generated-output",
		Doc:            "declared outputs that moved with no declared input dirty to account for them",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkGeneratedDrift() },
	},
	{
		Name:           "stale-worktrees",
		Doc:            "checkout directories under .claude/worktrees that the VCS no longer tracks",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkStaleWorktrees() },
	},
	{
		Name:           "environment-variables",
		Doc:            "exported MAGUS_* variables magus does not recognize, which are usually typos",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkEnvVars() },
	},
	{
		Name:           "target-name-conventions",
		Doc:            "the workspace declares target names in one casing convention rather than several",
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkTargetNameConventions,
	},
	{
		Name:           "bespoke-phase-fragment-targets",
		Doc:            "targets named after a static-analysis or formatting subset rather than a phase",
		Code:           types.BespokePhaseFragmentName,
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkBespokePhaseFragmentTargets,
	},
	{
		Name:           "unreached-footprint-declarations",
		Doc:            "a ctx.readsFiles/writesFiles call the extractor cannot reach, so it never keys the cache",
		Code:           types.UnreachedFootprintDecl,
		Evidence:       types.EvidenceInferred,
		NeedsWorkspace: true,
		run:            (*runner).checkUnreachedFootprintDecls,
	},
	{
		Name:           "cacheable-secret-reads",
		Doc:            "a cacheable target reading a secret, which rotating then invalidates nothing",
		Code:           types.CacheableSecretRead,
		Evidence:       types.EvidenceInferred,
		NeedsWorkspace: true,
		run:            (*runner).checkCacheableSecretReads,
	},
	{
		Name:           "redundant-footprint-globs",
		Doc:            "a per-target output glob already declared project-wide",
		Code:           types.RedundantFootprintGlob,
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkRedundantFootprintGlobs,
	},
	{
		Name:           "dead-output-globs",
		Doc:            "an output glob matching nothing while its siblings in the same project match files",
		Code:           types.DeadOutputGlob,
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            (*runner).checkDeadOutputGlobs,
	},
	{
		Name:           "undeclared-seeding-files",
		Doc:            "committed files no project declares that still pull one into the affected set",
		Code:           types.UndeclaredSeedingFile,
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            (*runner).checkUndeclaredSeedingFiles,
	},
	{
		Name:           "unmatchable-source-globs",
		Doc:            "a source glob rooted inside a tree the expansion walk prunes, so it matches nothing",
		Code:           types.UnmatchableSourceGlob,
		Evidence:       types.EvidenceInferred,
		NeedsWorkspace: true,
		run:            (*runner).checkUnmatchableSourceGlobs,
	},
	{
		Name:           "output-ownership",
		Doc:            "one output glob declared by two targets in the same project",
		Code:           types.OutputOwnedByTwoTargets,
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkOutputOwnedByTwoTargets,
	},
	{
		Name:           "self-staling-outputs",
		Doc:            "a committed generated file containing this repository's own HEAD commit",
		Code:           types.SelfStalingOutput,
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            (*runner).checkSelfStalingOutputs,
	},
	{
		Name:           "charm-target-collisions",
		Doc:            "a charm name that also names a target, making invocations ambiguous to read",
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkCharmTargetCollision,
	},
	{
		Name:           "has-charm-typos",
		Doc:            "a has_charm read whose name is a near-miss of a real charm, so its branch is dead",
		Evidence:       types.EvidenceInferred,
		NeedsWorkspace: true,
		run:            (*runner).checkHasCharmTypos,
	},
	{
		Name:           "tool-readiness",
		// The one check whose evidence a FLAG changes: without --probe it repeats what
		// the spells declare, and with it magus actually runs each gate. The check
		// raises its own evidence to measured in that case.
		Doc:            "which tools this workspace's spells gate on, and what each gate asks",
		Evidence:       types.EvidenceDeclared,
		NeedsWorkspace: true,
		run:            (*runner).checkReadinessProbes,
	},
	{
		Name:           "stale-spell-shadow-acknowledgments",
		Doc:            "a spells.allow_shadow entry whose shadow no longer exists, so the reason is dead config",
		Code:           types.SpellShadowed,
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkStaleShadowAcks() },
	},
	{
		Name:           "vcs-base-ref",
		Doc:            "the configured VCS base ref resolves, so affected tracking has something to diff",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkVCSBaseRef() },
	},
	{
		Name:           "workspace-registration",
		Doc:            "whether this workspace is loaded in the daemon, and what else is",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkWorkspaceRegistration() },
	},
	{
		Name:           "bridge-reachability",
		Doc:            "the console endpoint answers, proving the guarded route exists",
		Evidence:       types.EvidenceMeasured,
		NeedsWorkspace: true,
		run:            func(r *runner, _ []*types.Project) types.DoctorCheck { return r.checkBridgeReachability() },
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
