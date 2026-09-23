package types

import (
	"context"
	"strings"

	"github.com/egladman/magus/libs/diagnostics"
)

// magus's diagnostic codes are the MGS#### family. The MECHANISM (the Code/Error types, the rendering, the
// errors.Is matching, the run-time sink) lives in the shared github.com/egladman/magus/libs/diagnostics framework; this file
// is magus's INSTANTIATION of it: the MGS docs-URL layout, the MGS catalog, and thin re-exports so the
// ~20 in-tree consumers keep using types.DiagnosticCode / DiagnosticError / DiagnosticErrorf unchanged.
// gopherbuzz instantiates the same framework separately for its own BZZ#### codes; the two namespaces
// never share a code.

// Diagnostic codes (MGS####): 1000=magusfile authoring, 2000=sandbox, 3000=workspace-scope, 4000=race detection, 5000=services, 6000=charms, 7000=knowledge-graph extraction, 8000=output references, 9000=auth/connector.

// Base URLs for diagnostic documentation, keyed by code-prefix subdir.
//
// These point at the rendered docs SITE, not at the Markdown source on the repo host.
// A code in a terminal is something a reader clicks while stuck, so it should land on a
// styled page with its navigation, search, and cross-links intact, not on a raw file
// view. It also means the URL survives the source moving, since the site keeps a
// redirect for a page that relocates and the blob URL would simply 404.
//
// No ".md": the site serves each code as a directory URL.
const (
	diagnosticSandboxBase   = "https://eli.gladman.cc/magus/reference/codes/sandbox/"
	diagnosticRaceBase      = "https://eli.gladman.cc/magus/reference/codes/race/"
	diagnosticMagusfileBase = "https://eli.gladman.cc/magus/reference/codes/magusfile/"
	diagnosticServicesBase  = "https://eli.gladman.cc/magus/reference/codes/services/"
	diagnosticCharmsBase    = "https://eli.gladman.cc/magus/reference/codes/charms/"
	diagnosticKnowledgeBase = "https://eli.gladman.cc/magus/reference/codes/knowledge/"
	diagnosticOutputRefBase = "https://eli.gladman.cc/magus/reference/codes/outputref/"
	diagnosticAuthBase      = "https://eli.gladman.cc/magus/reference/codes/auth/"
	// Capability gaps. MGS11## because every leading digit 1-9 is already a family, so a tenth
	// needs two digits, and 11 rather than 10 because MGS1010 is a magusfile code and an
	// "MGS10" prefix would silently steal it.
	diagnosticCapabilityBase = "https://eli.gladman.cc/magus/reference/codes/capability/"
)

// DiagnosticCode identifies a stable diagnostic (MGS#### code). It aliases the framework's Code type, so
// every consumer keeps referring to types.DiagnosticCode while the machinery is shared.
type DiagnosticCode = diagnostics.Code

// DiagnosticError is a typed error carrying an MGS code and message (the framework's Error). It implements
// error, and a DiagnosticCode is itself an errors.Is sentinel, so a caller matches one idiomatically:
// errors.Is(err, types.ExecDenied).
type DiagnosticError = diagnostics.Error

// DiagnosticEvent is one diagnostic fired during a run (the framework's Event).
type DiagnosticEvent = diagnostics.Event

// DiagnosticSink records diagnostics fired during a run (the framework's Sink).
type DiagnosticSink = diagnostics.Sink

// ErrDiag is a sentinel for use with errors.Is on DiagnosticError values.
var ErrDiag = diagnostics.ErrSentinel

// mgs is the magus diagnostic domain: it maps an MGS code to its docs page by prefix range. Every magus
// coded error is minted through it so the docs URL is captured for rendering.
var mgs = diagnostics.New(func(c DiagnosticCode) string {
	switch {
	// BEFORE the single-digit cases: MGS11## starts with "MGS1" too, so the magusfile family
	// would otherwise swallow it. TestEveryDiagnosticCodeHasDocPage is what keeps this honest:
	// it resolves every code's URL back to a file on disk, so a mis-routed code has no page and
	// fails rather than shipping a dead link.
	case strings.HasPrefix(string(c), "MGS11"):
		return diagnosticCapabilityBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS9"):
		return diagnosticAuthBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS8"):
		return diagnosticOutputRefBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS7"):
		return diagnosticKnowledgeBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS6"):
		return diagnosticCharmsBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS5"):
		return diagnosticServicesBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS4"):
		return diagnosticRaceBase + string(c) + "/"
	case strings.HasPrefix(string(c), "MGS1"):
		return diagnosticMagusfileBase + string(c) + "/"
	default:
		return diagnosticSandboxBase + string(c) + "/"
	}
})

// CodeURL returns the documentation URL for an MGS code. (URL resolution is domain-specific, so it is a
// function on the magus domain rather than a method on the shared Code type.)
func CodeURL(c DiagnosticCode) string { return mgs.URL(c) }

const (
	NoCITarget               DiagnosticCode = "MGS1001"
	SpellShadowed            DiagnosticCode = "MGS1002"
	BespokePhaseFragmentName DiagnosticCode = "MGS1003"
	UnreachedFootprintDecl   DiagnosticCode = "MGS1004"
	RedundantFootprintGlob   DiagnosticCode = "MGS1005"
	UnknownTarget            DiagnosticCode = "MGS1006"
	TargetDependencyCycle    DiagnosticCode = "MGS1007"
	TargetMissingContext     DiagnosticCode = "MGS1008"
	TargetNeverReplays       DiagnosticCode = "MGS1009"
	AffectedSetUncomputable  DiagnosticCode = "MGS1010"
	CrossOutputOwnerUnknown  DiagnosticCode = "MGS1011"
	CrossOutputCycle         DiagnosticCode = "MGS1012"
	CrossOutputGlobEscapes   DiagnosticCode = "MGS1013"
	CrossOutputNotProduced   DiagnosticCode = "MGS1014"
	CrossDepOwnerUnknown     DiagnosticCode = "MGS1015"
	GoModReplaceDrift        DiagnosticCode = "MGS1016"
	MagusfileIsNotASpell     DiagnosticCode = "MGS1017"
	DeadOutputGlob           DiagnosticCode = "MGS1018"
	SelfStalingOutput        DiagnosticCode = "MGS1019"
	OutputOwnedByTwoTargets  DiagnosticCode = "MGS1020"
	WorkspaceNeedsNewerMagus DiagnosticCode = "MGS1021"
	MagusfileOnlyMember      DiagnosticCode = "MGS1022"
	ProviderPathRejected     DiagnosticCode = "MGS1023"
	ProviderProjectShadowed  DiagnosticCode = "MGS1024"
	MagusfileAPIRemoved      DiagnosticCode = "MGS1025"
	CacheableSecretRead      DiagnosticCode = "MGS1026"
	// SecretGrantInvalid covers every way a secret grant is unusable: a missing field, a
	// wildcard or non-ASCII host, a header that is not a legal field name. ONE code
	// rather than one per rule, because the resolution is the same in every case (fix
	// the declaration the error names), and a caller branching on it wants "this grant
	// is malformed", not which clause caught it. The message carries the specifics.
	SecretGrantInvalid DiagnosticCode = "MGS1027"
	// UndeclaredSeedingFile is a changed file no project declares that still pulled a
	// project into the affected set through directory containment. It reruns targets
	// while moving no cache key, so the work is real and its result was already
	// correct: the expensive half of an under-declaration, with the silent half
	// (nothing reruns when the file DOES matter) waiting behind it.
	UndeclaredSeedingFile DiagnosticCode = "MGS1028"
	// UnmatchableSourceGlob is a source or read declaration whose static directory
	// prefix lands inside a pruned tree (project.IgnoreDirs: gen, vendor, node_modules,
	// target). The expansion walk skips those directories wholesale, so the pattern
	// matches nothing and silently contributes no cache key: the target replays while
	// the files it named change underneath it.
	//
	// The mirror of MGS1014, which catches a declared OUTPUT that no run produces. Both
	// are a declaration disconnected from reality, and both are invisible without a
	// check: `describe target` lists the glob under sources either way.
	//
	// An EXACT path is not reported: a wildcard-free declaration names one file, so it
	// is resolved by stat rather than the walk and reaches the key normally. Only a
	// pattern is unmatchable, and only because letting one reach into a pruned tree is
	// what pruning exists to prevent (a bare **/*.js would hash all of node_modules).
	UnmatchableSourceGlob DiagnosticCode = "MGS1029"
	// OutputIsAnotherProjectsSource is a file one project declares as an OUTPUT that
	// another project's source glob also claims. Nothing is wrong until the file's
	// content changes: then the generating project rewrites it, the claiming project
	// sees a declared source move underneath a target it is running, and reports the
	// write as an undeclared mutation (MGS4007) against a file that is generated by
	// definition.
	//
	// Static, from the declarations alone: no run required, and it holds whether or not
	// the content happens to be drifting today. That is the point: this repository
	// carried the conflict on every project's MAGUS.md for as long as the markdown
	// spell has claimed **/*.md, and only the one whose bytes went stale ever surfaced.
	//
	// EXACT output paths only, against the other project's globs. The same decidability
	// line MGS4002 draws: glob-vs-glob overlap is not decidable in general, but asking
	// whether a pattern matches one literal path is.
	OutputIsAnotherProjectsSource DiagnosticCode = "MGS1031"
	// MemoryDeclarationDrift is a target whose memory_mb disagrees with the peak
	// resident memory magus has actually measured for it, in either direction, or
	// that declares nothing while measurably taking a material share of the machine.
	//
	// This is what keeps memory_mb honest. A declared figure is only as good as its
	// declarations, and declarations rot silently: a target written at 2GB grows to
	// 9GB over a year and the gate quietly stops protecting anything, while a target
	// declared far above what it uses refuses peers that would have fit. magus already
	// records a peak per target, so the disagreement is a fact it holds rather than a
	// question for the author.
	//
	// Advice, never a failure. The measurement is a maximum over recent runs on
	// whatever machines happened to run them, and the author is entitled to declare a
	// figure that differs deliberately: a ceiling for a target whose peak varies with
	// its input, say. magus reports what it measured; the number in the magusfile
	// stays a human's to write.
	MemoryDeclarationDrift DiagnosticCode = "MGS1030"
	// TimeoutDeclarationDrift is a target whose declared timeout no longer describes
	// how long it takes: recorded runs reach it (so the ceiling is about to fail a
	// legitimate build) or fall so far below it that it would not end a hang inside
	// any useful window.
	//
	// The sibling of MGS1030, and honest for the same reason: magus already records a
	// duration per target, so the disagreement is a fact rather than a question. Advice
	// rather than a failure: a guard is deliberately a multiple of the worst run, and
	// how large a multiple is the author's call.
	//
	// There is no undeclared arm, which is where it parts from MGS1030. An undeclared
	// memory figure is measurably harmful (machine-wide admission is blind to the
	// target), while an undeclared ceiling is the documented default and harms only the
	// runaway case, and a target that never terminates records no duration to argue
	// from, so the evidence for that finding does not exist.
	TimeoutDeclarationDrift DiagnosticCode = "MGS1032"
	// CacheableExternalOp is a cacheable target composing a spell op that declares a
	// relation to the world outside the tree (spells.External) the cache key cannot
	// see: a scanner reading a vulnerability feed, or a push, signature or deploy.
	//
	// The sibling of MGS1026, and for the same reason: what makes it worth a check is
	// that the failure is GREEN. A replayed scan reports the CVEs of whenever it last
	// ran, and a replayed push reports a delivery that never happened. Neither surfaces
	// where it was caused.
	//
	// A reads-external op has two answers, not one: declare skip_cache, or let the
	// spell probe the external data's identity (Tool.observe) so it keys like any other
	// input. A mutates-external op has only the first: a side effect cannot be hashed.
	CacheableExternalOp DiagnosticCode = "MGS1033"
	// ObservationKeyedAsVersion is a spell declaring one command as BOTH a tool's version
	// probe and its observation probe, with no VersionKey narrowing the version half.
	//
	// The two probes have deliberately different reach: an observation is scoped to the
	// targets whose ops drive the binary, while a version probe keys EVERY target in
	// every project that binds the spell. Declaring one command as both, and narrowing
	// neither, routes the observation-class half of its output through the unscoped
	// channel. Whatever moves on the feed's clock then invalidates targets that never
	// run the tool.
	//
	// MEASURED once, which is why it is a code rather than a comment: the go spell
	// declared `govulncheck -version` as both, and that output ends with the
	// vulnerability database's publication date, so every database release invalidated
	// every build, test and lint entry in every Go project.
	//
	// Two answers. Drop the version probe and keep the observation, which is right when
	// the command cannot report the tool's own version separately from the feed's. Or
	// declare a VersionKey that extracts the version alone, which keeps a genuine tool
	// upgrade keying everything while the feed keys only its drivers.
	ObservationKeyedAsVersion DiagnosticCode = "MGS1037"
	// RemovedOption is a magus.project key that an older magus accepted and this one
	// removed. It is fatal where an unrecognized key is only ignored: an unrecognized key
	// may come from a NEWER magus, so ignoring it with upgrade advice is right, but a
	// removed key comes from an older one, and that advice sends someone already on the
	// newest binary in a circle while the value they declared goes unhonored.
	RemovedOption DiagnosticCode = "MGS1038"
	// MagusNotImported is a magusfile, spell or script calling magus\ without importing
	// it. magus was bound into every program implicitly until v0.5.0 made it an ordinary
	// host module, so a file written for an older magus fails with a bare
	// `undefined: magus` that says nothing about the one-line fix.
	MagusNotImported DiagnosticCode = "MGS1039"
	// UnknownConfigKey is a magus.yaml key this magus does not recognize. Until v0.5.0 the
	// load only warned and carried on, so a file that loaded then can stop the load now,
	// and the code is what links that failure to the page saying why it changed.
	UnknownConfigKey DiagnosticCode = "MGS1040"
	// RemoteSpellUndeclared is an import of a registry path that magus.yaml does not
	// declare. The declaration names the tag the lock pins, so without one there is no
	// digest to verify and nothing to load.
	RemoteSpellUndeclared DiagnosticCode = "MGS1041"
	// RemoteSpellDigestMismatch is a pinned spell whose bytes do not hash to the pin:
	// served that way by a registry, or found that way in the cache while MAGUS_OFFLINE
	// forbids a fresh pull. Nothing loads.
	RemoteSpellDigestMismatch DiagnosticCode = "MGS1042"
	// RemoteSpellLockStale is a declared remote spell magus.lock does not pin, or pins for
	// a different tag than magus.yaml now tracks. Only the update charm resolves a tag, so
	// an ordinary run refuses rather than resolving it itself.
	RemoteSpellLockStale DiagnosticCode = "MGS1043"
	// SpellOverrideInvalid is a magus.yaml spell override that replaces nothing usable: its
	// path holds no spell, the spell there has another name than the embedded one it
	// replaces, or it names an embedded spell this magus does not ship.
	SpellOverrideInvalid DiagnosticCode = "MGS1044"
	// GuardRuleMisdeclared is a magus\guard.spawn, command or write registration the
	// workspace cannot use: one that is not a function, a second one in the same load, or
	// one outside the root magusfile. The load stops, because a rule that silently did not
	// register is a guard that looks enforced and is not.
	GuardRuleMisdeclared DiagnosticCode = "MGS1045"
	// SourceIsAlsoOutput is one target naming a path in both ctx.readsFiles and
	// ctx.writesFiles. The cache restores an output before the target runs, so the bytes
	// keying the target are the bytes the cache wrote: an edit to that file can neither
	// miss the cache nor be read by the target that declared it.
	SourceIsAlsoOutput DiagnosticCode = "MGS1034"
	// WriteWithoutRWCharm is a target with an rw branch that writes a file outside it, so
	// the run that was given no rw charm edits the tree anyway and then reports its own edit.
	WriteWithoutRWCharm DiagnosticCode = "MGS1035"
	// FootprintDropsOpGlobs is a target that declares its own footprint and then composes a
	// spell op reading file kinds that footprint never names.
	//
	// A ctx.readsFiles call REPLACES the project baseline (buildStep), and a spell
	// contributes its globs project-wide unless it declares them per target, so narrowing a
	// target's footprint silently drops the very files the ops in its body run on. The
	// target then replays on an edit to them.
	//
	// The failure is GREEN, which is what earns it a code over a comment: the op is skipped,
	// not failed, and a sibling target that kept the baseline still re-runs, so the gate
	// stays green while the formatter or the suite never saw the change. Measured in this
	// workspace four times before anyone wrote the rule down.
	//
	// It fires only on total omission. A footprint naming one *.go path is a narrowing its
	// author meant; a footprint naming no Go file at all under a target that calls go-fmt is
	// the mistake, and the two are distinguishable without knowing what the op reads.
	FootprintDropsOpGlobs     DiagnosticCode = "MGS1036"
	PathReadDenied            DiagnosticCode = "MGS2001"
	PathWriteDenied           DiagnosticCode = "MGS2002"
	EnvStripped               DiagnosticCode = "MGS2003"
	AllowlistUnresolved       DiagnosticCode = "MGS2004"
	SandboxUnsupported        DiagnosticCode = "MGS2005"
	PathShimSuspected         DiagnosticCode = "MGS2006"
	ExecDenied                DiagnosticCode = "MGS2007"
	DaemonSocketWithheld      DiagnosticCode = "MGS2008"
	SandboxPolicyMismatch     DiagnosticCode = "MGS2010"
	SecretTooShortToMask      DiagnosticCode = "MGS2011"
	DescendantBoundaryCrossed DiagnosticCode = "MGS3001"
	VCSUnavailable            DiagnosticCode = "MGS3002"
	ToolNotOnPath             DiagnosticCode = "MGS3003"
	// ToolNotReady is ToolNotOnPath one level deeper: the binary IS present, but the
	// service it talks to is not reachable. Same category (the environment, not the
	// code), so it sits beside it rather than in a family of its own.
	ToolNotReady DiagnosticCode = "MGS3004"
	// ToolTooOld is the fourth question about a tool: it exists, it reports a version,
	// it is usable, and that version is below the declared minimum.
	ToolTooOld DiagnosticCode = "MGS3005"
	// ToolTooNew is ToolTooOld's other side: the version is at or above a ceiling the
	// spell or the workspace excludes. A separate code rather than a shared "version
	// rejected" because the remediation is the opposite one, and because folding both
	// into MGS3005 is exactly the defect this pair replaced: a too-new binary being
	// told it was too old.
	ToolTooNew DiagnosticCode = "MGS3006"
	// ProjectLockHeldByAncestor is a magus run that cannot proceed because a project it
	// must lock is already locked by one of its OWN ancestor invocations, which cannot
	// release it until this run exits. It sits in the environment family beside the tool
	// codes: nothing in the workspace is wrong, the process context the run was started in
	// makes it impossible. Named for the condition it detects, not for a "re-entrant lock"
	// magus does not offer.
	ProjectLockHeldByAncestor DiagnosticCode = "MGS3007"
	// NoWorkspaceRoot is the most common first-run failure: no ancestor directory
	// declares a workspace (magus.yaml) or a contiguous run of projects (magusfile.buzz,
	// magusfiles/, go.mod) reaching one. `magus init` is the fix in both cases, so the
	// message names it directly rather than leaving the reader to find the command.
	NoWorkspaceRoot DiagnosticCode = "MGS3008"
	// MachineBudgetExhausted is a step magus did not start because the concurrency and
	// declared memory it needs do not fit alongside what every other magus on this
	// machine holds. It joins MGS3007 in the environment family for the same reason: the
	// workspace is correct and the code is fine, the machine cannot seat the work.
	//
	// magus never queues behind a peer, so a full budget refuses immediately (exit 75,
	// EX_TEMPFAIL: the same command succeeds once the holder finishes). A declaration
	// that does not fit in the whole budget refuses too, but permanently (exit 78,
	// EX_CONFIG), since an idle machine would refuse it just the same.
	MachineBudgetExhausted DiagnosticCode = "MGS3009"
	// RedundantGateDeferred is a ci gate magus did not start because an
	// identical-or-equivalent gate already passed for this branch on this
	// machine. It joins MGS3007/MGS3009 in the environment family: the workspace
	// is fine, what already happened on this machine is what changes the answer.
	//
	// Machine load used to be required too, and that made this unreachable where
	// it mattered: the load reading comes from the daemon, ordinary commands run
	// without a persistent one, so an idle machine always advised and ran the
	// duplicate anyway. Redundancy alone defers now; a nested run still only
	// advises, because it counts its own ancestors' claims as load.
	//
	// Exits 75 (EX_TEMPFAIL) like MGS3009: the same invocation is valid and runs
	// once the branch has a real delta, or immediately with the override flag.
	RedundantGateDeferred DiagnosticCode = "MGS3010"
	// TargetCeilingExceeded is a target magus cancelled because it outran the timeout
	// its magusfile declared. It joins MGS3007/MGS3009/MGS3010 in the environment
	// family: nothing in the workspace is wrong to read, the run took longer than the
	// author said it may.
	//
	// A FAILURE, never a skip and never a pass. A ceiling that lets the invocation
	// continue is a log line, and the incident this exists for was an invocation that
	// held three project locks for over an hour with nothing running: the shape a
	// build tool must not report as success.
	TargetCeilingExceeded DiagnosticCode = "MGS3011"
	// InvocationStalled is a run magus aborted because nothing moved: no target started,
	// finished, or wrote a line of output for the stall window, while every selected
	// project's lock stayed held. It joins MGS3007/MGS3009/MGS3010 in the environment
	// family: the workspace is fine, the process is what stopped making progress.
	//
	// The other half of MGS3011, not a duplicate of it. A ceiling bounds a target that
	// runs LONG, and only one whose author declared a bound; this bounds an invocation
	// making no progress AT ALL, in any of its work. The shape it exists for is a
	// post-batch pass wedged on a network read while every observer reported an idle
	// machine, which no target's ceiling covers because no target was running.
	InvocationStalled DiagnosticCode = "MGS3012"
	// BuildSlotsDeadlocked is a run magus refused because every one of its concurrency
	// slots is held by a step that is itself waiting, so no slot can free and the steps
	// queued for one would wait forever. It joins MGS3007/MGS3009/MGS3010 in the
	// environment family: nothing about the workspace is wrong, the process has arranged
	// itself into a wait nothing in it can end.
	//
	// The half of MGS3012 that can be answered rather than merely reported. The watchdog
	// says a run stopped moving, after the window it takes to be sure; this says why
	// within seconds, and names every holder and what each is blocked on, because the
	// admission path knows both.
	BuildSlotsDeadlocked DiagnosticCode = "MGS3013"
	// GateSuperseded is an earlier gate magus stopped because a LATER gate started on the
	// same tree and asked for the project locks it holds. It joins MGS3007/MGS3010/MGS3012
	// in the environment family: nothing in the workspace is wrong and nothing failed, the
	// tree the verdict was about moved on.
	//
	// The ordering is decided by the tree and never by the caller: one workspace root, both
	// invocations the whole ci target, later start wins. There is no priority to set,
	// because a verdict about a tree that has since changed is worthless whoever asked for
	// it, and the compute spent producing it is waste either way.
	//
	// Exits 75 (EX_TEMPFAIL) like MGS3009/MGS3010: nothing here is broken, and the same
	// command is valid again the moment the later gate finishes.
	GateSuperseded DiagnosticCode = "MGS3014"
	// WorkspaceLoadFailed is a daemon call against a workspace whose magusfiles failed to
	// load. The proximate cause of the refusal; the BZZ or MGS code the load stopped on
	// rides beside it as the underlying one. Retrying cannot help until a source changes.
	WorkspaceLoadFailed DiagnosticCode = "MGS3016"
	// WorkspaceStillLoading is a daemon call against a workspace still being loaded. The
	// transient twin of MGS3016: the same call succeeds once the load finishes.
	WorkspaceStillLoading DiagnosticCode = "MGS3017"
	// QueueCredentialMismatch is a merge queue whose base requires the queue's commit
	// status from one integration while apply holds another's credential. The provider
	// counts none of the statuses the queue posts, so every change would wait forever;
	// apply refuses at its start instead.
	QueueCredentialMismatch DiagnosticCode = "MGS3019"
	// PreflightFailed is a --preflight pass that failed in at least one project, so
	// nothing of the invoked target started. Exits 3, apart from 1, so a CI script can
	// tell "the cheap check failed" from "the fan-out failed".
	PreflightFailed DiagnosticCode = "MGS3020"
	// PreflightOutsideClosure is a --preflight target the invoked target never reaches
	// through ctx.needs in any selected project. Running it first would add work rather
	// than reorder it, so the invocation is refused before anything runs.
	PreflightOutsideClosure   DiagnosticCode = "MGS3021"
	RaceDetected              DiagnosticCode = "MGS4001"
	OutputOverlapDetected     DiagnosticCode = "MGS4002"
	NondeterministicOutput    DiagnosticCode = "MGS4003"
	MissingDependencyDetected DiagnosticCode = "MGS4004"
	EnvironmentalDrift        DiagnosticCode = "MGS4005"
	StaleGeneratedOutput      DiagnosticCode = "MGS4006"
	// UndeclaredSourceModified is a target that rewrote a file it declared as a SOURCE
	// and did not declare as an update. The key that identified the result no longer
	// describes the inputs that produced it, so the entry is unreproducible by
	// construction.
	//
	// Deliberately charm-agnostic. Keying on the charm was considered and rejected: a
	// charm patches args, so it cannot say whether the tool writes, and a rule keyed on
	// whether the caller TYPED the charm exempts a mutating custom charm merely because
	// someone spelled it out. ctx.modifiesExistingFiles is the declaration that answers
	// this, which is why a formatter names its edits rather than earning an exemption.
	UndeclaredSourceModified DiagnosticCode = "MGS4007"
	// UnorderedSameStepWrite is one target reading, inside a single step, what another
	// target of that same step writes, with no ctx.needs path between them. Both sides
	// are explicit declarations, so the overlap is the magusfile's own claim rather than
	// an over-approximated baseline.
	//
	// Refused at PLAN time, before any goroutine launches, because both failures it
	// produces are worse than a refusal: the reader may run first and read stale bytes,
	// or it may wait for the writer while holding the seat the writer needs, wedging the
	// run until the stall watchdog (MGS3012) kills it.
	//
	// The same-step case is the one magus must not schedule around. Across steps the
	// engine derives writer-before-reader ordering itself; within one step the sequencing
	// is the composing body's own, and only ctx.needs can express it.
	UnorderedSameStepWrite DiagnosticCode = "MGS4008"
	// UnformattedCommit is a commit that changed a Go file without leaving it correctly
	// formatted (gofmt -l still names it). A COMMIT-TIME question, not "is this file
	// formatted right now": golangci-lint's formatters already answer that one, against
	// the whole tree, on demand. This fires once per commit, scoped to the files that
	// commit touched, from the drift-notice hooks (post-commit, pre-push); see
	// checkDriftForCommit in cmd/magus. Sibling of MGS4006 (generated-output drift, the
	// other class the same notice carries) and distinct from MGS4007 (a target rewriting
	// an undeclared source, checked after a target runs, not after a commit is made).
	UnformattedCommit     DiagnosticCode = "MGS4009"
	NearDuplicateServices DiagnosticCode = "MGS5001"
	ServiceOpDetached     DiagnosticCode = "MGS5002"
	CommandOpNeverExits   DiagnosticCode = "MGS5003"
	DaemonRequired        DiagnosticCode = "MGS5004"
	CharmPatchInvalid     DiagnosticCode = "MGS6001"
	// CharmRenamed is a run activating a charm under a name magus has retired, with no
	// selected target declaring that name for itself. The old name matches nothing, so
	// without this the run would go ahead without the grant it asked for.
	CharmRenamed             DiagnosticCode = "MGS6002"
	UnresolvableBuzzImport   DiagnosticCode = "MGS7001"
	DanglingDocReference     DiagnosticCode = "MGS7002"
	OutputRefMissing         DiagnosticCode = "MGS8001"
	OutputRefAmbiguous       DiagnosticCode = "MGS8002"
	OutputRefMalformed       DiagnosticCode = "MGS8003"
	OutputRefForeignMachine  DiagnosticCode = "MGS8004"
	BearerRejected           DiagnosticCode = "MGS9001"
	InsecureTokenPermissions DiagnosticCode = "MGS9002"
	TokenStoreTooNew         DiagnosticCode = "MGS9003"
	NoAuthToken              DiagnosticCode = "MGS9004"
	TokenNameExists          DiagnosticCode = "MGS9005"
	TokenNotFound            DiagnosticCode = "MGS9006"
	// HostNotAllowed is a request whose Host or Origin names a host the daemon does not
	// serve: the DNS-rebinding guard, answered 403.
	HostNotAllowed DiagnosticCode = "MGS9007"
	// LoopbackPeerRequired is a request to a local-only route from a peer that is not on
	// this machine's loopback interface, answered 403.
	LoopbackPeerRequired DiagnosticCode = "MGS9008"
	// ShareBoundToAnotherDevice is a valid share token replayed from a device other than
	// the one that first used it, answered 403.
	ShareBoundToAnotherDevice DiagnosticCode = "MGS9009"
	// ConsoleFileWithheld is a console path outside the tokenless app shell, answered 404.
	ConsoleFileWithheld DiagnosticCode = "MGS9010"
	// BearerMissing is a request to a guarded route that carried no bearer token at all,
	// answered 401. A token that was sent and refused is BearerRejected instead: the two
	// need different fixes (attach one, or mint a new one).
	BearerMissing DiagnosticCode = "MGS9011"
	// MethodNotAllowed is a daemon route asked with a method it does not serve, answered 405.
	MethodNotAllowed DiagnosticCode = "MGS9012"
	// ConsoleNotBuilt is a share started while the daemon found no built console to serve.
	ConsoleNotBuilt DiagnosticCode = "MGS9013"
	// ShareUnavailable is a share whose LAN listener could not start: no private-range
	// interface is up, or the listener could not bind.
	ShareUnavailable DiagnosticCode = "MGS9014"
	// GrantInsufficient is a valid credential presented to a route that needs more than its
	// grant holds, answered 403. The body names the need.
	GrantInsufficient DiagnosticCode = "MGS9015"
	// OperatorTokenFormat is an operator token file that does not hold an mgo_ token: it
	// predates the class prefix, or was edited by hand.
	OperatorTokenFormat DiagnosticCode = "MGS9016"
	// TokenStoreTooOld is a stored token written before grants existed, which this magus
	// refuses rather than guess a grant for.
	TokenStoreTooOld DiagnosticCode = "MGS9017"
	// TokenLifetimeOutOfRange is a stored token or a share link asked to live outside its
	// bound, never included: refused rather than shortened.
	TokenLifetimeOutOfRange DiagnosticCode = "MGS9018"

	// VCSCapabilityMissing fires when the configured version-control backend does not implement
	// a lookup a feature needs, so the answer is reported as unavailable rather than as empty.
	//
	// It heads the CAPABILITY family: a magus feature exists, and the backend or provider wired
	// here has not implemented the piece it needs. These are not errors in the ordinary sense and
	// mostly do not fail a command: the reader did nothing wrong, and the surface still works
	// without the missing piece. They exist because the alternative is silence, and silence is
	// indistinguishable from the good news. "No other branch touches these files" and "this
	// backend cannot tell you about other branches" lead a reader to opposite decisions, and only
	// one of them is reassurance.
	//
	// A subset is legitimate by design (the review contract says so outright, and a VCS backend
	// answers what its host can answer), so a gap is a fact to report, never a spell to fix.
	VCSCapabilityMissing DiagnosticCode = "MGS1101"
	// ReviewOpMissing fires when the wired review spell does not export one of the four reserved
	// ops. On a READ that is a silence worth naming; on a WRITE it is an error, because a
	// tolerated no-op would mark every draft published and lose the remarks permanently.
	ReviewOpMissing DiagnosticCode = "MGS1102"
	// ReviewAuthorshipUnknown fires when a provider names neither the review's author nor the
	// credential holder, so magus cannot tell a self-review from a colleague's and will not
	// approve on the reader's behalf. Not knowing is not permission.
	ReviewAuthorshipUnknown DiagnosticCode = "MGS1103"
)

// MGS3015 is retired and deliberately absent above; docs/decisions/0001 says why. The
// number is not reused: a retired code that comes back means two different things in one
// search of a log archive.

// allDiagnosticCodes lists every registered code in ascending MGS order. Keep it
// in sync with the const block above; it is the enumeration source for tooling
// (the knowledge graph turns each into a diagnostic node) since Go const blocks
// are not reflectable.
var allDiagnosticCodes = []DiagnosticCode{
	NoCITarget, SpellShadowed, BespokePhaseFragmentName,
	UnreachedFootprintDecl, RedundantFootprintGlob, UnknownTarget, TargetDependencyCycle,
	TargetMissingContext, TargetNeverReplays, AffectedSetUncomputable,
	CrossOutputOwnerUnknown, CrossOutputCycle, CrossOutputGlobEscapes, CrossOutputNotProduced,
	CrossDepOwnerUnknown, GoModReplaceDrift, MagusfileIsNotASpell, DeadOutputGlob,
	SelfStalingOutput, OutputOwnedByTwoTargets, WorkspaceNeedsNewerMagus,
	MagusfileOnlyMember, ProviderPathRejected, ProviderProjectShadowed,
	MagusfileAPIRemoved, CacheableSecretRead, SecretGrantInvalid, UndeclaredSeedingFile,
	UnmatchableSourceGlob, MemoryDeclarationDrift, OutputIsAnotherProjectsSource,
	TimeoutDeclarationDrift, CacheableExternalOp, SourceIsAlsoOutput, WriteWithoutRWCharm,
	FootprintDropsOpGlobs, ObservationKeyedAsVersion, RemovedOption, MagusNotImported,
	UnknownConfigKey, RemoteSpellUndeclared, RemoteSpellDigestMismatch, RemoteSpellLockStale,
	SpellOverrideInvalid, GuardRuleMisdeclared,
	PathReadDenied, PathWriteDenied, EnvStripped, AllowlistUnresolved,
	SandboxUnsupported, PathShimSuspected, ExecDenied, DaemonSocketWithheld,
	SandboxPolicyMismatch, SecretTooShortToMask,
	DescendantBoundaryCrossed, VCSUnavailable, ToolNotOnPath, ToolNotReady, ToolTooOld, ToolTooNew,
	ProjectLockHeldByAncestor, NoWorkspaceRoot, MachineBudgetExhausted, RedundantGateDeferred,
	TargetCeilingExceeded, InvocationStalled, BuildSlotsDeadlocked, GateSuperseded,
	WorkspaceLoadFailed, WorkspaceStillLoading, QueueCredentialMismatch, PreflightFailed, PreflightOutsideClosure,
	RaceDetected, OutputOverlapDetected, NondeterministicOutput, MissingDependencyDetected,
	EnvironmentalDrift, StaleGeneratedOutput, UndeclaredSourceModified, UnorderedSameStepWrite,
	UnformattedCommit,
	NearDuplicateServices, ServiceOpDetached, CommandOpNeverExits, DaemonRequired,
	CharmPatchInvalid, CharmRenamed,
	UnresolvableBuzzImport, DanglingDocReference,
	OutputRefMissing, OutputRefAmbiguous, OutputRefMalformed, OutputRefForeignMachine,
	BearerRejected, InsecureTokenPermissions, TokenStoreTooNew,
	NoAuthToken, TokenNameExists, TokenNotFound,
	HostNotAllowed, LoopbackPeerRequired, ShareBoundToAnotherDevice, ConsoleFileWithheld,
	BearerMissing, MethodNotAllowed, ConsoleNotBuilt, ShareUnavailable,
	GrantInsufficient, OperatorTokenFormat, TokenStoreTooOld, TokenLifetimeOutOfRange,
	VCSCapabilityMissing, ReviewOpMissing, ReviewAuthorshipUnknown,
}

// AllDiagnosticCodes returns every registered diagnostic code in ascending MGS
// order. The returned slice is a copy; callers may mutate it freely.
func AllDiagnosticCodes() []DiagnosticCode {
	out := make([]DiagnosticCode, len(allDiagnosticCodes))
	copy(out, allDiagnosticCodes)
	return out
}

// DiagnosticErrorf builds a DiagnosticError with an MGS code and formatted message, capturing the code's
// docs URL for rendering.
func DiagnosticErrorf(c DiagnosticCode, format string, args ...any) *DiagnosticError {
	return mgs.Errorf(c, format, args...)
}

// FormatDiagnostic formats a diagnostic message with code and doc URL for slog logging.
func FormatDiagnostic(c DiagnosticCode, msg string) string {
	return mgs.Format(c, msg)
}

// WrapDiagnostic builds a DiagnosticError that carries an MGS code AND wraps cause, so errors.Is(err,
// cause) keeps matching while the error gains a lookupable code. Use it when a sentinel already drives
// control flow (e.g. ErrUnknownTarget) and must keep matching.
func WrapDiagnostic(c DiagnosticCode, cause error, format string, args ...any) *DiagnosticError {
	return mgs.Wrapf(c, cause, format, args...)
}

// WithDiagnosticSink returns ctx carrying s, so a deep emission site can reach the
// sink without threading it through every signature.
func WithDiagnosticSink(ctx context.Context, s DiagnosticSink) context.Context {
	return diagnostics.WithSink(ctx, s)
}

// EmitDiagnostic records ev to the sink in ctx, or is a no-op when none is
// installed (the common CLI path).
func EmitDiagnostic(ctx context.Context, ev DiagnosticEvent) {
	diagnostics.Emit(ctx, ev)
}

// Diagnostic is the shape a coded failure takes when it crosses into Buzz: the fields
// diagnostics.Error.BuzzError already produces, declared as a type rather than left as an
// undeclared map convention.
//
// It exists because `catch` hands back an untyped value (Buzz has no union types, and a
// throw can carry a str, an int, or this), so the caller narrows it at the boundary the
// way errors.As does in Go:
//
//	catch (e) {
//	    final d: Diagnostic = e;
//	    if (d.code == "MGS2001") { ... }
//	}
//
// Url is omitted when the domain captured none, so a caller testing it is asking "did this
// code come with docs", not reading an empty string that might mean either.
type Diagnostic struct {
	Code    string `json:"code" yaml:"code"`
	Message string `json:"message" yaml:"message"`
	// The buzz tag pins the Buzz name explicitly. LowerFirstWord would derive `url` from
	// URL anyway, but the tag is what a reader and a rename both see: the mirror name is
	// part of this type's contract, not a side effect of a casing rule.
	URL string `json:"url,omitempty" yaml:"url,omitempty" buzz:"url"`
}
