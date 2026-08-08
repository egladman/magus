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
// path an edit is about to write (`magus hook --path`). A host wires each
// surface to a different one of its events, and a host that cannot wire one
// covers less - which is a coverage difference to record, not to hide.
var guardSurfaces = []string{"command", "path"}

// GuardTemplateVersion is the revision of the hook templates a reader installs
// into their agent host.
//
// The templates are the one shipped artifact with no self-correcting path. An
// installed skill is generated, stamped and regraded on every `graph verify`; a
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
const GuardTemplateVersion = 1

// GuardTemplateMarker introduces the version line each template carries, and is
// what a reader greps for in their own copy.
const GuardTemplateMarker = "magus-guard-template:"

// GuardDecisions returns every decision a verdict can carry.
func GuardDecisions() []string { return append([]string(nil), guardDecisions...) }

// GuardSurfaces returns every input the guard judges.
func GuardSurfaces() []string { return append([]string(nil), guardSurfaces...) }
