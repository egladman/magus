package agent

// The guard's wire contract: the vocabulary every host glue must handle, in
// the one package that both the CLI producing a verdict and the repo-root
// dogfood tests can import.
//
// It lives here rather than beside the guard in cmd/magus for a mechanical
// reason: package main cannot be imported, so a parity check living outside it
// would have to RESTATE the contract, and a restated contract is exactly the
// copy that goes stale. Keeping the lists here makes "every host handles every
// decision on every surface" a comparison against the source of truth instead
// of against a second opinion.

// GuardSchemaVersion is the version of the verdict envelope every host glue
// parses, carried on the wire as schema_version. Bump it only when an existing
// field changes MEANING: adding an optional field that existing glues ignore is
// not a bump, and neither is adding a rule. A bump is expensive: a glue that
// meets a schema it does not recognize fails open, so every one of them must be
// re-downloaded before it guards again.
const GuardSchemaVersion = 1

// guardDecisions is every decision a verdict can carry. A new entry here is a
// promise that all four host glues can express it; the dogfood parity test is
// what collects on that promise.
var guardDecisions = []string{"pass", "advise", "deny"}

// guardSurfaces is every input the guard judges: a shell command, or a file
// path an edit is about to write (`magus session hook --path`). A host wires each
// surface to a different one of its events, and a host that cannot wire one
// covers less - which is a coverage difference to record, not to hide.
var guardSurfaces = []string{"command", "path"}

// GuardTemplateVersion is the revision of the hook templates a reader installs
// into their agent host.
//
// The templates are the one shipped artifact with no self-correcting path. An
// installed skill is generated, stamped and regraded on every `magus doctor`; a
// hook template is COPIED into a host's config and then owned by its reader, so
// a fix magus makes to the source never reaches the copy, and nothing about the
// copy says how old it is. That is not hypothetical: a change to the guard's
// exit code turned every unfixed copy into one that judges a denied command
// twice, and on one host into one that fails open on every block. The docs were
// correct within the hour; every installed copy stayed wrong indefinitely.
//
// A version rather than a content digest, because these files are explicitly
// the reader's to edit ("adjust to taste"). A digest would flag every legitimate
// customization and be switched off within a week; a marker survives editing and
// still answers the only question worth asking: is this copy older than the fix?
//
// Bump it whenever a template's BEHAVIOR changes - not for a comment or a
// rewording. TestShippedTemplatesCarryTheCurrentVersion makes the bump total:
// every template must be re-stamped or the build fails.
//
// 2: docs/guides/integrations/agents/opencode-plugin.ts unconditionally passed
// the attribution flag, which no released binary accepts (v0.3.0 predates it) -
// an older binary rejected it, the plugin's judge() got unparsable stdout, and
// every verdict silently allowed. The sh templates already retried without
// attribution on exactly this failure (magus-guard-command.sh's guard()); the
// plugin now does the same.
//
// 3: that flag is now --agent-name (was --host, which read as a network host)
// and the templates' variable is GUARD_AGENT_NAME (was GUARD_HOST). A copy
// still passing the old spelling degrades rather than breaks - the retry that
// version 2 added drops attribution and keeps the verdict - so an unbumped
// copy loses the activity trail's host label, not its guard.
//
// 4: two changes, neither released before this. The path surface learned to render a
// deny arm - it handled only advise, so a deny rendered EMPTY while magus exited 2, and
// both scripts read empty-output-plus-nonzero as a broken guard and exit 0, which every
// host takes as allow. And the templates now resolve ./magus before PATH: an older PATH
// binary does not fail when it lacks a rule, it reads the config key that ARMS the rule
// as unknown and answers pass, so the guard enforces nothing at exit 0.
//
// 7: the advise arm is now suppressible. A host whose pre-tool-use hook REJECTS
// the context key - treating it as an error and then failing OPEN - was not merely
// ignoring an advisory, it was disarmed by one for that call. A copy that predates
// this keeps sending it and keeps failing open, which no verdict anywhere reveals.
// Suppression is opt-in per host (GUARD_NO_ADVISE), so the rendered response for a
// host that keeps the arm is byte-identical to version 6. Which hosts need it is
// recorded in their own guide pages, not here.
//
// 8: the templates find the binary by walking UP to the magusfile instead of testing
// `./magus` in the process's own directory. A hook runs in the host's SESSION
// directory, which is not always the workspace root, and every copy that predates
// this silently judges with PATH's binary there - or, where PATH's copy cannot load
// the workspace, does not judge at all. Version 4 established preferring the
// workspace's binary; this is the half of it that was only true from the root.
const GuardTemplateVersion = 8

// GuardTemplateMarker introduces the version line each template carries, and is
// what a reader greps for in their own copy.
const GuardTemplateMarker = "magus-guard-template:"

// GuardDecisions returns every decision a verdict can carry.
func GuardDecisions() []string { return append([]string(nil), guardDecisions...) }

// GuardSurfaces returns every input the guard judges.
func GuardSurfaces() []string { return append([]string(nil), guardSurfaces...) }
