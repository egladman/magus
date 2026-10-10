---
title: Guard hook templates
description: The Buzz hook templates Claude Code and Codex run for the magus guard, the checkpoint and the post-compaction brief, with the knobs that adapt them to a host, the version marker that tells you when your copy is stale, and the full source of each.
tags: [agents, guard, hooks, templates, claude code, codex]
---

# Guard hook templates

These are files, not snippets. They sit in
[`docs/guides/integrations/agents/`](https://github.com/egladman/magus/tree/main/docs/guides/integrations/agents):
download them from there, or copy a block below. magus's own repository invokes
the same files rather than keeping a private copy, so what it dogfoods is what
you get, and two tests fail if its config stops referencing them or a block here
drifts from the file.

They are a magus project of their own, so the TypeScript and Buzz beside them are
held to the same gates as the rest of the workspace
(`magus run lint docs/guides/integrations/agents` runs `tsc --noEmit`, Biome,
and `magus buzz --check`).

Two hosts run these files: [Claude Code](claude-code.md) and [Codex](codex.md).
Both wire the same Buzz templates, run as `magus buzz -s <file>`, so there is one
guard to reason about. Each needs neither a POSIX shell nor `jq`, so it runs
unchanged on Windows and installs nothing. `magus describe harness` prints the
entries that name the files; Magus does not inject a reserved command.
[Cursor](cursor.md) and [OpenCode](opencode.md) each ship one self-contained file
instead, on their own pages, because a host that needs five downloads to install a
guard ends up without one.

## Checking whether your copy is current

Once you copy a template into your host's config it is yours, and magus cannot
reach it again. That is the point (you are meant to edit these), but it means a
fix magus makes never arrives on its own, and nothing about your copy says how
old it is. So each one carries a version line:

```sh
grep magus-guard-template magus-command.buzz
```

Compare it with the version in the block below. If yours is lower or absent,
re-copy, and diff rather than overwrite, because your edits are worth keeping.
A missing line means the copy predates versioning entirely.

The version tracks BEHAVIOR, not wording: it moves when a template starts doing
something different, not when a comment is rewritten. It is deliberately not a
checksum, because these are yours to modify and a checksum would flag your own
edits as drift.

This is the one part of the agent integration with no automatic staleness check.
Installed skills are generated, so `magus doctor` regrades them against
the binary; a copied hook template is owned by you, and this line stands in for
that. `magus doctor`'s **guard wiring** check reads the marker in whatever file
your host config points at, and fails when it is stale or missing.

## How they fit together

One template per guard input: `magus-command.buzz` for shell commands and MCP calls,
`magus-path.buzz` for file writes. A hook command is a plain argv (the host
splits it and runs `magus buzz` itself), so what varies PER ENTRY rides on that
argv, after `--`:

- `--agent-name <host>`, on every command. The template reads the host from there
  and nowhere else: not from the event's shape, not from the environment, never by
  default. The configuration `magus describe harness` prints renders it from the
  harness spell's own name. A template given none refuses the call
  ([MGS3024](../../../reference/codes/sandbox/MGS3024.md)) rather than answer in a
  guessed host's dialect, because a reply shaped for the wrong host can let a call
  through.
- `magus shell` flags an entry declares about itself, such as
  `-- --agent-name claude-code --reports-skills`. The template parses them
  against the flags it publishes in `SUPPORTED_FLAGS`. An argument it does not know is
  named on stderr and left out, and the call is judged anyway; your host shows that
  line as a hook error. Nothing is dropped quietly: a guard running with flags nobody
  chose is the failure this shape exists to avoid, and so is a guard that refuses to
  answer because an entry was typed wrong.

Whether to forward the whole event is worked out from the event, not set on the
entry: `magus-command.buzz` selects one field only when the tool is not in the
`mcp__` namespace AND `HOST_EVENT_PATH` finds a string there. Everything else goes
whole, because magus's own decoder knows every payload shape it reads and answers
that there is nothing to judge for the rest, while a field selected out of an
unrecognized shape gets judged as a command line it never was.

What is a property of the HOST rather than of one entry is an environment variable,
set once:

| variable                       | what it is                                                                                               |
| ------------------------------ | -------------------------------------------------------------------------------------------------------- |
| `HOST_EVENT_PATH`              | dot-path to the command or file path inside your host's event JSON                                       |
| `HOST_SESSION_PATH`            | dot-path to the session id inside your host's event JSON                                                 |
| `HOST_RESPONSE`                | Go template rendering your host's reply from the verdict                                                 |
| `__MAGUS_UNAVAILABLE_RESPONSE` | what to print when magus is missing, so each host picks its own fail-open or fail-closed stance          |
| `__MAGUS_FAILED_RESPONSE`      | the same, for a magus that is found but cannot judge the input; unset, the notice is built from evidence |
| `__MAGUS_BIN`                  | absolute path to magus when it is not on PATH                                                            |

To magus, the name and `HOST_SESSION_PATH` are attribution: they label the recorded
observation and cannot change a verdict. The template does read the name for one thing,
which reply shape its host can parse.

`__MAGUS_BIN` avoids the `MAGUS_*` prefix on purpose. That space is magus's
own settings, and a variable these templates invent must not look
like a setting magus reads.

Each command is `magus buzz -s <file>`: `-s` keeps the interpreter's own
advisories off stderr, which a host would otherwise show as a hook error. What the
templates need is a `magus` new enough to run them, because the interpreter has a
version. A magus too old to run the script renders no verdict, and your host
reports that as a hook error rather than passing the call silently; see
[Claude Code](claude-code.md) for what that looks like.

They import no magus Buzz module and no spell, and a test refuses one. A hook
fires on every tool call, and either import would make it pay for opening the
workspace: about 700ms here, against roughly 10ms for a script that reads none of
it.

## `magus-command.buzz`

The command guard. Wired to the host's `Read` tool as well, it restates the read
as the shell line it stands for (`cat <file>`, or `sed -n <first>,<last>p <file>`
over its offset and limit, a limit over 300 counting as whole), so the read rules
judge a `Read` exactly as they judge the `cat` it replaces.

```buzz
// magus guard hook: judges ONE shell command an agent is about to run.
//
// It needs neither jq nor a POSIX shell, so the file runs unchanged on Windows.
//
// Run it as `magus buzz -s magus-command.buzz`. `-s` is load-bearing, not tidiness:
// without it a BZZ advisory on stderr reads to the host as a hook error, so the glue
// reports itself broken. It imports no magus Buzz
// module and no spell, which is what keeps the workspace CLOSED: a script that
// reads no workspace member starts in about 10ms, where opening one costs
// roughly 700ms on every tool call.
//
// Contract: reads the host's event as JSON on stdin, selects its command, then
// feeds the command to `magus shell`. It writes the host's response on stdout
// and exits 0 either way.
//
// A hook command is an ARGV, not a shell line: the host splits it and runs the
// program itself, so a `NAME=value` prefix only means anything where something
// re-joins and re-parses the string. So this file is wired as a plain
// `<interpreter> buzz -s <this file> [-- flags]` and takes the two knobs that vary
// PER ENTRY from what it already has: the event decides whether to forward the whole
// envelope, and the argv after `--` carries the `magus shell` flags this wiring
// declares about itself. Everything else is still an environment variable, because
// it is a property of the HOST rather than of one entry, and a host sets it once.
//
//   -- --agent-name <host>  REQUIRED: the host this entry is wired into, the harness
//                    spell's own name, which the configuration `magus describe
//                    harness` prints puts on every command. It is the ONLY place this file
//                    learns the host: never the event's shape, never the environment,
//                    never a default. Without it `magus shell` refuses the call
//                    (MGS3024), so a hand-written config that omits it fails loudly
//   -- <flags>       `magus shell` flags this entry declares, one argv word each, parsed
//                    against SUPPORTED_FLAGS below. Capabilities, not policy: a config
//                    that also matches its host's skill tool passes
//                    `-- --reports-skills`, and rules that require a skill load
//                    stand down where it is absent. An argument this file does not know
//                    is REPORTED on stderr and left out, and the call is judged anyway:
//                    a misconfigured entry must not block work, and must not be silent
//
// Override any of the variables below:
//
//   HOST_EVENT_PATH  dot-path to the command inside your host's event
//   HOST_TOOL_PATH   dot-path to the tool name, which decides whether the whole envelope
//                    is forwarded rather than one field selected out of it
//   HOST_SESSION_PATH  dot-path to the session id inside your host's event
//   HOST_AGENT_PATH  dot-path to the subagent id inside your host's event, so a
//                    subagent's command is graded under the job it was spawned for
//                    rather than as its parent
//   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
//   HOST_MESSAGE_PATH  dot-path to the text of a message the person typed (default
//                    prompt, the key Claude Code's envelope uses). An event that names no
//                    tool and carries a string there is a message rather than a call: it
//                    goes to `magus shell --message`, which records the topics it raises,
//                    and this file prints NOTHING for it. A message hook's output reaches
//                    the model, and recording one is not something to tell the model about
//   HOST_CWD_PATH    dot-path to the directory the call runs in, whose checkout's own
//                    ./magus judges it (default cwd)
//   HOST_RESPONSE    Go template rendering your host's reply
//   HOST_ADVISE_BRANCH  the advise arm of that template
//   HOST_ASK_BRANCH  the ask arm of that template: the reply that puts the call in
//                    front of the PERSON through the host's own approval prompt
//   __MAGUS_NO_ADVISE  set it when the host has no context-injection channel, so
//                    an advise renders nothing rather than a reply it rejects
//   __MAGUS_BIN  path to the binary, when it is not on PATH
//   __MAGUS_UNAVAILABLE_RESPONSE  what to print when magus cannot be found, so a
//                    host can choose its own fail-open or fail-closed stance
//   __MAGUS_FAILED_RESPONSE  the same, for a magus that IS found but cannot judge
//                    the command. Left unset, this file builds one from evidence:
//                    which binary it resolved, that binary's version, and the
//                    error it actually printed
//   __MAGUS_UNREADABLE_RESPONSE  what to print when the call never arrived, which is
//                    the one case here that denies rather than failing open
//   __MAGUS_NOTICE_WINDOW  minutes a notice marker survives on a host that reports
//                    no session id, so one session cannot silence every later one
//
// The defaults are Claude Code's event and response shape.
//
// The host name and the session are ATTRIBUTION to magus, not policy. magus records them
// on its activity event so a reader can tell which host produced an observation;
// neither one can change the verdict, and a host whose event carries no session
// id records none and is judged exactly the same. The subagent id is the exception:
// magus grades a subagent it saw spawned under that spawn's job. This file does read the
// host name for one thing, which reply shape its host can parse, and that is why it must
// be given.
//
// __MAGUS_BIN is deliberately NOT called MAGUS_BIN: the whole MAGUS_* space is
// magus's own settings, so a variable this template invents must stay
// out of it rather than look like a setting magus reads.
//
// On a missing magus this prints a visible notice rather than exiting quietly. A
// guard that exits silently never runs and nothing says so, and an unguarded
// session you know about beats one you do not.
//
// NOTHING here may raise. Claude Code reads a non-zero hook exit other than 2 as a
// non-blocking error and runs the call anyway, and exit 2 BLOCKS with stderr as the
// message, which would turn a Buzz bug into a refusal the person cannot read. Every
// call that can fail is caught, and the script ends by writing one reply.
//
// The line below declares, per guard input, how much of a verdict this file
// can carry: model (reaches the agent), human (reaches the person only), or none
// (not delivered). It is machine-read by the host-parity gate, which fails the
// build when a decision or input exists in the guard contract that some host
// was never asked about. Keep it true to what HOST_RESPONSE actually renders.
//
// An ask reaches the person on both hosts, by different routes. Claude Code takes
// permissionDecision "ask" from PreToolUse and prompts. Codex parses that value and does
// not support it: the hook run is marked failed and the call CONTINUES, so on Codex this
// file never emits it. There the prompt comes from a rules file the codex harness writes
// (.codex/rules/magus.rules, a prefix_rule on git push with decision "prompt"), and the
// PermissionRequest event Codex raises before that prompt reaches this same file, which
// answers allow for a push the gate covers, leaves an ungated one to the person, and
// denies a leased worker's. Where Codex cannot prompt at all (no rules file, a
// permission_mode that never asks, a call no rule matches) the ask renders as a deny that
// names the person's own terminal.
// magus-guard-template: 21
// magus-guard-coverage: schema=2 host=claude-code input=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=codex input=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=claude-code input=mcp deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=codex input=mcp deny=model advise=model pass=none ask=model
// The mcp rows are real: an mcp__magus__* PreToolUse call carries no tool_input.command,
// so this file forwards the whole event instead (see hook\wholeEvent), and the same hookSpecificOutput
// reply it already renders for the command input carries a deny or an advise on this one too.
// No rule prompts for an MCP call, so Codex's mcp ask is the deny that names the terminal.

import "std";
import "flags";
import "io";
import "encoding/json";
import "env";
import "fs";
import "path";
import "proc";
import "lib/hook" as hook;

// ADVISE_DEFAULT and the two ask arms are separate strings because a host may
// replace one arm without replacing the whole reply. A host that REJECTS the
// context key is worse off than one that ignores it: an unsupported field can
// make the host mark the hook run failed and continue the call, so an advisory
// it cannot take disarms the guard rather than merely going unread. No host
// shipped here is in that position today, since both hosts wired to this file
// take additionalContext, so __MAGUS_NO_ADVISE has no user and is kept for the
// one you may wire.
final ADVISE_DEFAULT = `{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}`;

final ASK_CLAUDE = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":{{toJson .reason}}}}`;

final ASK_CODEX_PROMPTS = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .reason}}}}`;

// A backtick string DOES interpolate: a `{...}` run is evaluated when its contents parse as
// a Buzz expression, and stays literal only when they do not. Go template braces do not
// parse, which is the only reason these render verbatim, and a `{name}` fragment would not.
// So a template is spliced through this placeholder rather than concatenated around an
// open `{{`, which would swallow its own closing backtick.
final BLOCKER_SLOT = "__MAGUS_ASK_BLOCKER__";
final ASK_CODEX_BLOCKED = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print .reason "\n\nThis call needs the approval of the person you work for, and __MAGUS_ASK_BLOCKER__. Ask them to run it from their own terminal.")}}}}`;

// A decision this file does not know is refused, never allowed: the guard contract
// grows, and a copy older than the growth must not read the new verdict as a pass.
final UNKNOWN_DECISION = `{{else}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}{{end}}`;

final DENY_HEAD = `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}`;
final PASS_AND_ADVISE_TAIL = `{{else if eq .decision "advise"}}{{else if eq .decision "pass"}}`;

// The stdin rewrite. magus sets updated_command on a pass or an advise when it closed the
// command's stdin, and the reply hands the host its whole tool input back with that
// command in place: Claude Code's updatedInput REPLACES the input, so a field left out
// (description, timeout, run_in_background) would be dropped. No permissionDecision rides
// with it, so the rewritten call still meets the host's own permission rules.
//
// Read through `index`: magus renders with missingkey=error, and the key is absent from
// every verdict that carries no rewrite.
//
// Codex is never handed one. Its PreToolUse applies updatedInput only beside
// permissionDecision "allow", which would turn every guard pass into an approval that
// skips Codex's own prompt.
final UPDATED_INPUT_SLOT = "__MAGUS_UPDATED_INPUT__";
final UPDATED_COMMAND = `{{toJson .}}`;
final ADVISE_REWRITE = `{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}{{with index . "updated_command"}},"updatedInput":__MAGUS_UPDATED_INPUT__{{end}}}}`;
final REWRITE_ONLY = `{{with index . "updated_command"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":__MAGUS_UPDATED_INPUT__}}{{end}}`;

final PERMISSION_NO_DECISION = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest"}}`;
final PERMISSION_ALLOW = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`;
final PERMISSION_DENY_HEAD = `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":{{toJson .reason}}}}}{{else if eq .decision "ask"}}`;
final PERMISSION_UNKNOWN = `{{else}}{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}}{{end}}`;

final CONTEXT_SLOT = "__MAGUS_CONTEXT__";
final CONTEXT_REPLY = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":__MAGUS_CONTEXT__}}`;

// The prose, not the reply: the two event replies wrap it differently, and holding it once is
// what keeps them from drifting into two different sentences about one fact.
final UNAVAILABLE_TEXT = "magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set __MAGUS_BIN to its path, to restore the guard.";

// The one arm here that does not fail open. Elsewhere magus is missing or cannot answer;
// here nothing arrived, so no rule was ever offered the call. `magus shell` denies an
// unreadable payload for the same reason and leaves this case to its caller.
final UNREADABLE_DEFAULT = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"magus guard could not read this call from the host, so nothing was judged. A payload that arrives truncated reads exactly like an empty one, which is why this is blocked rather than cleared. Retry the call."}}`;

// The characters that let one command line become several, or become a different
// one. A push carrying any of them reaches no Codex prefix rule.
final SHELL_METACHARACTERS = [";", "&", "|", "`", "$", "(", ")", "<", ">", "\\", "\n"];


// What magus's own decoder tests to tell an envelope from a bare command line. `\{` escapes
// the interpolation a lone brace would open.
final ENVELOPE_PREFIX = "\{";

// Every `magus shell` flag an entry may declare on this script's argv, by exact spelling.
//
// A list rather than a rule, because this file has to be able to say "I do not know that
// one". Capabilities only: a flag here states something about the WIRING that the event
// cannot state for itself, which today is one thing, whether the config also matches its
// host's skill tool. Policy stays in the workspace's own rules, where a reader can see it
// and magus can change it without every installed config being edited.
final SUPPORTED_FLAGS = ["--reports-skills"];

// The host this entry is wired into. Valued, and read by this file rather than forwarded
// as one of SUPPORTED_FLAGS, because the file needs it too: it picks the ask arm.
final AGENT_NAME_FLAG = "--agent-name";


// findUp returns the nearest ancestor of start holding relative, or "" when there is none.
fun findUp(relative: str, start: str) > str {
    var dir = start;
    if (dir == "") { dir = path\abs(".") catch ""; }
    while (dir != "") {
        final candidate = "{dir}/{relative}";
        final found = fs\isFile(candidate) catch false;
        if (found) { return candidate; }
        dir = hook\parentDir(dir);
    }
    return "";
}

// codexPushRule names the Codex prefix rule a bare push reaches, or null for anything
// else: a compound line, `git -C dir push`, an MCP call. Those reach no rule, so Codex
// would run them unprompted and no answer here may assume it prompts.
fun codexPushRule(line: str) > str? {
    foreach (meta in SHELL_METACHARACTERS) {
        if (line.indexOf(meta) != null) { return null; }
    }
    if (line == "git push" or line.startsWith("git push ")) { return `["git","push"]`; }
    if (line == "hg push" or line.startsWith("hg push ")) { return `["hg","push"]`; }
    if (line == "sl push" or line.startsWith("sl push ")) { return `["sl","push"]`; }
    if (line == "jj git push" or line.startsWith("jj git push ")) { return `["jj","git","push"]`; }
    return null;
}

// codexPromptBlocker states why Codex will not put this call in front of the person,
// and "" when its own approval prompt will.
fun codexPromptBlocker(event: any?, pushRule: str?) > str {
    final mode = hook\field(event, dotPath: "permission_mode");
    if (mode == "bypassPermissions" or mode == "dontAsk") {
        return "this Codex session runs in permission_mode {mode}, which never prompts";
    }
    if (pushRule == null) {
        return "no Codex approval rule matches this call, only a plain git push, hg push, sl push or jj git push command";
    }
    final rules = findUp(".codex/rules/magus.rules", start: hook\field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd")));
    if (rules != "") {
        // A file that is THERE and unreadable is not the same as one that carries no
        // matching rule, and collapsing the two produced a deny whose stated reason was
        // false. Named separately so the person is told what to look at.
        final body = fs\readFile(rules) catch null;
        if (body == null) {
            return "{rules} exists but could not be read, so whether it carries the prompt rule for this push is unknown";
        }
        // Compared with the spacing removed, so the rule matches however it is
        // formatted. The leading bracket is what keeps the git rule from being found
        // inside jj's ["jj", "git", "push"].
        final packed = body!.replace(" ", with: "").replace("\t", with: "");
        if (packed.indexOf(pushRule!) != null) { return ""; }
    }
    return "no .codex/rules/magus.rules carries the prompt rule for this push, which magus describe harness codex prints";
}

// shellFlags parses this script's own argv: the `magus shell` flags the entry that wired
// it declares about itself, already split into words by the host that ran the command.
//
// Parsed against SUPPORTED_FLAGS, never filtered by shape. The three answers are: a flag
// this file knows, which is used; anything else, which is REPORTED on stderr and left out;
// and no argv at all, which is the ordinary entry. There is no fourth.
//
// Reporting is the whole point. Dropping an argument silently leaves a guard judging with
// flags nobody chose and nothing anywhere saying so, which is the same failure this file
// was just rewritten to remove. Forwarding an unknown one is no better: `magus shell`
// rejects it, prints usage, exits non-zero, and the verdict is lost to a usage error.
// Saying it and carrying on is the arm that matches every other thing that can go wrong
// here (see the unavailable and could-not-judge notices): the call is not blocked for a
// misconfiguration, and the misconfiguration is not silent. A host shows a hook's stderr
// as an error notice, so the person who wrote the entry is the one who reads it.
// The parse is the flags module's; the POLICY is this file's. It declares what it
// supports, and decides for itself that an argument it did not declare is reported and
// dropped rather than refused, because a misconfigured entry must not block work.
fun shellFlags(args: [str]) > [str] {
    final parsed = flags\parse(args, switches: SUPPORTED_FLAGS, valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) {
        // Unreachable while every supported flag is a switch, and caught anyway: NOTHING
        // here may raise, and a `catch` that exists only for that is cheaper than a
        // guarantee that has to be re-checked whenever the list grows a valued flag.
        warn("could not parse this entry's arguments; the call was judged without them");
        return [<str>];
    }
    foreach (word in parsed!.unknown) {
        // Named in quotes because an empty or whitespace argument otherwise produces
        // "unsupported argument ;", and this line is the only remedy a misconfigured
        // reader is offered.
        warn("unsupported argument \"{word}\"; this call was judged WITHOUT it. "
            + "Supported: {SUPPORTED_FLAGS.join(", ")}. Fix the hook command in your host config.");
    }
    final supported = mut [<str>];
    foreach (name in SUPPORTED_FLAGS) {
        if (parsed!.values[name] != null) { supported.append(name); }
    }
    return supported;
}

// readAgentName is the host this entry names on its argv, or "" when it names none. Empty is
// not defaulted: `magus shell` refuses installed glue that names no host (MGS3024).
fun readAgentName(args: [str]) > str {
    final parsed = flags\parse(args, switches: SUPPORTED_FLAGS, valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) { return ""; }
    return parsed!.values[AGENT_NAME_FLAG] ?? "";
}

// warn names the file, because a host reports a hook's stderr with the entry's matcher at
// best and nothing at all at worst, and a reader with eight entries needs to know which.
fun warn(message: str) > void {
    io\stderr.write("magus-command.buzz: {message}\n") catch void;
}


// Guard carries everything one judgment call needs, so the three call sites (the
// verdict, the unattributed retry, and the stderr capture for the failure notice)
// cannot drift apart.
object Guard {
    bin: str,
    payload: str,
    flags: [str],
    response: str,
    // The event's cwd. The guard reads its workspace, job store and lease binding from
    // its own working directory, and a host may start hooks in the repository's main
    // checkout while the session works in a linked worktree.
    dir: str,
}

// The entry's own flags ride in `extra` with everything else, so the unattributed retry
// drops them too. Keeping them there meant a binary too old for `--reports-skills`
// failed BOTH attempts and the spawn entry could never recover where the Bash entry did, and a
// binary too old to accept the flag cannot report a skill load to begin with.
fun judge(guard: Guard, extra: [str]) > proc\ExecResult !> any {
    final args = mut ["shell"];
    foreach (arg in extra) { args.append(arg); }
    args.append("-o");
    args.append("template={guard.response}");
    return proc\exec(guard.bin, args: args, dir: guard.dir, opts: {
        "quiet": true,
        "allow_failure": true,
        "stdin": guard.payload,
    });
}

// recordMessage hands a message the person typed to `magus shell --message` and reads
// nothing back. Every failure is silence: a binary missing, or too old for --message, costs
// the record and never the message, and nothing printed here could be anything but noise in
// the model's context. `attribution` is the same argv the judged calls carry.
fun recordMessage(bin: str, message: str, dir: str, attribution: [str]) > void {
    if (bin == "") { return; }
    final args = mut ["shell", "--message"];
    foreach (word in attribution) { args.append(word); }
    proc\exec(bin, args: args, dir: dir, opts: {
        "quiet": true,
        "allow_failure": true,
        "stdin": message,
    }) catch void;
}

// failureNotice states WHICH binary went silent, what version it is, and what it
// actually said, the three facts a reader otherwise spends a session collecting.
//
// The stderr comes from the attempt that ALREADY failed rather than a fresh one. Re-running
// cost a third process inside the host's 10s budget, on the path where the binary is the
// thing going wrong, so the arm that exists to make a silent failure visible was the
// likeliest to be killed before writing anything; it also reported whatever a NEW run
// printed, which is not necessarily what went wrong. WARN lines are dropped because a
// config the binary is too old to parse warns BEFORE it fails, and that warning is a
// symptom of the same staleness, not the error.
fun failureNotice(guard: Guard, failed: proc\ExecResult?) > str {
    var version = "";
    final probe = proc\exec(guard.bin, args: ["version"], opts: {"quiet": true, "allow_failure": true}) catch null;
    if (probe != null) { version = hook\firstLine(probe!.stdout); }
    if (version == "") { version = "version unreadable"; }

    var why = "";
    if (failed != null) {
        foreach (line in failed!.stderr.split("\n")) {
            if (why == "" and line != "" and line.indexOf("WARN") == null) { why = line; }
        }
    }
    if (why == "") { why = "it printed no error"; }

    return "magus guard is NOT running: {guard.bin} ({version}) could not judge this command, "
        + "so its deny and advise rules are unenforced. It said: {why}. "
        + "Rebuild or update THAT binary to restore the guard.";
}

// A read tool's limit past this reads the whole file, as the read rule counts it.
final WHOLE_READ_LIMIT = 300;

// readCommand is the shell line a host's Read stands for, "" for any other tool: `cat` of
// the file, or a sed range over its offset and limit. The read rules then judge it as the
// command it replaces, the way Cursor's hook already restates its Read.
fun readCommand(event: any?, tool: str) > str {
    if (tool != "Read") { return ""; }
    final input = hook\dig(event, dotPath: "tool_input");
    var file = hook\field(input, dotPath: "file_path");
    if (file == "") { file = hook\field(input, dotPath: "path"); }
    if (file == "") { return ""; }
    final word = "'" + file.replace("'", with: "'\\''") + "'";
    final limit = std\parseInt(hook\field(input, dotPath: "limit")) ?? 0;
    var first = std\parseInt(hook\field(input, dotPath: "offset")) ?? 1;
    if (first < 1) { first = 1; }
    if (limit > 0 and limit <= WHOLE_READ_LIMIT) { return "sed -n {first},{first + limit - 1}p {word}"; }
    if (first > 1) { return "sed -n {first},$p {word}"; }
    return "cat {word}";
}

fun contextReply(text: str) > str {
    final encoded = json\stringify(text) catch `""`;
    return CONTEXT_REPLY.replace(CONTEXT_SLOT, with: encoded);
}

// noticeReply renders a notice in the dialect of the event that asked for it.
//
// A PermissionRequest reply carries a decision and no context field, so the PreToolUse
// envelope the other events use is one the host rejects outright: on the single event
// where the notice is the only thing written, it arrived unreadable. There the prose goes
// to the person on stderr and stdout carries the envelope that leaves the decision alone.
fun noticeReply(eventName: str, text: str) > str {
    if (eventName == "PermissionRequest") {
        warn(text);
        return PERMISSION_NO_DECISION;
    }
    return contextReply(text);
}

// askBranch picks the ask arm for the host that sent this event. Only a reply this
// file assembled may claim --renders-ask; see the header for why Codex never receives
// permissionDecision "ask".
fun askBranch(event: any?, codex: bool, pushRule: str?) > str {
    final declared = env\get("HOST_ASK_BRANCH") catch "";
    if (declared != "") { return declared; }
    if (!codex) { return ASK_CLAUDE; }
    final blocker = codexPromptBlocker(event, pushRule: pushRule);
    if (blocker == "") { return ASK_CODEX_PROMPTS; }
    return ASK_CODEX_BLOCKED.replace(BLOCKER_SLOT, with: blocker);
}

// adviseBranch is the advise arm; updatedInput is the host's tool input with the command's
// template action in place, "" when this reply carries no rewrite.
fun adviseBranch(updatedInput: str) > str {
    final suppressed = env\get("__MAGUS_NO_ADVISE") catch "";
    if (suppressed != "") { return ""; }
    final declared = env\get("HOST_ADVISE_BRANCH") catch "";
    if (declared != "") { return declared; }
    if (updatedInput == "") { return ADVISE_DEFAULT; }
    return ADVISE_REWRITE.replace(UPDATED_INPUT_SLOT, with: updatedInput);
}

// passAndAdviseTail renders a pass as nothing, and as the rewrite alone when it carries
// one. The advise arm here is reached only when adviseBranch rendered none.
fun passAndAdviseTail(updatedInput: str) > str {
    if (updatedInput == "") { return PASS_AND_ADVISE_TAIL; }
    final only = REWRITE_ONLY.replace(UPDATED_INPUT_SLOT, with: updatedInput);
    return `{{else if eq .decision "advise"}}` + only + `{{else if eq .decision "pass"}}` + only;
}

// permissionResponse answers the approval request Codex raises just before its own
// prompt. No decision object leaves the prompt to the person; allow skips it, and is
// answered only for a plain push, because this event fires for every approval Codex
// asks and a pass from the guard is not the person's consent to anything else.
fun permissionResponse(pushRule: str?) > str {
    var covered = PERMISSION_NO_DECISION;
    if (pushRule != null) { covered = PERMISSION_ALLOW; }
    return PERMISSION_DENY_HEAD + PERMISSION_NO_DECISION
        + `{{else if eq .decision "pass"}}` + covered
        + `{{else if eq .decision "advise"}}` + covered
        + PERMISSION_UNKNOWN;
}

fun main(args: [str]) > void {
    final raw = io\stdin.readAll() catch null;
    final event = json\parse(raw ?? "") catch null;

    // A payload that opens like an envelope but does not parse is a TRUNCATED one, not a
    // command line. Judged as text it matches no rule and passes, so it is refused here
    // while its shape still says what it was. The prefix test is magus's own decoder's.
    if (raw == null or (event == null and raw!.startsWith(ENVELOPE_PREFIX))) {
        io\stdout.write(hook\envOr("__MAGUS_UNREADABLE_RESPONSE", fallback: UNREADABLE_DEFAULT)) catch void;
        return;
    }

    final eventPath = hook\envOr("HOST_EVENT_PATH", fallback: "tool_input.command");
    final sessionPath = hook\envOr("HOST_SESSION_PATH", fallback: "session_id");
    final agentPath = hook\envOr("HOST_AGENT_PATH", fallback: "agent_id");
    final transcriptPath = hook\envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path");
    final agentName = readAgentName(args);
    // Overridable alongside the other dot-paths rather than fixed: a host that points
    // HOST_EVENT_PATH at a field it DOES populate for MCP calls loses the namespace test
    // otherwise, and gets that field judged as a shell command.
    final toolPath = hook\envOr("HOST_TOOL_PATH", fallback: "tool_name");
    final rawEvent = hook\wholeEvent(event, dotPath: eventPath, toolPath: toolPath);

    // Read BEFORE the availability check below, because the notices that check prints
    // are held to one firing per session and the session id is what keys them.
    final session = hook\field(event, dotPath: sessionPath);
    // Forwarded because this file selects one field out of the envelope: without it a
    // subagent's command reaches magus looking like its parent's.
    final agent = hook\field(event, dotPath: agentPath);
    final transcript = hook\field(event, dotPath: transcriptPath);
    final eventName = hook\field(event, dotPath: "hook_event_name");
    final toolName = hook\field(event, dotPath: toolPath);

    var pushRule: str? = null;
    if (!rawEvent) { pushRule = codexPushRule(hook\field(event, dotPath: eventPath)); }

    // Codex is the host the entry names, never one the event's shape suggests: see
    // AGENT_NAME_FLAG. An entry that names no host gets MGS3024 from magus, whatever arm
    // is assembled here.
    final codex = agentName == "codex";
    final read = readCommand(event, tool: toolName);

    var response = env\get("HOST_RESPONSE") catch "";
    var capabilities = mut [<str>];
    var rewrites = false;
    if (response == "" and eventName == "PermissionRequest") {
        response = permissionResponse(pushRule);
        capabilities.append("--renders-ask");
    } else if (response == "") {
        // Only a shell command the host itself will run can come back rewritten: a Read
        // restated as `cat` is not what the host runs, and a whole event is not a command.
        var updatedInput = "";
        if (!codex and !rawEvent and read == "" and eventName == "PreToolUse") {
            updatedInput = hook\objectWith(event, dotPath: "tool_input", key: "command", raw: UPDATED_COMMAND) ?? "";
        }
        response = DENY_HEAD + askBranch(event, codex: codex, pushRule: pushRule)
            + adviseBranch(updatedInput) + passAndAdviseTail(updatedInput) + UNKNOWN_DECISION;
        capabilities.append("--renders-ask");
        rewrites = updatedInput != "";
    }

    final cwd = hook\field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd"));
    final bin = hook\resolveBin(cwd: cwd);

    // A message, by its shape: no tool, and text where HOST_MESSAGE_PATH points. Ahead of the
    // availability notice, which a message hook would hand the model as context.
    final message = hook\dig(event, dotPath: hook\envOr("HOST_MESSAGE_PATH", fallback: "prompt")) as? str;
    if (toolName == "" and message != null) {
        recordMessage(bin, message: message!, dir: cwd, attribution: [
            "--agent-name", agentName, "--transport", "buzz", "--session", session,
            "--agent", agent, "--transcript", transcript, "--event", eventName,
        ]);
        return;
    }

    if (bin == "" or !hook\isExecutable(bin)) {
        if (hook\noticeOnce(session, family: "unavailable-{toolName}", file: "magus-command.buzz")) {
            io\stdout.write(hook\envOr("__MAGUS_UNAVAILABLE_RESPONSE",
                fallback: noticeReply(eventName, text: UNAVAILABLE_TEXT))) catch void;
        }
        return;
    }

    var payload = raw!;
    if (read != "") {
        payload = read + "\n";
    } else if (!rawEvent) {
        payload = hook\rawField(event, dotPath: eventPath) + "\n";
    }
    final guard = Guard{ bin = bin, payload = payload, flags = shellFlags(args), response = response, dir = cwd };

    // Attribution is BEST EFFORT; the verdict is not.
    //
    // --agent-name, --session and --agent postdate the current magus release, and this template is
    // downloaded and run against whatever binary a reader already has. Passing them
    // unconditionally does not degrade the guard, it BREAKS it: an older binary rejects the
    // unknown flag, prints its usage to stdout, and exits non-zero, so the host receives no
    // verdict at all and every deny and advise rule silently stops being enforced.
    //
    // A DENY exits non-zero (2) with the verdict on stdout, so retrying on a non-zero status
    // alone would judge every blocked command twice, unattributed and recorded twice in the
    // activity trail. Emptiness alone cannot tell the cases apart either, because a pass
    // renders empty on purpose. Both together can: a rejected flag prints its usage to
    // STDERR and leaves stdout empty, while any real verdict that is not a pass leaves
    // something on stdout.
    //
    // --renders-ask rides the attributed call only. A binary too old for it is too old to
    // ask, so the retry dropping it loses nothing.
    //
    // --rewrites-input is newer than the rest, so a binary that rejects it is asked again
    // with everything else before it is asked with nothing: dropping the rewrite must not
    // also drop the attribution and the ask.
    //
    // --transport buzz names this glue as the caller. magus keeps a deny's full text and
    // its once-per-session notices per host, transport and session.
    final attributed = mut [<str>];
    foreach (flag in guard.flags) { attributed.append(flag); }
    foreach (word in ["--agent-name", agentName, "--transport", "buzz", "--session", session, "--agent", agent, "--transcript", transcript]) {
        attributed.append(word);
    }
    foreach (flag in capabilities) { attributed.append(flag); }
    var result: proc\ExecResult? = null;
    if (rewrites) {
        final rewriting = mut [<str>];
        foreach (word in attributed) { rewriting.append(word); }
        rewriting.append("--rewrites-input");
        result = judge(guard, extra: rewriting) catch null;
    }
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        result = judge(guard, extra: attributed) catch null;
    }
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        result = judge(guard, extra: [<str>]) catch null;
    }

    // A PASS and a BROKEN GUARD both render nothing, and telling them apart is the whole
    // point of this block. A pass exits 0 with empty output because there was nothing to
    // say; a binary that cannot run, too old for `magus shell`, unable to load the
    // workspace, half-written by a concurrent build, exits non-zero with empty output, and
    // printing that as a pass silently disables every rule with nothing anywhere saying so.
    //
    // Fail OPEN either way. A guard that blocks work because it cannot judge it has its
    // priorities backwards, and an unguarded session you know about beats one you do not.
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        if (hook\noticeOnce(session, family: "failed-{toolName}", file: "magus-command.buzz")) {
            final chosen = env\get("__MAGUS_FAILED_RESPONSE") catch "";
            if (chosen != "") {
                io\stdout.write(chosen) catch void;
            } else {
                io\stdout.write(noticeReply(eventName, text: failureNotice(guard, failed: result)) + "\n") catch void;
            }
        }
        return;
    }
    io\stdout.write(hook\trimTrailingNewlines(result!.stdout)) catch void;
}
```

## `magus-path.buzz`

The declared-output guard. Wire it to your host's file-editing tool rather than
its shell tool. It explains rather than blocks: editing a generated file is
wasteful, not destructive.

```buzz
// magus guard hook: judges ONE file path an agent is about to write.
//
// Companion to magus-command.buzz, wired to your host's file-editing tool
// rather than its shell tool. It needs neither jq nor a POSIX shell.
//
// Run it as `magus buzz -s magus-path.buzz`; see magus-command.buzz for why `-s` is
// load-bearing. It imports no magus Buzz module
// and no spell, so the workspace stays closed and the script starts in about
// 10ms instead of paying roughly 700ms per tool call.
//
// The declared-output rule here is the one guard rule that is not a heuristic:
// magus reads every target's DECLARED outputs, so a generated file is generated
// by definition and an edit to it would be overwritten by the next run.
//
// That rule ADVISES rather than blocks. magus denies only what cannot be undone;
// a hand-edited generated file is wasteful, not destructive, since regenerating
// erases it. So it explains that the edit will be overwritten and lets the agent
// correct itself, rather than treating it as unable to learn. Every file-write rule
// says nothing on any uncertainty, no magus, no workspace, an unclaimed
// path, because an advisory fired on a guess trains the reader to ignore it.
//
// HOST_RESPONSE renders BOTH arms even though the rules shipping today only
// advise. That is deliberate, and it is why the arm predates any rule that fires it.
// These files are COPIED into a reader's config and never self-correct, so a
// deny arm added at the same time as the first denying rule would fail OPEN on
// every already-installed copy: the deny renders empty, magus exits non-zero,
// and the tail below reads empty-output-plus-nonzero as a broken guard and exits
// 0, which every host takes as allow. Shipping the arm first gives installed
// copies a window to update against a rule that is not yet firing.
//
// A host with no file-write hook still gets the command rules; it just misses
// this one. That is a coverage difference to record, not a reason to skip it.
//
// `-- --agent-name <host>` and HOST_SESSION_PATH work exactly as they do in
// magus-command.buzz: the host name is REQUIRED on this script's argv, and both are
// attribution recorded on the activity event, never an input to the verdict. So does
// `-- --reports-skills`, the one capability an entry here may declare.
//
// Every knob this file reads, each meaning what its magus-command.buzz twin means:
//
//   HOST_EVENT_PATH, HOST_TOOL_PATH, HOST_SESSION_PATH, HOST_AGENT_PATH, HOST_TRANSCRIPT_PATH,
//   HOST_CWD_PATH    dot-paths into the event; the tool name is what decides whether the
//                    whole envelope goes rather than one field selected out of it
//   HOST_RESPONSE, HOST_ASK_BRANCH, HOST_ADVISE_BRANCH  the reply template and its arms
//   __MAGUS_NO_ADVISE  render an advise as nothing, for a host with no context channel
//   __MAGUS_BIN      the binary, when it is not on PATH
//   __MAGUS_UNAVAILABLE_RESPONSE  what to print when magus cannot be found
//   __MAGUS_FAILED_RESPONSE  the same, for a magus found but unable to judge
//   __MAGUS_UNREADABLE_RESPONSE  what to print when the write never arrived, the one
//                    case here that denies rather than failing open
//
// NOTHING here may raise. Claude Code reads hook exit 2 as a BLOCK whose message
// comes from stderr, so an uncaught Buzz error would refuse a write with a stack
// trace as its reason. Every call that can fail is caught.
//
// Coverage declaration, machine-read by the host-parity gate; see the longer
// note in magus-command.buzz. It records what HOST_RESPONSE RENDERS, not
// which rules currently fire, so deny=model is true the moment the arm exists.
//
// No rule asks on a file write today. The arm exists for the same reason the deny arm did
// before its first rule: an installed copy never self-corrects. Claude Code prompts on it;
// Codex does not support a hook ask and no Codex rule prompts for a write, so there it
// renders as a deny.
// magus-guard-template: 21
// magus-guard-coverage: schema=2 host=claude-code input=path deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=codex input=path deny=model advise=model pass=none ask=model

import "io";
import "flags";
import "encoding/json";
import "env";
import "proc";
import "lib/hook" as hook;

final ADVISE_DEFAULT = `{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}`;

final ASK_CLAUDE = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":{{toJson .reason}}}}`;

final ASK_CODEX = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print .reason "\n\nThis write needs the approval of the person you work for, and Codex has no prompt for it. Ask them to make it themselves.")}}}}`;

// A decision this file does not know is refused, never allowed; see magus-command.buzz.
final UNKNOWN_DECISION = `{{else}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}{{end}}`;

// The one arm here that does not fail open; see magus-command.buzz. The call never
// arrived, so no rule was offered it.
final UNREADABLE_DEFAULT = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"magus guard could not read this write from the host, so nothing was judged. A payload that arrives truncated reads exactly like an empty one, which is why this is blocked rather than cleared. Retry the call."}}`;

final DENY_HEAD = `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}`;
final PASS_AND_ADVISE_TAIL = `{{else if eq .decision "advise"}}{{else if eq .decision "pass"}}`;


// What magus's own decoder tests to tell an envelope from a bare path. `\{` escapes the
// interpolation a lone brace would open.
final ENVELOPE_PREFIX = "\{";


object Guard {
    bin: str,
    payload: str,
    response: str,
    // The event's cwd, for the reason magus-command.buzz's Guard gives.
    dir: str,
}

fun judge(guard: Guard, extra: [str]) > proc\ExecResult !> any {
    final args = mut ["shell", "--path"];
    foreach (arg in extra) { args.append(arg); }
    args.append("-o");
    args.append("template={guard.response}");
    return proc\exec(guard.bin, args: args, dir: guard.dir, opts: {
        "quiet": true,
        "allow_failure": true,
        "stdin": guard.payload,
    });
}

fun adviseBranch() > str {
    final suppressed = env\get("__MAGUS_NO_ADVISE") catch "";
    if (suppressed != "") { return ""; }
    return hook\envOr("HOST_ADVISE_BRANCH", fallback: ADVISE_DEFAULT);
}

// warn names the file: a host reports a hook's stderr with the entry's matcher at best
// and nothing at all at worst, and a reader with several entries needs to know which.
fun warn(message: str) > void {
    io\stderr.write("magus-path.buzz: {message}\n") catch void;
}

// The host this entry is wired into; see AGENT_NAME_FLAG in magus-command.buzz.
final AGENT_NAME_FLAG = "--agent-name";

// The `magus shell` flags an entry may declare, as in magus-command.buzz's SUPPORTED_FLAGS:
// a config that also matches its host's skill tool says so, and a rule that wants a skill
// read before a write can then hold the write.
final SUPPORTED_FLAGS = ["--reports-skills"];

// EntryArgs is what this script's argv says: the host, and the supported flags it named.
object EntryArgs {
    agentName: str,
    flags: [str],
}

// readEntryArgs reads the host and the supported flags off this script's argv, and reports
// every other argument. An empty host is not defaulted: `magus shell` refuses installed
// glue that names no host (MGS3024).
//
// An argument that arrives here means the wiring meant something by it, and reading none
// at all accepted that in silence. Reported and then ignored, which is the same policy
// every other arm of this file takes: a misconfigured entry must not block work, and must
// not be invisible either.
fun readEntryArgs(args: [str]) > EntryArgs {
    final parsed = flags\parse(args, switches: SUPPORTED_FLAGS, valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) { return EntryArgs{ agentName = "", flags = [<str>] }; }
    foreach (word in parsed!.unknown) {
        warn("unsupported argument \"{word}\"; this hook accepts {AGENT_NAME_FLAG} and {SUPPORTED_FLAGS.join(", ")}. "
            + "Fix the hook command in your host config.");
    }
    final named = mut [<str>];
    foreach (name in SUPPORTED_FLAGS) {
        if (parsed!.values[name] != null) { named.append(name); }
    }
    return EntryArgs{ agentName = parsed!.values[AGENT_NAME_FLAG] ?? "", flags = named };
}

fun main(args: [str]) > void {
    final entry = readEntryArgs(args);
    final agentName = entry.agentName;
    final eventPath = hook\envOr("HOST_EVENT_PATH", fallback: "tool_input.file_path");
    final sessionPath = hook\envOr("HOST_SESSION_PATH", fallback: "session_id");
    final agentPath = hook\envOr("HOST_AGENT_PATH", fallback: "agent_id");
    final transcriptPath = hook\envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path");

    // The event is read first, as magus-command.buzz reads it: the binary is the one in the
    // checkout the event's cwd names.
    final raw = io\stdin.readAll() catch null;
    final event = json\parse(raw ?? "") catch null;

    final cwd = hook\field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd"));
    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) {
        // Prints nothing by default: for most hosts an empty response means "allow".
        // Set __MAGUS_UNAVAILABLE_RESPONSE for a host that needs an explicit verdict.
        final unavailable = env\get("__MAGUS_UNAVAILABLE_RESPONSE") catch "";
        if (unavailable != "") { io\stdout.write(unavailable) catch void; }
        return;
    }

    // A payload that opens like an envelope but does not parse is a TRUNCATED one, not a
    // path. Judged as text it matches no rule and passes; see magus-command.buzz.
    if (raw == null or (event == null and raw!.startsWith(ENVELOPE_PREFIX))) {
        io\stdout.write(hook\envOr("__MAGUS_UNREADABLE_RESPONSE", fallback: UNREADABLE_DEFAULT)) catch void;
        return;
    }
    final session = hook\field(event, dotPath: sessionPath);
    // Forwarded for the reason magus-command.buzz forwards it: a selected path carries no
    // subagent id of its own.
    final agent = hook\field(event, dotPath: agentPath);
    final transcript = hook\field(event, dotPath: transcriptPath);

    // Codex is the host the entry names, exactly as magus-command.buzz decides it. No
    // Codex rule prompts for a write, so there an ask renders as a deny.
    final isCodex = agentName == "codex";
    var askBranch = env\get("HOST_ASK_BRANCH") catch "";
    if (askBranch == "") {
        if (isCodex) { askBranch = ASK_CODEX; } else { askBranch = ASK_CLAUDE; }
    }

    // Only a reply assembled here claims --renders-ask: a HOST_RESPONSE the reader wrote
    // gets a deny from magus rather than an ask it may render as nothing.
    var response = env\get("HOST_RESPONSE") catch "";
    var rendersAsk = [<str>];
    if (response == "") {
        response = DENY_HEAD + askBranch + adviseBranch() + PASS_AND_ADVISE_TAIL + UNKNOWN_DECISION;
        rendersAsk = ["--renders-ask"];
    }

    var payload = raw!;
    final toolPath = hook\envOr("HOST_TOOL_PATH", fallback: "tool_name");
    if (!hook\wholeEvent(event, dotPath: eventPath, toolPath: toolPath)) {
        payload = hook\rawField(event, dotPath: eventPath) + "\n";
    }
    final guard = Guard{ bin = bin, payload = payload, response = response, dir = cwd };

    // Attribution is BEST EFFORT; the verdict is not. --agent-name, --session and --agent postdate
    // the current magus release, and an older binary rejects the unknown flag outright,
    // printing usage to stdout and exiting non-zero, which leaves the host with no verdict
    // rather than an unattributed one. Try with attribution, fall back to the call this
    // script made before it existed.
    //
    // The retry tests status AND emptiness together, for the same reason as the command
    // template now that a file-write rule can deny: a DENY exits non-zero (2) with the verdict
    // on stdout, so retrying on status alone would judge every blocked write twice.
    //
    // The entry's own flags ride the attributed call only, as in magus-command.buzz: a
    // binary too old for one cannot report a skill load either.
    final attributed = mut [<str>];
    foreach (flag in entry.flags) { attributed.append(flag); }
    foreach (word in ["--agent-name", agentName, "--transport", "buzz", "--session", session, "--agent", agent, "--transcript", transcript]) {
        attributed.append(word);
    }
    foreach (flag in rendersAsk) { attributed.append(flag); }
    var result = judge(guard, extra: attributed) catch null;
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        result = judge(guard, extra: [<str>]) catch null;
    }

    // A pass and a broken guard both render nothing; see magus-command.buzz for why
    // telling them apart matters. Kept identical here so neither template grows a behavior
    // the other lacks. The difference is only that this one has no default message,
    // because for most hosts an empty response to a file-write hook already means "allow".
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        final failed = env\get("__MAGUS_FAILED_RESPONSE") catch "";
        if (failed != "") { io\stdout.write(failed) catch void; }
        return;
    }
    io\stdout.write(hook\trimTrailingNewlines(result!.stdout)) catch void;
}
```

## `magus-observe.buzz`

The one template that carries no verdict. Wire it to the tools that only LOOK
(your host's read equivalent), and it records the path the agent reached without
judging it. It prints nothing and always exits 0.

A host that wants its reads judged too wires `magus-command.buzz` beside it, which
restates the read as a shell line. Do not point a read tool at `magus-path.buzz`. A
read event carries a file path just as a write event does, so the write rules would
advise "you are editing a declared output" at a file the agent merely opened.
`--observe` is what separates the two, and only this wrapper can set it, because
only it knows which of your host's tools look.

It declares no `magus-guard-coverage` line, and that absence is deliberate: a
coverage declaration states how much of a verdict a host can carry on a guard
input, and this file carries no verdict on any input.

```buzz
// magus observe hook: records ONE path an agent reached, and judges nothing.
//
// Run it as `magus buzz -s magus-observe.buzz`; see magus-command.buzz for why `-s` is
// load-bearing. It imports no magus Buzz
// module and no spell: a read hook fires on every file an agent opens, so the
// 10ms start a closed workspace buys is worth more here than anywhere else.
//
// Wire it to the tools that only LOOK, your host's read equivalent. The guard
// templates beside this one handle the tools that ACT; magus-command.buzz also judges
// a read, restated as the shell read it stands for. Do not point a read tool at
// magus-path.buzz: a read event carries a file path, so the write rules would advise
// "you are editing a declared output" at a file the agent merely opened.
// --observe is what separates the two, and only this wrapper can set it,
// because only it knows which of your host's tools look.
//
// Contract: reads the host's event as JSON on stdin, selects the path, and feeds
// it to `magus shell --observe`. It prints NOTHING and always exits 0; see the
// note on that below, which is load-bearing rather than tidy. Override any of
// the variables below:
//
//   HOST_EVENT_PATH  dot-path to the read path inside your host's event
//   HOST_SESSION_PATH  dot-path to the session id inside your host's event
//   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
//   HOST_CWD_PATH  dot-path to the directory the call runs in
//   __MAGUS_BIN  path to the binary, when it is not on PATH
//
// REQUIRED argument: `-- --agent-name <host>`, the host recorded alongside the
// observation, which the configuration `magus describe harness` prints puts on the
// command. Without it nothing is recorded and MGS3024 goes to stderr.
//
// The defaults are Claude Code's event shape, matching its two siblings. A
// different host overrides the dot-paths.
//
// NO magus-guard-coverage line, and that absence is deliberate rather than an
// oversight: a coverage declaration states how much of a VERDICT a host can
// carry on a guard input, and this file carries no verdict on any input. It
// never denies, never advises, and cannot change what your host does next. The
// parity gates ask that question only of artifacts that answer it.
//
// magus-guard-template: 21

// EVERY call that can fail is caught, deliberately.
//
// An unparsable event, a missing binary, a flag the binary does not know: each one
// must end as silence and exit 0. A PreToolUse hook's exit status is not advisory on
// every host. Claude Code reads exit 2 as "block this tool call" and takes the message
// from stderr, so an uncaught error here could stop the agent from reading anything at
// all. An optional record must never be able to do that.

import "io";
import "flags";
import "encoding/json";
import "proc";
import "lib/hook" as hook;


// field reads an absent value as the empty string rather than the literal "null".
// Only a STRING is a path. hook\field renders an object or a list as JSON, and that recorded the
// serialized blob as the file the agent reached, which the rules reading those records treat
// as a shape they cannot read; nothing to record beats recording something untrue.
fun field(event: any?, dotPath: str) > str {
    final value = hook\dig(event, dotPath: dotPath);
    if (value == null) { return ""; }
    return (value as? str) ?? "";
}


// warn names the file; see magus-command.buzz. STDERR, which is why it does not break
// this file's contract to print nothing: stdout is what the host reads as a verdict.
fun warn(message: str) > void {
    io\stderr.write("magus-observe.buzz: {message}\n") catch void;
}

// The host this entry is wired into, the one argument this hook takes; see
// AGENT_NAME_FLAG in magus-command.buzz.
final AGENT_NAME_FLAG = "--agent-name";

// readAgentName reads the host off this script's argv, "" when the entry names none, and
// reports every other argument: an argument arriving here means the wiring meant
// something by it, and reading none at all accepted that silently.
fun readAgentName(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) { return ""; }
    foreach (word in parsed!.unknown) {
        warn("unsupported argument \"{word}\"; this hook accepts only {AGENT_NAME_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    return parsed!.values[AGENT_NAME_FLAG] ?? "";
}

fun main(args: [str]) > void {
    // No host, no record, and a coded line on stderr rather than a guess: an observation
    // filed under the wrong host is wrong, and a silent gap is invisible.
    final agentName = readAgentName(args);
    if (agentName == "") {
        warn("[MGS3024] this hook was not given --agent-name, so nothing was recorded. "
            + "Merge what `magus describe harness` prints into the host's hook configuration; "
            + "the commands it prints name the host.");
        return;
    }
    final eventPath = hook\envOr("HOST_EVENT_PATH", fallback: "tool_input.file_path");
    final sessionPath = hook\envOr("HOST_SESSION_PATH", fallback: "session_id");
    final transcriptPath = hook\envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path");

    // An absent observer is SILENT, where an absent guard is loud.
    //
    // The guard templates announce themselves when magus cannot be found, because an
    // unenforced deny rule is a safety fact the reader needs. Nothing is unenforced
    // here, there is no rule, so the same announcement would be a per-read interruption
    // reporting that an optional record was not written.
    final raw = io\stdin.readAll() catch "";
    final event = json\parse(raw) catch null;
    // The event's cwd, for the reason magus-command.buzz's Guard gives.
    final cwd = field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd"));
    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) { return; }

    // The path is extracted rather than forwarding the whole event, because a payload
    // magus does not recognize as an envelope is judged as the literal text it is, and
    // for a search that carries a pattern but no path that would record the entire
    // event, query text included, as the thing the agent reached.
    //
    // Nothing to record is not a failure: a host event that names no path has no reach
    // to report, and inventing one would claim a file the host never named.
    final reached = field(event, dotPath: eventPath);
    if (reached == "") { return; }

    // A magus too old for --observe rejects the flag and exits non-zero. That is a real
    // state worth knowing about once, but not once per read, so it is reported through
    // the trail's own absence rather than through the session: if reads are missing from
    // `magus session`, the binary is too old.
    proc\exec(bin, args: [
        "shell", "--observe",
        "--agent-name", agentName,
        "--session", field(event, dotPath: sessionPath),
        "--transcript", field(event, dotPath: transcriptPath),
        "--event", "PreToolUse",
    ], dir: cwd, opts: {"quiet": true, "allow_failure": true, "stdin": reached}) catch void;
}
```

## What a template must not get wrong

Three failure modes are worth naming, because each one looks like a working
guard.

**A missing arm renders empty.** `HOST_RESPONSE` carries both a deny arm and an
advise arm. A template missing one does not fail loudly; it renders nothing, and
every host reads nothing as allow. Claude Code's `--path` wiring once rendered
only the deny arm, so every advisory it produced was silently dropped, while the
shipped path template had the opposite gap and dropped denials.

**A pass and a broken guard both render nothing.** A pass exits 0 with empty
output because there was nothing to say. A binary that cannot run (too old for
`session hook`, unable to load the workspace, half-written by a concurrent build) exits
non-zero with empty output. Printing that as a pass disables every rule with
nothing anywhere saying so. Both templates discriminate on status and emptiness
together, and announce the second case. The announcement names its evidence
(the binary path they resolved, that binary's version, and the first line it
printed on stderr) because the guesses it used to offer sent readers to check a
workspace that was never the problem. Set `__MAGUS_FAILED_RESPONSE` to replace it
with a fixed response of your own.

**Attribution must never break a verdict.** `--agent-name` and `--session`
postdate the current release, and an older binary rejects an unknown flag by
printing usage and exiting non-zero, which leaves the host with no verdict at
all. Both templates try with attribution and retry without it, and they retry only
when the call produced no verdict, never merely because it exited non-zero,
since a deny exits 2 with the verdict on stdout.

## `magus-checkpoint.buzz`

The second template that carries no verdict. Wire it to your host's stop or
session-end event and it records where the work stands: the revision, branch and
dirtiness of the tree, plus your host's session id and transcript path as opaque
pointers. `magus session` lists what it wrote.

It is worth wiring for the case nobody plans for. A session that ends because it
finished leaves a commit; one that ends because a usage limit was reached, or
because the laptop closed, leaves the work exactly where it was and nothing
saying so. Recovering one such stop was measured at a session id passed by hand,
a guessed transcript location, and three failed commands before it emerged the
commits had never been pushed.

It records the same position [`magus vcs checkpoint`](../../../reference/manpage/magus-vcs.md)
computes and prints, and keeps it. What makes one worth keeping is the note, so
`magus session checkpoint --note "..."` is the form a person runs; the hook
records the position and the pointers, and leaves the prose alone. Nothing in the
host's payload becomes the note.

It forwards the event whole and magus parses the envelope itself; the one field
it reads is `cwd`, the tree it records. A host that spells those fields differently
passes `--session` and `--transcript` instead, and one that can supply neither
still records a usable checkpoint, because the part that matters is read from the
tree.

It declares no `magus-guard-coverage` line, for the reason
`magus-observe.buzz` declares none: it carries no verdict on any input.

```buzz
// magus checkpoint hook: records where the work stands when a session stops.
//
// Run it as `magus buzz -s magus-checkpoint.buzz`; see magus-command.buzz for why `-s`
// is load-bearing. It imports no magus Buzz module
// and no spell, which is what keeps the workspace CLOSED: a Stop hook fires once
// per session rather than once per tool call, so the 700ms an open costs would
// be affordable here; it is still refused, because the rule that keeps the glue
// closed is worth more than the one exception that would erode it.
//
// Wire it to your host's stop or session-end event. It records the revision,
// branch and dirtiness of the tree, plus your host's session id and transcript
// path as opaque pointers, so that whoever comes back to this repository (you
// tomorrow, or another session) reads `magus session` instead of reconstructing
// where the work stopped. That reconstruction is the cost this exists to remove:
// it was measured at a session id passed by hand, a guessed transcript location,
// and three failed commands before it emerged the work had never been pushed.
//
// Contract: pipes your host's event, unread, into `magus session checkpoint`.
// magus takes the two pointers only a host knows out of the envelope and ignores
// the rest; nothing in the payload becomes the note, because a note is a sentence
// a person writes. It prints NOTHING and always exits 0. Override:
//
//   __MAGUS_BIN   path to the binary, when it is not on PATH
//
// REQUIRED argument: `-- --agent-name <host>`, the host recorded alongside the
// checkpoint, which the configuration `magus describe harness` prints puts on the
// command. Without it nothing is recorded and MGS3024 goes to stderr.
//
// A host whose envelope spells those fields differently passes them as flags
// instead, since `--session` and `--transcript` outrank the envelope, and a host that
// cannot supply either still records a usable checkpoint, because the part that
// matters is read from the tree rather than from the event.
//
// The event is forwarded whole: magus parses the envelope itself. The one field read
// here is cwd, the tree the record describes.
//
// NO magus-guard-coverage line, for the same reason magus-observe.buzz has
// none: a coverage declaration states how much of a VERDICT a host can carry, and
// this file carries no verdict on any input. It never denies, never advises, and
// cannot change what your host does next.
//
// magus-guard-template: 21

// EVERY call that can fail is caught, matching the templates beside it. A hook that can fail is a hook that can break
// the session it was meant to observe, and a record of where the work stopped is
// worth strictly less than the work.

import "io";
import "flags";
import "proc";
import "encoding/json";
import "lib/hook" as hook;

// warn names the file; see magus-command.buzz. STDERR, which is why it does not break
// this file's contract to print nothing: stdout is what the host reads as a verdict.
fun warn(message: str) > void {
    io\stderr.write("magus-checkpoint.buzz: {message}\n") catch void;
}

// The host this entry is wired into, the one argument this hook takes; see
// AGENT_NAME_FLAG in magus-command.buzz.
final AGENT_NAME_FLAG = "--agent-name";

// readAgentName reads the host off this script's argv, "" when the entry names none, and
// reports every other argument: an argument arriving here means the wiring meant
// something by it, and reading none at all accepted that silently.
fun readAgentName(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) { return ""; }
    foreach (word in parsed!.unknown) {
        warn("unsupported argument \"{word}\"; this hook accepts only {AGENT_NAME_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    return parsed!.values[AGENT_NAME_FLAG] ?? "";
}

fun main(args: [str]) > void {
    // No host, no record, and a coded line on stderr rather than a guess.
    final agentName = readAgentName(args);
    if (agentName == "") {
        warn("[MGS3024] this hook was not given --agent-name, so nothing was recorded. "
            + "Merge what `magus describe harness` prints into the host's hook configuration; "
            + "the commands it prints name the host.");
        return;
    }

    final event = io\stdin.readAll() catch "";
    // The event's cwd names the tree whose state is recorded, for the reason
    // magus-command.buzz's Guard gives.
    final cwd = hook\field(json\parse(event) catch null, dotPath: "cwd");

    // An absent recorder is SILENT, where an absent guard is loud. Nothing here is
    // unenforced, because there is no rule, so announcing it would interrupt the end of
    // every session to report that an optional record was not written.
    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) { return; }

    // Both streams are discarded by `quiet`: a magus too old for `session checkpoint`
    // prints its usage, and that would otherwise reach the host as this hook's response
    // every time a session ends. The absence shows up where it is actionable instead -
    // as an empty checkpoint list in `magus session`.
    proc\exec(bin, args: [
        "session", "checkpoint",
        "--agent-name", agentName,
    ], dir: cwd, opts: {"quiet": true, "allow_failure": true, "stdin": event}) catch void;
}
```

## `magus-rehydrate.buzz`

The third template that carries no verdict. Wire it to your host's session-start
event, at least for the compaction and resume cases, and every time a host
replaces a session's history with a summary the model is handed this checkout's
state instead of a retelling: branch and revision, commits not yet on the base
ref, the dirty tree split into sources, generated outputs and paths nothing
claims, the live leases with the command that binds each one, the last recorded
run's failures with the ref that holds their output, whether anything here runs
the guard, and where the rules live.

Every line of it is read off the disk at the moment it prints, which is the
property that makes it worth wiring. A summary degrades with each retelling and
nothing in the transcript says by how much; a fact read from the tree cannot
degrade at all. Nothing in it is remembered between sessions. The one thing it
takes from the host's event is `cwd`, which names the checkout to brief.

It restates no rule either. `magus session --brief` names the files this
workspace's rules live in (AGENTS.md and the installed skill directories, when
they exist), and the template adds one line for your host's own instruction file,
`--rules <file>`, which defaults to `CLAUDE.md` and prints only when that file
is really there. A rule copied into a hook's output is a second copy to go stale,
and the model can read the first.

Run `magus session --brief` yourself to see exactly what a session will be
handed; `-o json` is the same brief for a wrapper that wants to reshape it.

Hosts disagree about what a session-start hook's stdout IS, so this template has
two channels. Plain text is the default, for a host that adds stdout to the
model's context. `--format json` wraps the same text in
`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":...}}`
for a host that parses stdout as a reply and drops anything that is not one.

It declares no `magus-guard-coverage` line, for the reason
`magus-observe.buzz` declares none: it carries no verdict on any input.

```buzz
// magus rehydrate hook: prints where this checkout stands, for a session that has
// lost its history.
//
// Run it as `magus buzz -s magus-rehydrate.buzz`; see magus-command.buzz for why `-s`
// is load-bearing. It imports no magus Buzz module and
// no spell: the workspace stays CLOSED, which is what lets a session-start hook add
// its block in about 10ms rather than paying roughly 700ms to open one.
//
// Wire it to your host's session-start event, for the compaction and resume cases
// at least. When a host replaces a long session's history with a summary, the model
// keeps working from prose: the branch it is on, what it has already changed, and
// which rules it agreed to all survive only as somebody's retelling, and each
// retelling is a copy of a copy. Whatever this prints lands in that context window
// instead, read off the disk at the moment it prints.
//
// Contract: runs `magus session --brief`, prints what it says, and adds one line
// naming your host's own instruction file, for the checkout its event's cwd names. It
// judges nothing and exits 0 whatever happens. Arguments, after `--`:
//
//   --rules <file>   your host's instruction file, relative to the workspace root;
//                    CLAUDE.md when omitted
//   --format json    for a host that reads stdout as a reply rather than as context
//
// and one variable:
//
//   __MAGUS_BIN   path to the binary, when it is not on PATH
//
// Two channels, because hosts disagree about what a session-start hook's stdout
// IS. Some add plain stdout to the model's context, which is the default here.
// Others parse stdout as a JSON reply and drop anything that is not one, so the
// same text has to arrive as a string field:
//
//   {"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"..."}}
//
// The rules line is the one host-shaped part, which is why the entry names it rather
// than magus printing it: magus names the files it ships and can see (AGENTS.md, the
// installed skill directories), and the file YOUR host reads is yours to name. It
// prints only when that file is really there.
//
// NO magus-guard-coverage line, for the same reason magus-checkpoint.buzz has none: a
// coverage declaration states how much of a VERDICT a host can carry, and this file
// carries no verdict on any input. It never denies, never advises, and cannot
// change what your host does next.
//
// magus-guard-template: 21

// EVERY call that can fail is caught, matching the templates beside it. A hook that
// can fail is a hook that can break the session it was meant to help.

import "io";
import "flags";
import "fs";
import "encoding/json";
import "proc";
import "lib/hook" as hook;

final RULES_FLAG = "--rules";
final FORMAT_FLAG = "--format";

// warn names the file; see magus-command.buzz. STDERR, so it never joins the reply this
// file writes on stdout.
fun warn(message: str) > void {
    io\stderr.write("magus-rehydrate.buzz: {message}\n") catch void;
}

// parseOptions reads the two flags and reports every other argument: an argument arriving
// here means the wiring meant something by it, and reading none at all accepted that
// silently. An unreadable argv falls back to the defaults rather than to no brief.
fun parseOptions(args: [str]) > {str: str} {
    final parsed = flags\parse(args, switches: [<str>], valued: [RULES_FLAG, FORMAT_FLAG]) catch null;
    if (parsed == null) {
        warn("could not read the arguments {args}; using the defaults. Fix the hook command in your host config.");
        return {<str: str>};
    }
    foreach (word in parsed!.unknown) {
        warn("unsupported argument \"{word}\"; this hook accepts {RULES_FLAG} and {FORMAT_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    return parsed!.values;
}

fun main(args: [str]) > void {
    final options = parseOptions(args);
    final rules = options[RULES_FLAG] ?? "CLAUDE.md";
    final format = options[FORMAT_FLAG] ?? "";
    if (format != "" and format != "json") {
        warn("unsupported {FORMAT_FLAG} \"{format}\"; the one format is json. Printing plain text.");
    }
    // The event's cwd names the checkout to brief, for the reason magus-command.buzz's
    // Guard gives. The working directory stands in when the event names none.
    final cwd = hook\field(json\parse(io\stdin.readAll() catch "") catch null, dotPath: "cwd");
    var root = "";
    if (cwd != "") { root = hook\workspaceRootAt(cwd); }
    if (root == "") { root = hook\workspaceRoot(); }

    // An absent magus is SILENT, where an absent guard is loud. Nothing here is
    // unenforced (there is no rule), so announcing it would open every compacted
    // session with a report that an optional context block was not written.
    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) { return; }

    // Captured rather than streamed, because the json arm has to wrap it. stderr is
    // discarded by `quiet`: a magus too old for `session --brief` prints its usage
    // there, and that would otherwise be injected as this hook's answer.
    final result = proc\exec(bin, args: ["session", "--brief"], dir: cwd, opts: {
        "quiet": true,
        "allow_failure": true,
    }) catch null;
    if (result == null) { return; }

    var brief = hook\trimTrailingNewlines(result!.stdout);

    // Nothing from magus is nothing to say, in either channel. The rules line trails
    // the brief and points back at it, so on its own it is a sentence about a block
    // that was never written, and an envelope carrying only that is worse than none.
    if (brief == "") { return; }

    if (root != "") {
        final hasRules = fs\isFile("{root}/{rules}") catch false;
        if (hasRules) {
            brief = brief + "\nstanding rules: " + rules + "; re-read it, the summary above is not it";
        }
    }

    if (format == "json") {
        final context = json\stringify(brief + "\n") catch "";
        if (context == "") { return; }
        io\stdout.write(`\{"hookSpecificOutput":\{"hookEventName":"SessionStart","additionalContext":`
            + context + "}}") catch void;
        return;
    }

    io\stdout.write(brief + "\n") catch void;
}
```

## `magus-session.buzz`

The fourth template that carries no verdict, and the one that runs no magus. Wire
it to your host's session-start event as
`magus buzz -C <root> -s magus-session.buzz -- --env-file <file>`, where
`<file>` is the one your host sources before each shell command, and it appends
`export PATH="<root>:$PATH"` there. A `magus` the agent types then runs the build
the hooks run, rather than whatever the shell that launched the host had.

The directory `-C` names is what goes on PATH, so the entry states it and the
template guesses nothing. An empty `--env-file` does nothing, which is what a
host without such a file passes.

It declares no `magus-guard-coverage` line: it carries no verdict on any input.

```buzz
import "io";
import "flags";
import "fs";
import "path";

// magus session hook: puts the workspace's own magus first on PATH for the shell
// commands a session runs, so a `magus` typed there is the build its hooks run.
//
// Wire it to your host's session-start event as `magus buzz -C <root> -s
// magus-session.buzz -- --env-file <file>`. The directory -C names is the one that goes
// on PATH, and <file> is the one your host sources before each shell command. It
// appends `export PATH="<root>:$PATH"` there, keeping what other hooks wrote, and
// leaves `$PATH` for that shell to expand. An empty or absent --env-file does nothing:
// a host that sources no such file has no PATH to set. It prints nothing on stdout and
// exits 0.
//
// NO magus-guard-coverage line: it judges nothing on any input.
//
// magus-guard-template: 21

final ENV_FILE_FLAG = "--env-file";

fun warn(message: str) > void {
    io\stderr.write("magus-session.buzz: {message}\n") catch void;
}

fun envFilePath(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [ENV_FILE_FLAG]) catch null;
    if (parsed == null) {
        warn("could not read the arguments {args}; PATH is unchanged. Fix the hook command in your host config.");
        return "";
    }
    foreach (word in parsed!.unknown) {
        warn("unsupported argument \"{word}\"; this hook accepts only {ENV_FILE_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    return parsed!.values[ENV_FILE_FLAG] ?? "";
}

fun main(args: [str]) > void {
    final envFile = envFilePath(args);
    if (envFile == "") { return; }
    final root = path\abs(".") catch "";
    if (root == "") { return; }
    try {
        fs\appendFile(envFile, content: "export PATH=\"{root}:$PATH\"\n");
    } catch {
        warn("could not append to {envFile}; the session's commands find magus on PATH as it was.");
    }
}
```

## Trying a verdict by hand

```sh
printf '%s' 'git stash' | magus session hook -o name
printf '%s' 'MAGUS.md' | magus session hook --path -o name
magus session hook -o template
```

The last one lists the fields available to `-o template`. A deny exits 2 with
the verdict on stdout; a pass and an advise exit 0. See [The guard](guard.md)
for the rules behind the verdicts.
