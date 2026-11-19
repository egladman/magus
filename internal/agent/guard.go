package agent

import (
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/types"
)

// The guard's wire contract: the vocabulary every host glue must handle, in
// the one package that the CLI producing a verdict imports and the parity tests
// beside it read.
//
// It lived here because package main could not be imported. The rules are now
// internal/guard, so that reason has expired: the lists could move beside the Verdict
// they describe, and only the installed-artifact constants below still belong here.

// GuardAPIVersion is the version of the verdict envelope every host glue
// parses, carried on the wire as api_version. Bump it only when an existing
// field changes MEANING: adding an optional field that existing glues ignore is
// not a bump, and neither is adding a rule. A bump is expensive: a glue that
// meets a schema it does not recognize fails open, so every one of them must be
// re-downloaded before it guards again.
const GuardAPIVersion = verdict.APIVersion

// guardInputs is every input the guard judges: a shell command, a file path
// an edit is about to write (`magus shell --path`), or an MCP tool call
// (a tool name plus a params object, forwarded whole rather than reduced to a
// single string). A host wires each input to a different one of its events,
// and a host that cannot wire one covers less, which is a coverage difference
// to record, not to hide.
var guardInputs = []string{"command", "path", "mcp"}

// GuardTemplateVersion is the api_version of the hook templates a reader installs
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
// The numeric revisions 1 to 39 ended at magus.guard-template/v1alpha1. A bump is the
// next alpha: v1alpha2.
const GuardTemplateVersion types.APIVersion = "magus.guard-template/v1alpha1"

// GuardTemplateMarker introduces the version line each template carries, and is
// what a reader greps for in their own copy.
const GuardTemplateMarker = "magus-guard-template:"

// GuardDecisions returns every decision a verdict can carry. A new entry here is a
// promise that all four host glues can express it; the dogfood parity test is what
// collects on that promise.
//
// None of them says yes. A defer is no objection: a glue writes no permission decision
// for it, so the host's own permission flow decides the call. An ask blocks the call
// until the PERSON approves it through the host's own prompt. It is never an escape the
// agent can clear, and a glue that cannot prompt renders it as a deny.
func GuardDecisions() []string {
	return []string{string(verdict.Defer), string(verdict.Advise), string(verdict.Deny), string(verdict.Ask)}
}

// GuardInputs returns every input the guard judges.
func GuardInputs() []string { return append([]string(nil), guardInputs...) }
