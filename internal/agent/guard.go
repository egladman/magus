package agent

// The guard's wire contract: the vocabulary every host glue must handle, in
// the one package that both the CLI producing a verdict and the repo-root
// dogfood tests can import.
//
// It lives here rather than beside the guard because package main cannot be
// imported, so a parity check outside it would have to RESTATE the contract, and
// a restated contract is the copy that goes stale. That reason expires the day
// the rules move out of cmd/magus into a package of their own; the lists should
// move with them then.

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

// guardSurfaces is every input the guard judges: a shell command, a file path
// an edit is about to write (`magus session hook --path`), or an MCP tool call
// (a tool name plus a params object, forwarded whole rather than reduced to a
// single string). A host wires each surface to a different one of its events,
// and a host that cannot wire one covers less, which is a coverage difference
// to record, not to hide.
var guardSurfaces = []string{"command", "path", "mcp"}

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
// Bump it whenever a template's BEHAVIOR changes, not for a comment or a
// rewording. TestShippedTemplatesCarryTheCurrentVersion makes the bump total:
// every template must be re-stamped or the build fails.
//
// 1, 5 and 6 have no entry, and the gap is recorded rather than left to be
// rediscovered: 1 is the marker's own starting value and predates this log, and
// nothing in the tree or its history says what 5 and 6 changed. Read them as
// versions nobody documented rather than as versions that mean something here.
//
// 2: docs/guides/integrations/agents/opencode-plugin.ts unconditionally passed
// the attribution flag, which no released binary accepts (v0.3.0 predates it):
// an older binary rejected it, the plugin's judge() got unparsable stdout, and
// every verdict silently allowed. The sh templates already retried without
// attribution on exactly this failure (magus-guard-command.sh's guard()); the
// plugin now does the same.
//
// 3: that flag is now --agent-name (was --host, which read as a network host)
// and the templates' variable is GUARD_AGENT_NAME (was GUARD_HOST). A copy
// still passing the old spelling degrades rather than breaks (the retry that
// version 2 added drops attribution and keeps the verdict), so an unbumped
// copy loses the activity trail's host label, not its guard.
//
// 4: two changes, neither released before this. The path surface learned to render a
// deny arm: it handled only advise, so a deny rendered EMPTY while magus exited 2, and
// both scripts read empty-output-plus-nonzero as a broken guard and exit 0, which every
// host takes as allow. And the templates now resolve ./magus before PATH: an older PATH
// binary does not fail when it lacks a rule, it reads the config key that ARMS the rule
// as unknown and answers pass, so the guard enforces nothing at exit 0.
//
// 7: the advise arm is now suppressible. A host whose pre-tool-use hook REJECTS
// the context key (treating it as an error and then failing OPEN) was not merely
// ignoring an advisory, it was disarmed by one for that call. A copy that predates
// this keeps sending it and keeps failing open, which no verdict anywhere reveals.
// Suppression is opt-in per host (GUARD_NO_ADVISE), so the rendered response for a
// host that keeps the arm is byte-identical to version 6. Which hosts need it is
// recorded in their own guide pages, not here.
//
// 8: the templates find the binary by walking UP to the magusfile instead of testing
// `./magus` in the process's own directory. A hook runs in the host's SESSION
// directory, which is not always the workspace root, and every copy that predates
// this silently judges with PATH's binary there (or, where PATH's copy cannot load
// the workspace, does not judge at all). Version 4 established preferring the
// workspace's binary; this is the half of it that was only true from the root.
//
// 9: the notice a template prints when the binary is found but cannot judge now names
// the evidence (which binary path it resolved, that binary's version, and the error it
// actually printed) instead of guessing. The wording it replaces blamed "too old for
// session hook, or cannot load this workspace", and the second half is not a cause: the
// deny rules need no workspace, so a reader who took the sentence at its word went looking
// for a workspace problem that was never there. A copy that predates this keeps sending
// them, which is why this bumps even though enforcement is unchanged.
// 10: the two notices a template prints when the guard is not enforcing are held to one
// firing per session, keyed on a TMPDIR marker rather than on magus, which is the thing
// that is missing when they fire. Measured over recent sessions: 2,741 unavailable and 653
// could-not-judge firings, 99% of them same-session repeats, and one session took 913. A
// copy that predates this keeps sending all of them, and a reader who has learned to skip
// the notice skips the one that mattered too.
//
// 11: the set grew its first template that RECORDS instead of judging, one that says where
// the work stood when a session stopped, and every file was restamped to keep the bump
// total. Nothing an installed copy already did changed. The line exists so the ledger has no
// hole: a version nobody explained reads as one nobody understood.
//
// 12: an advise now reaches the MODEL wherever a host has any channel for one. Two of the
// four wired hosts delivered advisories nowhere: one suppressed the context key on a claim
// its vendor's current reference contradicts, and one sent it on a gating event that
// carries a message only with a denial, so the advisory collapsed into a plain allow. A
// copy that predates this enforces every deny and silently drops every explanation, and
// nothing in a session reveals that half the contract stopped arriving. Two smaller
// changes ride along: a write is judged before it lands wherever a pre-write event exists
// (a deny on an after-the-write event is a warning, not a block), and the brief a
// compacted session is handed back renders as a JSON envelope on request, for a host that
// parses a session-start hook's stdout as a reply rather than reading it as context.
//
// 13: the contract grew a third surface, MCP tool calls, and magus-guard-command.sh grew
// HOST_EVENT_RAW to carry it: an MCP call has no single string to select with
// HOST_EVENT_PATH, only a tool name and a params object, so a host wiring this surface
// forwards the whole event instead of reducing it to one jq extraction. A copy that
// predates this has no HOST_EVENT_RAW arm at all, so a host that tries to wire an
// mcp__magus__* matcher through it ships the literal string "null" as the command to
// judge rather than the envelope the guard can at least recognize and pass through.
const GuardTemplateVersion = 13

// GuardTemplateMarker introduces the version line each template carries, and is
// what a reader greps for in their own copy.
const GuardTemplateMarker = "magus-guard-template:"

// GuardDecisions returns every decision a verdict can carry.
func GuardDecisions() []string { return append([]string(nil), guardDecisions...) }

// GuardSurfaces returns every input the guard judges.
func GuardSurfaces() []string { return append([]string(nil), guardSurfaces...) }
