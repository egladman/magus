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
splits it and runs `magus buzz` itself), so the host's name rides on that argv, after
`--`, and is the one argument an entry takes:

- `--agent-name <host>`, on every command. The template reads the host from there
  and nowhere else: not from the event's shape, not from the environment, never by
  default. The configuration `magus describe harness` prints renders it from the
  harness spell's own name. A template given none refuses the call
  ([MGS3024](../../../reference/codes/sandbox/MGS3024.md)) rather than answer in a
  guessed host's dialect, because a reply shaped for the wrong host can let a call
  through. magus reads every other fact about the host from its harness spell, as
  install wrote them beside the host's tool rows. Any other argument is named on stderr and
  left out, and the call is judged anyway; your host shows that line as a hook error.
  Nothing is dropped quietly, and a guard does not refuse to answer because an entry was
  typed wrong.

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
// magus guard hook: judges ONE tool call an agent is about to make, whatever the tool.
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
// Contract: reads the host's event as JSON on stdin and feeds the whole event to
// `magus shell`, which judges the call by its tool's name and the shape of its
// input: a command line, a file read or write, a search, a spawn. It writes the
// host's response on stdout and exits 0 either way.
//
// A hook command is an ARGV, not a shell line: the host splits it and runs the
// program itself, so a `NAME=value` prefix only means anything where something
// re-joins and re-parses the string. So this file is wired as a plain
// `<interpreter> buzz -s <this file> -- --agent-name <host>` and takes one argument:
//
//   -- --agent-name <host>  REQUIRED: the host this entry is wired into, the harness
//                    spell's own name, which the configuration `magus describe
//                    harness` prints puts on every command. It is the ONLY place this file
//                    learns the host: never the event's shape, never the environment,
//                    never a default. Without it `magus shell` refuses the call
//                    (MGS3024), so a hand-written config that omits it fails loudly.
//                    magus reads every other fact about the host from that spell: its
//                    tool rows, its foreground limits, the release that resumes a stopped
//                    subagent, and whether its wiring sees a skill load. Any other
//                    argument is REPORTED on stderr and left out, and the call is judged
//                    anyway: a misconfigured entry must not block work, and must not be
//                    silent
//
// Everything else is an environment variable, a property of the HOST that it sets once.
//
// Override any of the variables below:
//
//   HOST_EVENT_PATH  dot-path to the command inside your host's event, for a host whose
//                    envelope magus does not read: that one string is forwarded instead
//                    of the whole event. Unset, the whole event goes, which is what lets
//                    magus judge every tool and not only a shell
//   HOST_TOOL_PATH   dot-path to the tool name, which keeps an MCP call whole even where
//                    HOST_EVENT_PATH selects
//   HOST_SESSION_PATH  dot-path to the session id inside your host's event
//   HOST_AGENT_PATH  dot-path to the subagent id inside your host's event, so a
//                    subagent's command is graded under the job it was spawned for
//                    rather than as its parent
//   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
//   HOST_MESSAGE_PATH  dot-path to the text of a message the person typed (default
//                    prompt, the key Claude Code's envelope uses). An event that names no
//                    tool and carries a string there is a message rather than a call: it
//                    goes to `magus shell --message`, which records the
//                    topics it raises. Its only reply is one systemMessage
//                    per session when no magus runs
//   HOST_CWD_PATH    dot-path to the directory the call runs in. The ./magus beside the
//                    nearest magus.yaml above it judges the call, so a call from a
//                    subdirectory with its own magusfile.buzz (console/, docs/) still
//                    uses the root's build (default cwd)
//   HOST_RESPONSE    Go template rendering your host's reply
//   HOST_ADVISE_BRANCH  the advise arm of that template
//   HOST_ASK_BRANCH  the ask arm of that template: the reply that puts the call in
//                    front of the PERSON through the host's own approval prompt
//   __MAGUS_NO_ADVISE  set it when the host has no context-injection channel, so
//                    an advise renders nothing rather than a reply it rejects
//   __MAGUS_BIN  path to the binary, when it is not on PATH
//   __MAGUS_UNAVAILABLE_RESPONSE  what to print when magus cannot be found,
//                    in place of the refusal this file builds
//   __MAGUS_FAILED_RESPONSE  the same, for a magus that IS found but cannot judge
//                    the command. Left unset, this file builds one from evidence:
//                    which binary it resolved, that binary's version, and the
//                    error it actually printed
//   __MAGUS_UNREADABLE_RESPONSE  what to print when the call never arrived
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
// A call no magus judged fails CLOSED. With magus missing, or unable to
// answer, every PreToolUse call but a read is refused. Each such call also
// tells the person how to install or build magus, through systemMessage.
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
// (.codex/rules/magus.rules, a prefix_rule with decision "prompt" on each push verb and
// on each run of a human_only target), and the PermissionRequest event Codex raises
// before that prompt reaches this same file, which answers allow for a call the guard
// passes, leaves an ask to the person, and denies a leased worker's.
//
// An ask is the host's own prompt, never the model's question tool.
// Where no prompt can happen it renders as a deny, never an allow.
// promptBlocker names those cells. systemMessage tells the person why
// and what to type at their terminal.
// magus-guard-template: 36
// magus-guard-coverage: schema=2 host=claude-code input=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=codex input=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=claude-code input=mcp deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=codex input=mcp deny=model advise=model pass=none ask=model
// magus-guard-coverage: schema=2 host=claude-code input=path deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=codex input=path deny=model advise=model pass=none ask=model
// The mcp and path rows are real: every host wires this file to every tool, so an
// mcp__magus__* call and a file write reach it as whole events (see hook\wholeEvent), and
// the same hookSpecificOutput reply it renders for a command carries a deny or an advise on
// those too. No Codex rule prompts for an MCP call or a write, so there the ask is the deny
// that names the terminal.

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
// So a template is spliced through placeholders rather than
// concatenated around an open `{{`, which would swallow its closing
// backtick. Each takes a quoted literal: the text can quote a command.
final PERSON_SLOT = "__MAGUS_ASK_PERSON__";
final AGENT_SLOT = "__MAGUS_ASK_AGENT__";
final ASK_BLOCKED = `{{else if eq .decision "ask"}}{"systemMessage":{{toJson (print .reason __MAGUS_ASK_PERSON__)}},"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print .reason __MAGUS_ASK_AGENT__)}}}}`;

// A decision this file does not know is refused, never allowed: the guard contract
// grows, and a copy older than the growth must not read the new verdict as a pass.
final UNKNOWN_DECISION = `{{else}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}{{end}}`;

// A deny's first line is the verdict, for the display both readers see; the rest (next step,
// details ref) goes to additionalContext, the capture only the agent reads. A host whose
// harness declares no capture channel is sent the verdict line alone by `magus shell`.
final DENY_HEAD = `{{if eq .decision "deny"}}{{$r := splitList "\n" .reason}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (first $r)}}{{with join (rest $r) "\n"}},"additionalContext":{{toJson .}}{{end}}}}`;
final PASS_AND_ADVISE_TAIL = `{{else if eq .decision "advise"}}{{else if eq .decision "pass"}}`;

// A block's reason sends the agent back to work and shows the person, so it carries the
// first line and additionalContext the rest. A systemMessage shows the person a notice and
// lets the stop through. Spliced through STOP_EVENT_SLOT: Stop and SubagentStop share it.
final STOP_EVENT_SLOT = "__MAGUS_STOP_EVENT__";
final STOP_REPLY = `{{if eq .decision "deny"}}{{$r := splitList "\n" .reason}}{"decision":"block","reason":{{toJson (first $r)}}{{with join (rest $r) "\n"}},"hookSpecificOutput":{"hookEventName":"__MAGUS_STOP_EVENT__","additionalContext":{{toJson .}}}{{end}}}{{else if eq .decision "advise"}}{"systemMessage":{{toJson .context}}}{{end}}`;
// Codex's Stop reply takes no hookSpecificOutput, so its block carries the whole reason.
final STOP_REPLY_CODEX = `{{if eq .decision "deny"}}{"decision":"block","reason":{{toJson .reason}}}{{else if eq .decision "advise"}}{"systemMessage":{{toJson .context}}}{{end}}`;

final PERMISSION_NO_DECISION = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest"}}`;
final PERMISSION_ALLOW = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`;
final PERMISSION_DENY_HEAD = `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":{{toJson .reason}}}}}{{else if eq .decision "ask"}}`;
final PERMISSION_UNKNOWN = `{{else}}{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}}{{end}}`;

// The Claude Code tools its harness spell lists as reads. Copied: an unjudged
// call has no magus to ask, and a read may pass it.
final READ_TOOLS = ["Read", "NotebookRead", "Glob", "Grep", "LS"];

// Nothing arrived, so no rule was ever offered the call. `magus shell`
// denies an unreadable payload too and leaves this case to its caller.
final UNREADABLE_DEFAULT = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"magus guard could not read this call from the host, so nothing was judged. A payload that arrives truncated reads exactly like an empty one, which is why this is blocked rather than cleared. Retry the call."}}`;

// The characters that let one command line become several, or become a different
// one. A line carrying any of them reaches no Codex prefix rule.
final SHELL_METACHARACTERS = [";", "&", "|", "`", "$", "(", ")", "<", ">", "\\", "\n"];


// What magus's own decoder tests to tell an envelope from a bare command line. `\{` escapes
// the interpolation a lone brace would open.
final ENVELOPE_PREFIX = "\{";

// The host this entry is wired into, the one argument it takes. The file needs it too: it
// picks the ask arm. magus reads every other fact about the host from its harness spell.
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

// codexPromptRule names the Codex prefix rule a bare push or a bare magus run reaches, as
// the rules file spells its pattern with the spacing removed, or null for anything else:
// a compound line, `git -C dir push`, a flag before the target, an MCP call. Those reach
// no rule, so Codex would run them unprompted and no answer here may assume it prompts. A
// magus run reaches a rule only when the harness wrote one for its target, which it does
// for each target the workspace declares human_only.
fun codexPromptRule(line: str) > str? {
    foreach (meta in SHELL_METACHARACTERS) {
        if (line.indexOf(meta) != null) { return null; }
    }
    if (line == "git push" or line.startsWith("git push ")) { return `["git","push"]`; }
    if (line == "hg push" or line.startsWith("hg push ")) { return `["hg","push"]`; }
    if (line == "sl push" or line.startsWith("sl push ")) { return `["sl","push"]`; }
    if (line == "jj git push" or line.startsWith("jj git push ")) { return `["jj","git","push"]`; }
    final words = line.split(" ").filter(fun (index: int, word: str) > bool => word != "");
    if (words.len() >= 3 and (words[0] == "magus" or words[0] == "./magus")
            and (words[1] == "run" or words[1] == "affected") and !words[2].startsWith("-")) {
        return "[\"" + words[0] + "\",\"" + words[1] + "\",\"" + words[2] + "\"]";
    }
    return null;
}

// codexRuleMissing states why the project's rules file does not carry rule, and "" when
// it does.
fun codexRuleMissing(event: any?, rule: str) > str {
    final rules = findUp(".codex/rules/magus.rules", start: hook\field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd")));
    if (rules != "") {
        // A file that is THERE and unreadable is not the same as one that carries no
        // matching rule, and collapsing the two produced a deny whose stated reason was
        // false. Named separately so the person is told what to look at.
        final body = fs\readFile(rules) catch null;
        if (body == null) {
            return "{rules} exists but could not be read, so whether it carries the prompt rule for this call is unknown";
        }
        // Compared with the spacing removed, so the rule matches however it is
        // formatted. The leading bracket is what keeps the git rule from being found
        // inside jj's ["jj", "git", "push"].
        final packed = body!.replace(" ", with: "").replace("\t", with: "");
        if (packed.indexOf(rule) != null) { return ""; }
    }
    return "no .codex/rules/magus.rules carries the prompt rule for this call, which magus describe harness codex prints";
}

// An allowlist: a mode not seen to prompt must not read as one that does.
final PROMPTING_MODES = ["default", "acceptEdits", "auto"];

// promptBlocker states why the host will not put this call in front of
// the person, and "" when its own approval prompt will.
fun promptBlocker(event: any?, codex: bool, rule: str?) > str {
    final mode = hook\field(event, dotPath: "permission_mode");
    if (mode == "") {
        return "this session reports no permission_mode, so a prompt cannot be assumed";
    }
    if (mode == "bypassPermissions" or mode == "dontAsk") {
        return "this session runs in permission_mode {mode}, which never prompts";
    }
    if (PROMPTING_MODES.indexOf(mode) == null) {
        return "this session runs in permission_mode {mode}, which is not known to prompt";
    }
    if (!codex) { return ""; }
    if (rule == null) {
        return "no Codex approval rule matches this call, only a plain git push, hg push, sl push or jj git push command, or a plain magus run of a human-only target";
    }
    return codexRuleMissing(event, rule: rule!);
}

// callFlags is what a shell command's tool input asked of the host: to run it in the
// background, and how long to wait on it. The whole event carries both too; these say it
// where HOST_EVENT_PATH forwards the command alone. JSON numbers parse as double.
fun callFlags(event: any?) > [str] {
    final facts = mut [<str>];
    if (hook\dig(event, dotPath: "tool_input.run_in_background") == true) {
        facts.append("--call-background");
    }
    final timeout = hook\dig(event, dotPath: "tool_input.timeout");
    var ms = 0;
    if (timeout is int) { ms = timeout as int; }
    if (timeout is double) { ms = std\toInt(timeout as double); }
    if (ms > 0) {
        facts.append("--call-timeout");
        facts.append("{ms}ms");
    }
    return facts;
}

// readAgentName is the host this entry names on its argv, or "" when it names none. Empty is
// not defaulted: `magus shell` refuses installed glue that names no host (MGS3024). Any
// other argument is REPORTED on stderr and left out, and the call is judged anyway: a
// misconfigured entry must not block work, and must not be silent. A host shows a hook's
// stderr as an error notice, so the person who wrote the entry is the one who reads it.
fun readAgentName(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) {
        // A valued flag with no value after it. Caught because NOTHING here may raise.
        warn("could not parse this entry's arguments; the call was judged without them");
        return "";
    }
    foreach (word in parsed!.unknown) {
        // Quoted because an empty or whitespace argument otherwise reads as nothing.
        warn("unsupported argument \"{word}\"; this call was judged WITHOUT it. "
            + "This hook takes only {AGENT_NAME_FLAG} <host>. Fix the hook command in your host config.");
    }
    foreach (word in parsed!.positionals) {
        warn("stray word \"{word}\"; this call was judged WITHOUT it. "
            + "This hook takes only {AGENT_NAME_FLAG} <host>. Fix the hook command in your host config.");
    }
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
    response: str,
    // The event's cwd. The guard reads its workspace, job store and lease binding from
    // its own working directory, and a host may start hooks in the repository's main
    // checkout while the session works in a linked worktree.
    dir: str,
}

// Every flag rides in `extra`, so the unattributed retry drops them all: a binary too old
// for one fails BOTH attempts otherwise.
fun judge(guard: Guard, extra: [str]) > proc\ExecResult !> any {
    final args = mut ["shell"];
    foreach (arg in extra) { args.append(arg); }
    args.append("-o");
    args.append("template={guard.response}");
    return proc\exec(guard.bin, args: args, dir: guard.dir, opts: proc\ExecOptions{
        quiet = true,
        allowFailure = true,
        stdin = guard.payload,
    });
}

// UNKNOWN_FLAG is how magus's flag parser refuses a flag it does not define.
final UNKNOWN_FLAG = "flag provided but not defined";

// rejectedFlag reports an attempt refused for a flag the binary predates, the one failure
// that asking with fewer flags recovers. A deny exits non-zero with the verdict on stdout,
// and a timeout kill or a crash leaves stdout empty too; asking again judges the call a
// second time, which a host already out of time cannot afford.
fun rejectedFlag(result: proc\ExecResult?) > bool {
    return result != null
        and result!.code != 0
        and result!.stdout.trim() == ""
        and result!.stderr.indexOf(UNKNOWN_FLAG) != null;
}

// A person's open advisory, as context for the prompt the person just sent.
final PROMPT_REPLY = `{{if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":{{toJson .context}}}}{{end}}`;

// recordMessage hands a message the person typed to `magus shell --message`.
// A failure costs the record, never the message. It returns the reply: an
// open advisory as context, the notice that no magus ran, or "".
fun recordMessage(bin: str, message: str, dir: str, session: str, host: str, attribution: [str]) > str {
    if (bin == "" or !hook\isExecutable(bin)) {
        return hook\unrecordedNotice(bin, session: session, host: host,
            file: "magus-command.buzz", root: hook\workspaceRootAt(dir));
    }
    final args = mut ["shell", "--message", "--renders-context", "-o", "template={PROMPT_REPLY}"];
    foreach (word in attribution) { args.append(word); }
    final opts = proc\ExecOptions{quiet = true, allowFailure = true, stdin = message};
    final result = proc\exec(bin, args: args, dir: dir, opts: opts) catch null;
    if (result == null) { return ""; }
    if (!rejectedFlag(result)) { return hook\trimTrailingNewlines(result!.stdout); }
    // A magus older than advisories still records the message.
    final plain = mut ["shell", "--message"];
    foreach (word in attribution) { plain.append(word); }
    _ = proc\exec(bin, args: plain, dir: dir, opts: opts) catch void;
    return "";
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
final FAILURE_LINES = 12;

fun failureNotice(guard: Guard, failed: proc\ExecResult?) > str {
    var version = "";
    // In the binary's own directory, or version re-execs into another ./magus.
    final probe = proc\exec(guard.bin, args: ["version"], dir: binDir(guard.bin), opts: proc\ExecOptions{quiet = true, allowFailure = true}) catch null;
    if (probe != null) { version = hook\firstLine(probe!.stdout); }
    if (version == "") { version = "version unreadable"; }

    final lines = mut [<str>];
    if (failed != null) {
        foreach (line in failed!.stderr.split("\n")) {
            if (line != "" and line.indexOf("WARN") == null and lines.len() < FAILURE_LINES) { lines.append(line); }
        }
    }
    var why = lines.join("\n");
    if (why == "" and failed != null) { why = "it printed no error (exit code {failed!.code})"; }
    if (why == "") { why = "it could not be run"; }

    return "magus guard could not judge this call: {guard.bin} ({version}) said: {why}";
}

// binDir is the directory bin lives in, "" for a bare name found on PATH.
fun binDir(bin: str) > str {
    final parts = bin.split("/");
    final kept = mut [<str>];
    var i = 0;
    while (i < parts.len() - 1) {
        kept.append(parts[i]);
        i = i + 1;
    }
    return kept.join("/");
}

// unjudgedReply answers a call no magus judged. Every event shows the person
// systemMessage. A PreToolUse call that is not a read is refused. A
// PermissionRequest leaves the decision to the person's own prompt. The
// bootstrap line passes: it is the fix the notice names.
fun unjudgedReply(eventName: str, toolName: str, command: str, verdict: str, fix: str) > str {
    final text = "{verdict}\n{fix}";
    if (eventName == "Stop" or eventName == "SubagentStop") {
        return json\stringify({"systemMessage": text}) catch "";
    }
    if (eventName == "PermissionRequest") {
        return json\stringify({"systemMessage": text,
            "hookSpecificOutput": {"hookEventName": eventName}}) catch PERMISSION_NO_DECISION;
    }
    if (eventName == "PostToolUse" or READ_TOOLS.indexOf(toolName) != null or hook\isBootstrap(command)) {
        final named = if (eventName == "PostToolUse") eventName else "PreToolUse";
        return json\stringify({"systemMessage": text,
            "hookSpecificOutput": {"hookEventName": named, "additionalContext": text}}) catch "";
    }
    return hook\refusal(verdict, fix: fix);
}

// askBranch picks the ask arm for the host that sent this event. Only a reply this
// file assembled may claim --renders-ask; see the header for why Codex never receives
// permissionDecision "ask".
fun askBranch(event: any?, codex: bool, rule: str?, command: str) > str {
    final declared = env\get("HOST_ASK_BRANCH") catch "";
    if (declared != "") { return declared; }
    final blocker = promptBlocker(event, codex: codex, rule: rule);
    if (blocker == "") {
        if (codex) { return ASK_CODEX_PROMPTS; }
        return ASK_CLAUDE;
    }
    return blockedAsk(blocker, command: command);
}

// blockedAsk is the deny that stands in for a prompt that cannot happen.
// The verdict's reason leads both texts: it names the target or commit.
fun blockedAsk(blocker: str, command: str) > str {
    var yours = "Do this one yourself; it has no command line to copy.";
    var theirs = "Ask them to do it themselves.";
    if (command != "") {
        yours = "Run it from your own terminal: {command}";
        theirs = "Ask them to run it from their own terminal: {command}";
    }
    final person = "\n\nmagus stopped this call. It needs your approval, and {blocker}. {yours}";
    final agent = "\n\nThis call needs the approval of the person you work for, and {blocker}. {theirs} "
        + "If they saw no such message, relay this one to them verbatim.";
    return ASK_BLOCKED
        .replace(PERSON_SLOT, with: json\stringify(person) catch `""`)
        .replace(AGENT_SLOT, with: json\stringify(agent) catch `""`);
}

// adviseBranch is the advise arm.
fun adviseBranch() > str {
    final suppressed = env\get("__MAGUS_NO_ADVISE") catch "";
    if (suppressed != "") { return ""; }
    final declared = env\get("HOST_ADVISE_BRANCH") catch "";
    if (declared != "") { return declared; }
    return ADVISE_DEFAULT;
}

// permissionResponse answers the approval request Codex raises just before its own
// prompt. No decision object leaves the prompt to the person; allow skips it, and is
// answered only for a call a magus prompt rule raised, because this event fires for every
// approval Codex asks and a pass from the guard is not the person's consent to anything
// else.
fun permissionResponse(event: any?, rule: str?) > str {
    var covered = PERMISSION_NO_DECISION;
    if (rule != null and codexRuleMissing(event, rule: rule!) == "") { covered = PERMISSION_ALLOW; }
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

    // Empty unless the host names a field: the whole event goes, and magus reads the tool.
    final eventPath = hook\envOr("HOST_EVENT_PATH", fallback: "");
    final sessionPath = hook\envOr("HOST_SESSION_PATH", fallback: "session_id");
    final agentPath = hook\envOr("HOST_AGENT_PATH", fallback: "agent_id");
    final transcriptPath = hook\envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path");
    final agentName = readAgentName(args);
    // Overridable alongside the other dot-paths rather than fixed: a host that points
    // HOST_EVENT_PATH at a field it DOES populate for MCP calls loses the namespace test
    // otherwise, and gets that field judged as a shell command.
    final toolPath = hook\envOr("HOST_TOOL_PATH", fallback: "tool_name");
    final rawEvent = hook\wholeEvent(event, dotPath: eventPath, toolPath: toolPath);
    // A shell command line, which alone has a Codex prompt rule, a background mode and a
    // timeout: a string at the command field of a call that is not over MCP.
    final commandPath = hook\envOr("HOST_EVENT_PATH", fallback: hook\commandPath(event));
    final shellCall = !hook\wholeEvent(event, dotPath: commandPath, toolPath: toolPath);

    // Read BEFORE the availability check below, because the notices that check prints
    // are held to one firing per session and the session id is what keys them.
    final session = hook\field(event, dotPath: sessionPath);
    // Forwarded for a host whose HOST_EVENT_PATH selects one field out of the envelope:
    // without it a subagent's command reaches magus looking like its parent's.
    final agent = hook\field(event, dotPath: agentPath);
    final transcript = hook\field(event, dotPath: transcriptPath);
    final eventName = hook\field(event, dotPath: "hook_event_name");
    final toolName = hook\field(event, dotPath: toolPath);

    var promptRule: str? = null;
    var commandLine = "";
    if (shellCall) {
        commandLine = hook\field(event, dotPath: commandPath);
        promptRule = codexPromptRule(commandLine);
    }

    // Codex is the host the entry names, never one the event's shape suggests: see
    // AGENT_NAME_FLAG. An entry that names no host gets MGS3024 from magus, whatever arm
    // is assembled here.
    final codex = agentName == "codex";

    var response = env\get("HOST_RESPONSE") catch "";
    var capabilities = mut [<str>];
    if (response == "" and eventName == "PermissionRequest") {
        response = permissionResponse(event, rule: promptRule);
        capabilities.append("--renders-ask");
    } else if (response == "" and (eventName == "Stop" or eventName == "SubagentStop")) {
        response = STOP_REPLY.replace(STOP_EVENT_SLOT, with: eventName);
        if (codex) { response = STOP_REPLY_CODEX; }
    } else if (response == "") {
        response = DENY_HEAD + askBranch(event, codex: codex, rule: promptRule, command: commandLine)
            + adviseBranch() + PASS_AND_ADVISE_TAIL + UNKNOWN_DECISION;
        capabilities.append("--renders-ask");
    }

    final cwd = hook\commandDir(event,
        cwd: hook\field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd")),
        commandPath: commandPath, toolPath: toolPath);
    final bin = hook\resolveBin(cwd: cwd);

    // A message, by its shape: no tool, and text where HOST_MESSAGE_PATH points. Ahead of the
    // availability notice, which a message hook would hand the model as context.
    final message = hook\dig(event, dotPath: hook\envOr("HOST_MESSAGE_PATH", fallback: "prompt")) as? str;
    if (toolName == "" and message != null) {
        io\stdout.write(recordMessage(bin, message: message!, dir: cwd, session: session, host: agentName, attribution: [
            "--agent-name", agentName, "--transport", "buzz", "--session", session,
            "--agent", agent, "--transcript", transcript, "--event", eventName,
        ])) catch void;
        return;
    }

    // Every unjudged call names a fallback from ./magus; a judged one, once.
    final note = hook\fallbackNote(cwd);
    var fix = hook\magusFix(hook\workspaceRootAt(cwd));
    if (note != "") { fix = note + ". " + fix; }
    if (bin == "" or !hook\isExecutable(bin)) {
        final verdict = "magus guard could not judge this call: no magus binary was found";
        io\stdout.write(hook\envOr("__MAGUS_UNAVAILABLE_RESPONSE",
            fallback: unjudgedReply(eventName, toolName: toolName, command: commandLine, verdict: verdict, fix: fix))) catch void;
        return;
    }

    var payload = raw!;
    if (!rawEvent) {
        payload = hook\rawField(event, dotPath: eventPath) + "\n";
    }
    final guard = Guard{ bin = bin, payload = payload, response = response, dir = cwd };

    // Attribution is BEST EFFORT; the verdict is not.
    //
    // --agent-name, --session and --agent postdate the current magus release, and this template is
    // downloaded and run against whatever binary a reader already has. Passing them
    // unconditionally does not degrade the guard, it BREAKS it: an older binary rejects the
    // unknown flag, prints its usage to stderr, and exits non-zero, so the host receives no
    // verdict at all and every deny and advise rule silently stops being enforced.
    //
    // Only that refusal is asked again: see rejectedFlag.
    //
    // --renders-ask rides the attributed call only. A binary too old for it is too old to
    // ask, so the retry dropping it loses nothing.
    //
    // The call facts are newer than the rest, so a binary that rejects one is asked again
    // with everything else before it is asked with nothing: dropping them must not also
    // drop the attribution and the ask.
    //
    // --transport buzz names this glue as the caller. magus keeps a deny's full text and
    // its once-per-session notices per host, transport and session.
    final attributed = mut [<str>];
    foreach (word in ["--agent-name", agentName, "--transport", "buzz", "--session", session, "--agent", agent, "--transcript", transcript]) {
        attributed.append(word);
    }
    foreach (flag in capabilities) { attributed.append(flag); }
    final newest = mut [<str>];
    // Only a shell command the host runs has a background or a timeout to report.
    if (shellCall) {
        foreach (word in callFlags(event)) { newest.append(word); }
    }
    var result: proc\ExecResult? = null;
    if (newest.len() > 0) {
        final newer = mut [<str>];
        foreach (word in attributed) { newer.append(word); }
        foreach (word in newest) { newer.append(word); }
        result = judge(guard, extra: newer) catch null;
    }
    if (newest.len() == 0 or rejectedFlag(result)) {
        result = judge(guard, extra: attributed) catch null;
    }
    if (rejectedFlag(result)) {
        result = judge(guard, extra: [<str>]) catch null;
    }

    // A PASS and a BROKEN GUARD both render nothing, and telling them apart is the whole
    // point of this block. A pass exits 0 with empty output because there was nothing to
    // say; a binary that cannot run, too old for `magus shell`, unable to load the
    // workspace, half-written by a concurrent build, exits non-zero with empty output, and
    // printing that as a pass silently disables every rule with nothing anywhere saying so.
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        final refused = unjudgedReply(eventName, toolName: toolName, command: commandLine,
            verdict: failureNotice(guard, failed: result), fix: "Rebuild or update {bin}. {fix}");
        io\stdout.write(hook\envOr("__MAGUS_FAILED_RESPONSE", fallback: refused)) catch void;
        return;
    }
    var reply = hook\trimTrailingNewlines(result!.stdout);
    if (note != "" and hook\noticeOnce(session, family: "bin-fallback", file: "magus-command.buzz")) {
        reply = hook\withNotice(reply, text: "magus-command.buzz: " + note);
    }
    io\stdout.write(reply) catch void;
}

fun replyOf(text: str) > {str: any} {
    return json\parse(text) catch {<str: any>};
}

test "an unjudged call that may change state is refused and told to the person" {
    final reply = replyOf(unjudgedReply("PreToolUse", toolName: "Bash", command: "ls", verdict: "v", fix: "f"));
    final spec: {str: any} = reply["hookSpecificOutput"] ?? {<str: any>};
    std\assert(spec["permissionDecision"] == "deny", message: "{reply}");
    std\assert(spec["additionalContext"] == "f", message: "the agent reads the fix");
    std\assert(reply["systemMessage"] == "v\nf", message: "the person reads the verdict and the fix");
}

test "the bootstrap line alone passes an unjudged call" {
    final line = "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .";
    final reply = replyOf(unjudgedReply("PreToolUse", toolName: "Bash", command: line, verdict: "v", fix: "f"));
    final spec: {str: any} = reply["hookSpecificOutput"] ?? {<str: any>};
    std\assert(spec["permissionDecision"] == null and reply["systemMessage"] == "v\nf", message: "{reply}");
    final chained = replyOf(unjudgedReply("PreToolUse", toolName: "Bash", command: line + " && ls", verdict: "v", fix: "f"));
    final chainedSpec: {str: any} = chained["hookSpecificOutput"] ?? {<str: any>};
    std\assert(chainedSpec["permissionDecision"] == "deny", message: "{chained}");
}

test "an unjudged read passes, and the person still hears of it" {
    final reply = replyOf(unjudgedReply("PreToolUse", toolName: "Read", command: "", verdict: "v", fix: "f"));
    final spec: {str: any} = reply["hookSpecificOutput"] ?? {<str: any>};
    std\assert(spec["permissionDecision"] == null, message: "{reply}");
    std\assert(reply["systemMessage"] == "v\nf", message: "{reply}");
}

test "the lifecycle events carry the notice in their own dialect" {
    foreach (name in ["Stop", "SubagentStop"]) {
        final reply = replyOf(unjudgedReply(name, toolName: "", command: "", verdict: "v", fix: "f"));
        std\assert(reply["systemMessage"] == "v\nf" and reply["hookSpecificOutput"] == null, message: "{name}: {reply}");
    }
    final permission = replyOf(unjudgedReply("PermissionRequest", toolName: "Bash", command: "", verdict: "v", fix: "f"));
    final spec: {str: any} = permission["hookSpecificOutput"] ?? {<str: any>};
    std\assert(spec["hookEventName"] == "PermissionRequest" and spec["decision"] == null, message: "{permission}");
    final post = replyOf(unjudgedReply("PostToolUse", toolName: "Bash", command: "", verdict: "v", fix: "f"));
    final postOut: {str: any} = post["hookSpecificOutput"] ?? {<str: any>};
    std\assert(postOut["hookEventName"] == "PostToolUse" and postOut["permissionDecision"] == null, message: "{post}");
}

test "a message with no magus to record it says so once per session" {
    final session = "record-{std\random(min: 0, max: 1000000)}";
    std\assert(recordMessage("", message: "m", dir: "", session: session, host: "claude-code", attribution: [<str>]).indexOf("systemMessage") != null, message: "first");
    std\assert(recordMessage("", message: "m", dir: "", session: session, host: "claude-code", attribution: [<str>]) == "", message: "second");
}
```

## `magus-path.buzz`

The declared-output guard. Wire it to your host's file-editing tool rather than
its shell tool. It explains rather than blocks: editing a generated file is
wasteful, not destructive.

```buzz
// magus guard hook: judges ONE file path an agent is about to write.
//
// Companion to magus-command.buzz, for a host config that wires its file-editing tool
// apart from the rest. The shipped harnesses do not: they wire magus-command.buzz to every
// tool, and it judges a write by the shape of the whole event. It needs neither jq nor a
// POSIX shell.
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
// correct itself, rather than treating it as unable to learn. An unclaimed
// path gets no advisory: one fired on a guess trains the reader to ignore it.
// No magus to ask refuses the write.
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
// attribution recorded on the activity event, never an input to the verdict. magus reads
// whether the host's wiring sees a skill load from that host's harness spell.
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
//   __MAGUS_UNREADABLE_RESPONSE  what to print when the write never arrived
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
// magus-guard-template: 36
// magus-guard-coverage: schema=2 host=claude-code input=path deny=model advise=model pass=none ask=human

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

// The call never arrived, so no rule was offered it.
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
    return proc\exec(guard.bin, args: args, dir: guard.dir, opts: proc\ExecOptions{
        quiet = true,
        allowFailure = true,
        stdin = guard.payload,
    });
}

final UNKNOWN_FLAG = "flag provided but not defined";

// rejectedFlag is magus-command.buzz's: only a flag the binary predates is asked again.
fun rejectedFlag(result: proc\ExecResult?) > bool {
    return result != null
        and result!.code != 0
        and result!.stdout.trim() == ""
        and result!.stderr.indexOf(UNKNOWN_FLAG) != null;
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

// readAgentName reads the host off this script's argv, and reports every other argument.
// An empty host is not defaulted: `magus shell` refuses installed glue that names no host
// (MGS3024). A misconfigured entry must not block work, and must not be invisible either.
fun readAgentName(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) { return ""; }
    foreach (word in parsed!.unknown + parsed!.positionals) {
        warn("unsupported argument \"{word}\"; this hook takes only {AGENT_NAME_FLAG} <host>. "
            + "Fix the hook command in your host config.");
    }
    return parsed!.values[AGENT_NAME_FLAG] ?? "";
}

fun main(args: [str]) > void {
    final agentName = readAgentName(args);
    // Empty unless the host names a field: the whole event goes, content included.
    final eventPath = hook\envOr("HOST_EVENT_PATH", fallback: "");
    final sessionPath = hook\envOr("HOST_SESSION_PATH", fallback: "session_id");
    final agentPath = hook\envOr("HOST_AGENT_PATH", fallback: "agent_id");
    final transcriptPath = hook\envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path");

    // The event is read first, as magus-command.buzz reads it: the binary is the one in the
    // checkout the event's cwd names.
    final raw = io\stdin.readAll() catch null;
    final event = json\parse(raw ?? "") catch null;

    final cwd = hook\field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd"));
    final bin = hook\resolveBin(cwd: cwd);
    final note = hook\fallbackNote(cwd);
    var fix = hook\magusFix(hook\workspaceRootAt(cwd));
    if (note != "") { fix = note + ". " + fix; }
    if (bin == "" or !hook\isExecutable(bin)) {
        io\stdout.write(hook\envOr("__MAGUS_UNAVAILABLE_RESPONSE", fallback: hook\refusal(
            "magus guard could not judge this write: no magus binary was found", fix: fix))) catch void;
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
    // printing usage to stderr and exiting non-zero, which leaves the host with no verdict
    // rather than an unattributed one. Try with attribution, fall back to the call this
    // script made before it existed.
    //
    // Only that refusal is asked again: see rejectedFlag.
    final attributed = mut [<str>];
    foreach (word in ["--agent-name", agentName, "--transport", "buzz", "--session", session, "--agent", agent, "--transcript", transcript]) {
        attributed.append(word);
    }
    foreach (flag in rendersAsk) { attributed.append(flag); }
    var result = judge(guard, extra: attributed) catch null;
    if (rejectedFlag(result)) {
        result = judge(guard, extra: [<str>]) catch null;
    }

    // A pass and a broken guard both render nothing; see magus-command.buzz for why
    // telling them apart matters. Every call here is a write, so it is refused.
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        var why = "it could not be run";
        if (result != null) { why = "it printed no error (exit code {result!.code})"; }
        if (result != null and hook\firstLine(result!.stderr) != "") { why = hook\firstLine(result!.stderr); }
        io\stdout.write(hook\envOr("__MAGUS_FAILED_RESPONSE", fallback: hook\refusal(
            "magus guard could not judge this write: {bin} said: {why}",
            fix: "Rebuild or update {bin}. {fix}"))) catch void;
        return;
    }
    var reply = hook\trimTrailingNewlines(result!.stdout);
    if (note != "" and hook\noticeOnce(session, family: "bin-fallback", file: "magus-path.buzz")) {
        reply = hook\withNotice(reply, text: "magus-path.buzz: " + note);
    }
    io\stdout.write(reply) catch void;
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
// it to `magus shell --observe`. It always exits 0; see the note on that below.
// It prints only the once-per-session notice that no magus could record.
// Override any of the variables below:
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
// magus-guard-template: 36

// EVERY call that can fail is caught, deliberately.
//
// An unparsable event, a missing binary, a flag the binary does not know: each one
// must exit 0. A PreToolUse hook's exit status is not advisory on
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
    foreach (word in parsed!.positionals) {
        warn("stray word \"{word}\"; this hook accepts only {AGENT_NAME_FLAG}. "
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

    final raw = io\stdin.readAll() catch "";
    final event = json\parse(raw) catch null;
    // The event's cwd, for the reason magus-command.buzz's Guard gives.
    final cwd = field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd"));
    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) {
        io\stdout.write(hook\unrecordedNotice(bin, session: field(event, dotPath: sessionPath),
            host: agentName, file: "magus-observe.buzz", root: hook\workspaceRootAt(cwd))) catch void;
        return;
    }

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
    _ = proc\exec(bin, args: [
        "shell", "--observe",
        "--agent-name", agentName,
        "--session", field(event, dotPath: sessionPath),
        "--transcript", field(event, dotPath: transcriptPath),
        "--event", "PreToolUse",
    ], dir: cwd, opts: proc\ExecOptions{quiet = true, allowFailure = true, stdin = reached}) catch void;
}
```

## `magus-duration.buzz`

Carries no verdict either. Wire it to the host's post-call event on every tool, in
the background where the host has one, and it records how long the tool ran as the
host reports it. A host whose event carries no duration records nothing. It always
exits 0.

```buzz
// magus duration hook: records how long a tool ran, as the host says.
// It judges nothing and always exits 0.
// Run as `magus buzz -s magus-duration.buzz -- --agent-name <host>`.
// An event with no duration records nothing.
// The HOST_*_PATH variables retarget a field, as in magus-observe.buzz.
//
// It also sends digests of the call (HOST_INPUT_PATH) and of its result
// (HOST_RESULT_PATH), never the text. A host that sends no result gets no
// digest, and magus records the result as unobserved. HOST_AGENT_PATH names
// the subagent, which tells a worker from its parent.
//
// magus-guard-template: 36

import "std";
import "io";
import "flags";
import "crypto";
import "encoding/json";
import "proc";
import "lib/hook" as hook;

fun field(event: any?, dotPath: str) > str {
    final value = hook\dig(event, dotPath: dotPath);
    if (value == null) { return ""; }
    return (value as? str) ?? "";
}

// A run under one millisecond reads as 1, since 0 means nothing was reported.
fun milliseconds(event: any?, dotPath: str) > int {
    final value = hook\dig(event, dotPath: dotPath);
    var ms = 0;
    if (value is int) { ms = value as int; }
    if (value is double) { ms = std\toInt(value as double); }
    if (value == null or ms < 0) { return 0; }
    if (ms == 0) { return 1; }
    return ms;
}

final DIGEST_HEX_CHARS = 16;

// A string is hashed as written, anything else as its JSON.
fun digestOf(event: any?, dotPath: str) > str {
    final value = hook\dig(event, dotPath: dotPath);
    if (value == null) { return ""; }
    var text = "";
    if (value is str) {
        text = value as str;
    } else {
        text = json\stringify(value) catch "";
    }
    if (text == "") { return ""; }
    return crypto\sha256Hex(text).sub(0, len: DIGEST_HEX_CHARS);
}

fun warn(message: str) > void {
    io\stderr.write("magus-duration.buzz: {message}\n") catch void;
}

final AGENT_NAME_FLAG = "--agent-name";

fun readAgentName(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) { return ""; }
    foreach (word in parsed!.unknown) {
        warn("unsupported argument \"{word}\"; this hook accepts only {AGENT_NAME_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    foreach (word in parsed!.positionals) {
        warn("stray word \"{word}\"; this hook accepts only {AGENT_NAME_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    return parsed!.values[AGENT_NAME_FLAG] ?? "";
}

fun main(args: [str]) > void {
    final agentName = readAgentName(args);
    if (agentName == "") {
        warn("[MGS3024] this hook was not given --agent-name, so nothing was recorded. "
            + "Merge what `magus describe harness` prints into the host's hook configuration; "
            + "the commands it prints name the host.");
        return;
    }
    final sessionPath = hook\envOr("HOST_SESSION_PATH", fallback: "session_id");
    final raw = io\stdin.readAll() catch "";
    final event = json\parse(raw) catch null;
    // Cursor names its session conversation_id.
    var session = field(event, dotPath: sessionPath);
    if (session == "") { session = field(event, dotPath: "conversation_id"); }
    final cwd = field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd"));
    final tool = field(event, dotPath: hook\envOr("HOST_TOOL_PATH", fallback: "tool_name"));
    var ms = milliseconds(event, dotPath: hook\envOr("HOST_DURATION_PATH", fallback: "duration_ms"));
    if (ms == 0) { ms = milliseconds(event, dotPath: "duration"); }
    if (tool == "" or ms == 0) { return; }

    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) {
        io\stdout.write(hook\unrecordedNotice(bin, session: session,
            host: agentName, file: "magus-duration.buzz", root: hook\workspaceRootAt(cwd))) catch void;
        return;
    }

    final args = mut [
        "shell", "--duration", "{ms}ms",
        "--agent-name", agentName,
        "--session", session,
        "--agent", field(event, dotPath: hook\envOr("HOST_AGENT_PATH", fallback: "agent_id")),
        "--transcript", field(event, dotPath: hook\envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path")),
        "--event", "PostToolUse",
    ];
    final call = digestOf(event, dotPath: hook\envOr("HOST_INPUT_PATH", fallback: "tool_input"));
    if (call != "") {
        args.append("--call-digest");
        args.append(call);
    }
    final result = digestOf(event, dotPath: hook\envOr("HOST_RESULT_PATH", fallback: "tool_response"));
    if (result != "") {
        args.append("--result-digest");
        args.append(result);
    }
    _ = proc\exec(bin, args: args, dir: cwd, opts: proc\ExecOptions{quiet = true, allowFailure = true, stdin = tool}) catch void;
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
// a person writes. It always exits 0, and prints only the once-per-session
// notice that no magus could record. Override:
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
// magus-guard-template: 36

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
    foreach (word in parsed!.unknown + parsed!.positionals) {
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
    final doc = json\parse(event) catch null;
    // The event's cwd names the tree whose state is recorded, for the reason
    // magus-command.buzz's Guard gives.
    final cwd = hook\field(doc, dotPath: "cwd");

    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) {
        io\stdout.write(hook\unrecordedNotice(bin, session: hook\field(doc, dotPath: "session_id"),
            host: agentName, file: "magus-checkpoint.buzz", root: hook\workspaceRootAt(cwd))) catch void;
        return;
    }

    // Both streams are discarded by `quiet`: a magus too old for `session checkpoint`
    // prints its usage, and that would otherwise reach the host as this hook's response
    // every time a session ends. The absence shows up where it is actionable instead -
    // as an empty checkpoint list in `magus session`.
    _ = proc\exec(bin, args: [
        "session", "checkpoint",
        "--agent-name", agentName,
    ], dir: cwd, opts: proc\ExecOptions{quiet = true, allowFailure = true, stdin = event}) catch void;
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
// A subagent starts with none of its parent's history, which is the same loss, so the
// event may also be SubagentStart: the reply then names that event, and the child reads
// the brief before its first step. Any other event is answered as SessionStart.
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
// magus-guard-template: 36

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
final SUBAGENT_START = "SubagentStart";

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
    foreach (word in parsed!.unknown + parsed!.positionals) {
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
    final event = json\parse(io\stdin.readAll() catch "") catch null;
    final cwd = hook\field(event, dotPath: "cwd");
    var eventName = "SessionStart";
    if (hook\field(event, dotPath: "hook_event_name") == SUBAGENT_START) { eventName = SUBAGENT_START; }
    var root = "";
    if (cwd != "") { root = hook\workspaceRootAt(cwd); }
    if (root == "") { root = hook\workspaceRoot(); }

    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) {
        // Only the json reply carries a systemMessage.
        final host = if (format == "json") "claude-code" else "";
        io\stdout.write(hook\unrecordedNotice(bin, session: hook\field(event, dotPath: "session_id"),
            host: host, file: "magus-rehydrate.buzz", root: root)) catch void;
        return;
    }

    // Captured rather than streamed, because the json arm has to wrap it. stderr is
    // discarded by `quiet`: a magus too old for `session --brief` prints its usage
    // there, and that would otherwise be injected as this hook's answer.
    final result = proc\exec(bin, args: ["session", "--brief"], dir: cwd, opts: proc\ExecOptions{
        quiet = true,
        allowFailure = true,
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
        io\stdout.write(`\{"hookSpecificOutput":\{"hookEventName":"{eventName}","additionalContext":`
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
import "proc";
import "std";
import "env";
import "encoding/json";
import "lib/hook" as hook;

// magus session hook: puts the session checkout's own magus first on PATH for the shell
// commands a session runs, so a `magus` typed there is the build its hooks run, and says
// once per session when that magus cannot load the checkout.
//
// Wire it to your host's session-start event as `magus buzz -C <root> -s
// magus-session.buzz -- --env-file <file>`. <file> is the one your host sources before each
// shell command. The directory that goes on PATH is the workspace root above the `cwd` the
// event reports, found the way the guard glue finds it (the nearest magus.yaml), NOT the
// directory -C names: a host's command line names the main checkout, and a worktree session
// would otherwise run main's build. An event with no cwd falls back to -C's directory.
// The hook appends `export PATH="<root>:$PATH"` to <file>, keeping what other hooks wrote,
// and leaves `$PATH` for that shell to expand. An empty or absent --env-file sets no PATH.
//
// It then resolves the binary the guard glue would run for that cwd and loads the
// workspace with it (`ls`). If that fails, or no binary resolves at all, it answers
// with additionalContext naming one command, once per session. A guard whose binary
// cannot load the tree judges nothing and denies every call that changes state, and a
// session should hear that when it starts, not at the first refused edit. Otherwise it
// prints nothing on stdout.
// It exits 0 either way.
//
// NO magus-guard-coverage line: it judges nothing on any input.
//
// magus-guard-template: 36

final ENV_FILE_FLAG = "--env-file";
final BOOTSTRAP = "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .";
final LEASE_KEY = "magus.lease";
final HOOK_FILE = "magus-session.buzz";

fun warn(message: str) > void {
    io\stderr.write("magus-session.buzz: {message}\n") catch void;
}

fun envFilePath(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [ENV_FILE_FLAG]) catch null;
    if (parsed == null) {
        warn("could not read the arguments {args}; PATH is unchanged. Fix the hook command in your host config.");
        return "";
    }
    foreach (word in parsed!.unknown + parsed!.positionals) {
        warn("unsupported argument \"{word}\"; this hook accepts only {ENV_FILE_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    return parsed!.values[ENV_FILE_FLAG] ?? "";
}

// leaseFrom is the lease a W3C baggage header names, "" when it names none. The hook runs
// inside a worker's environment, where BAGGAGE carries `magus.lease=<id>`.
fun leaseFrom(baggage: str) > str {
    foreach (member in baggage.split(",")) {
        final pair = member.trim().split(";")[0];
        final eq = pair.indexOf("=");
        if (eq != null and pair.sub(0, len: eq!).trim() == LEASE_KEY) {
            return pair.sub(eq! + 1, len: pair.len() - eq! - 1).trim();
        }
    }
    return "";
}

// fixFor is the likeliest fix for a binary unable to load root, carrying at most one
// command. A leased worker never builds: there is one binary per base, and the main
// session places it.
fun fixFor(own: bool, hasBinary: bool, lease: str) > str {
    if (lease != "") {
        if (own) {
            return "ask the main session to place ./magus: `<main checkout>/magus buzz hack/dev/bootstrap-worktree.buzz -- --job {lease} --from <main checkout>`";
        }
        return "ask the main session to install a magus that loads this workspace";
    }
    if (own and hasBinary) {
        return "rebuild ./magus with `./magus run go-build .`";
    }
    if (own) {
        return "bootstrap ./magus with `{BOOTSTRAP}`";
    }
    return "install a magus that loads this workspace with `magus self update`";
}

// unloadableNotice is the verdict and the fix on its first line, and the cause after it.
// why is the error the load printed, or "" when no binary resolved at all.
fun unloadableNotice(root: str, bin: str, why: str, fix: str) > str {
    var cause = "No magus binary resolves for {root}.";
    if (bin != "") {
        cause = "{bin} cannot load {root}.";
        if (why != "") { cause = "{bin} cannot load {root}: {why}"; }
    }
    return "the magus guard is not judging this session; {fix}.\n{cause}\n"
        + "Every edit, spawn, push and state-changing command is denied until a magus loads the tree; reads and the bootstrap line still run.";
}

fun contextReply(text: str) > str {
    final encoded = json\stringify(text) catch `""`;
    return "\{\"hookSpecificOutput\":\{\"hookEventName\":\"SessionStart\",\"additionalContext\":{encoded}}}";
}

// readEvent is the host's SessionStart event, null when the hook was run by hand or the
// payload does not parse. A terminal stdin would block forever, so it is never read.
fun readEvent() > any? {
    if (proc\stdinIsTerminal()) { return null; }
    final raw = io\stdin.readAll() catch null;
    if (raw == null) { return null; }
    return json\parse(raw!) catch null;
}

// firstLine is the first non-empty line of text, which is where a load error says what it is.
fun firstLine(text: str) > str {
    foreach (line in text.split("\n")) {
        if (line.trim() != "") { return line.trim(); }
    }
    return "";
}

// loadFailure loads the workspace with bin, the way any command would, and returns the error
// it printed. "" means it loaded; a binary that would not even start reports that.
fun loadFailure(bin: str, root: str) > str {
    final result = proc\exec(bin, args: ["--root", root, "ls", "-o", "name"], dir: root, opts: proc\ExecOptions{
        quiet = true,
        allowFailure = true,
    }) catch null;
    if (result == null) { return "it could not be started"; }
    if (result!.code == 0) { return ""; }
    final said = firstLine(result!.stderr);
    if (said == "") { return "it exited {result!.code} without an error"; }
    return said;
}

fun main(args: [str]) > void {
    final envFile = envFilePath(args);
    final event = readEvent();
    final cwd = hook\field(event, dotPath: hook\envOr("HOST_CWD_PATH", fallback: "cwd"));
    var root = "";
    if (cwd != "") { root = hook\workspaceRootAt(cwd); }
    if (root == "") { root = path\abs(".") catch ""; }
    if (root == "") { return; }

    if (envFile != "") {
        try {
            fs\appendFile(envFile, content: "export PATH=\"{root}:$PATH\"\n");
        } catch {
            warn("could not append to {envFile}; the session's commands find magus on PATH as it was.");
        }
    }

    final bin = hook\resolveBin(cwd: cwd);
    var why = "";
    if (bin != "") { why = loadFailure(bin, root: root); }
    if (bin != "" and why == "") { return; }

    final session = hook\field(event, dotPath: hook\envOr("HOST_SESSION_PATH", fallback: "session_id"));
    if (!hook\noticeOnce(session, family: "session-unloadable", file: HOOK_FILE)) { return; }
    final own = (fs\isFile("{root}/go.mod") catch false) and (fs\isFile("{root}/cmd/magus/main.go") catch false);
    final hasBinary = hook\isExecutable("{root}/magus");
    final fix = fixFor(own, hasBinary: hasBinary, lease: leaseFrom(env\get("BAGGAGE") catch ""));
    io\stdout.write(contextReply(unloadableNotice(root, bin: bin, why: why, fix: fix))) catch void;
}

test "a lease is read off the baggage header" {
    std\assert(leaseFrom("magus.lease=hook-binary") == "hook-binary", message: "alone");
    std\assert(leaseFrom("a=b, magus.lease=hook-binary;prop=1 ,c=d") == "hook-binary", message: "among others");
    std\assert(leaseFrom("a=b") == "", message: "absent");
    std\assert(leaseFrom("") == "", message: "empty");
}

// commandCount is how many backticked commands text carries.
fun commandCount(text: str) > int {
    return (text.split("`").len() - 1) / 2;
}

test "the fix follows who is asking and names one command" {
    std\assert(fixFor(true, hasBinary: true, lease: "") == "rebuild ./magus with `./magus run go-build .`", message: "main session with a binary");
    std\assert(fixFor(true, hasBinary: false, lease: "") == "bootstrap ./magus with `{BOOTSTRAP}`", message: "main session with none");
    std\assert(fixFor(false, hasBinary: true, lease: "") == "install a magus that loads this workspace with `magus self update`", message: "another workspace");
    final worker = fixFor(true, hasBinary: true, lease: "w1");
    std\assert(worker.indexOf("bootstrap-worktree.buzz -- --job w1 --from <main checkout>") != null, message: "a worker asks for a placed binary");
    std\assert(worker.indexOf("go-build") == null, message: "and is never told to build");
    foreach (fix in [fixFor(true, hasBinary: true, lease: ""), fixFor(true, hasBinary: false, lease: ""), fixFor(false, hasBinary: true, lease: ""), worker]) {
        std\assert(commandCount(fix) <= 1, message: fix);
    }
}

test "the notice opens with the verdict and one command; the error follows it" {
    final text = unloadableNotice("/w", bin: "/usr/bin/magus", why: "object guard has no field", fix: "rebuild ./magus with `./magus run go-build .`");
    final lines = text.split("\n");
    std\assert(lines[0] == "the magus guard is not judging this session; rebuild ./magus with `./magus run go-build .`.", message: text);
    std\assert(lines[1] == "/usr/bin/magus cannot load /w: object guard has no field", message: text);
    std\assert(commandCount(lines[0]) == 1, message: text);
    std\assert(unloadableNotice("/w", bin: "", why: "", fix: "x").indexOf("\nNo magus binary resolves for /w.\n") != null, message: "nothing resolves");
}

test "the reply is the SessionStart envelope" {
    std\assert(contextReply("hi \"there\"") == "\{\"hookSpecificOutput\":\{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"hi \\\"there\\\"\"}}", message: contextReply("hi \"there\""));
}

test "the first non-empty line is the error" {
    std\assert(firstLine("\n  \nerror: boom\nmore") == "error: boom", message: "skips blanks");
    std\assert(firstLine("") == "", message: "empty");
}
```

## `proofread-reply.buzz`

The fifth template that carries no verdict. It runs proofread rather than magus. Wire
it to your host's stop event and it judges the reply a turn ends with, then hands up to
five findings back to the agent as context for the next step. It needs only the
`proofread` binary from the same release as magus: on `PATH`, in `__PROOFREAD_BIN`,
or named with `-- --proofread PATH`. `-- --decisions FILE` passes a JSON table of
`{"rules": {"<rule>": "deny"}}` to proofread.

It advises and nothing more: it prints no decision, always exits 0, and prints
nothing when proofread is missing or fails. A reply written while a stop hook has the
agent continuing (`stop_hook_active`) is never judged, so the context it hands back
cannot loop the agent. [Check writing in a commit hook](../../proofread.md) covers
choosing decisions.

It declares no `magus-guard-coverage` line, for the reason
`magus-observe.buzz` declares none: it carries no verdict on any input.

```buzz
// proofread-reply hook: judges the reply an agent ends its turn with and hands the findings
// back so the next step can fix them.
//
// Run it as `magus buzz -s proofread-reply.buzz -- --agent-name <host>`; see
// magus-command.buzz for why `-s` is load-bearing. It imports no magus module and no spell,
// so the workspace stays CLOSED, and it needs only the proofread release binary:
// https://github.com/egladman/magus/releases
//
// Wire it to your host's stop event. It reads the reply, runs `proofread agent-reply --stdin
// -o json` over it, and hands back at most 5 findings in the shape the named host takes:
//
//   claude-code  Stop: nothing, since Stop context starts a turn
//                the person sees answered. The findings wait in a
//                store for the next UserPromptSubmit, which hands them
//                over as additionalContext. Wire both events.
//   codex        Stop: {"decision": "block", "reason": ...}. Codex continues the turn with
//                the reason as a new prompt, once: the next Stop carries stop_hook_active.
//   cursor       stop: {"followup_message": ...}. Cursor submits it as another turn. It
//                cannot block, and the hook's loop_limit caps the chain.
//   opencode     {"prompt": ...}. OpenCode has no stop hook; opencode-plugin.ts calls this on a
//                root session's idle event, with the codex envelope (last_assistant_message,
//                stop_hook_active), and sends the prompt back with session.promptAsync.
//
// Cursor's stop event carries no reply, so the hook reads it from the conversation's
// transcript_path: the last assistant message that has text, stopping at the last user
// message. A host this file has no shape for is named on stderr and answered with nothing,
// and so is an event that is a tool call rather than a stop: the hook is wired to the wrong
// event.
//
// It prints nothing when
//
//   - the stop is already a continuation (stop_hook_active on claude-code and codex,
//     loop_count above 0 on cursor), because the findings this hook handed back are what
//     started it, and judging that reply would loop;
//   - the message is empty, the envelope is unreadable, or the turn did not complete;
//   - proofread finds nothing.
//
// A proofread that is missing, stale, exits 2 or prints unreadable JSON
// judged nothing. That gets one notice per session naming the binary and its
// fix, a systemMessage on claude-code and codex, else stderr. The reply is
// never blocked. It always exits 0.
//
// In a checkout of magus the proofread it builds judges, checked for
// staleness by that checkout's own proofread module.
//
// Arguments:
//
//   --agent-name HOST  REQUIRED: the host this entry is wired into, the harness spell's own
//                      name. Without it nothing is judged and MGS3024 goes to stderr
//   --decisions FILE   a JSON decisions table passed to proofread as --decisions:
//                      {"rules": {"<rule>": "deny" | "advise" | "off"},
//                      "minimums": {"agent-reply": N}}. It must set the agent-reply
//                      minimum, or proofread judges nothing. Keep it to those two keys:
//                      proofread refuses a "paths" glob that matches no file argument,
//                      and a reply is read from stdin
//   --proofread PATH   the proofread binary, ahead of the environment and PATH
//
// Override:
//
//   __PROOFREAD_BIN        path to the binary, ahead of PATH
//   __PROOFREAD_DECISIONS  the decisions file, when --decisions is not given
//
// NO magus-guard-coverage line, for the reason magus-checkpoint.buzz has none: this file
// carries no verdict on any input.
//
// magus-guard-template: 36

import "io";
import "fs";
import "flags";
import "proc";
import "std";
import "encoding/json";
import "lib/hook" as hook;

final AGENT_FLAG = "--agent-name";
final DECISIONS_FLAG = "--decisions";
final PROOFREAD_FLAG = "--proofread";

final CLAUDE_CODE = "claude-code";
final CODEX = "codex";
final CURSOR = "cursor";
final OPENCODE = "opencode";

// MINIMUM is the grade a reply is held to when no decisions file is given: the review-reply
// minimum in magus's own hack/policy/proofread.buzz, where every reply people wrote grades 100.
final MINIMUM = 100;

// MAX_FINDINGS caps the findings handed back for one reply, in proofread's order.
final MAX_FINDINGS = 5;

final sourceLine = $":([0-9]+)$";

fun warn(message: str) > void {
    io\stderr.write("proofread-reply.buzz: {message}\n") catch void;
}

// knownHost is a host this file has an output shape for.
fun knownHost(host: str) > bool {
    return host == CLAUDE_CODE or host == CODEX or host == CURSOR or host == OPENCODE;
}

// Stop is what this hook reads of a stop envelope: the reply, and whether the agent is
// already continuing because a stop hook handed it something.
object Stop {
    reply: str,
    again: bool,
    // tool names the tool when the event is a tool call, not a stop.
    tool: str = "",
}

// Finding is one proofread finding; line counts from the reply's first line, 0 when the
// finding has none.
object Finding {
    rule: str,
    line: int = 0,
    message: str,
}

// textOf joins the text parts of one transcript message's content, "" for any other shape.
fun textOf(content: any?) > str {
    final parts = content as? [any];
    if (parts == null) { return ""; }
    final texts = mut [<str>];
    foreach (part in parts!) {
        final text = hook\field(part, dotPath: "text");
        if (hook\field(part, dotPath: "type") == "text" and text.trim() != "") {
            texts.append(text);
        }
    }
    return texts.join("\n\n");
}

// lastAssistantText is the last assistant message with text in a JSONL transcript, or ""
// when the last user message comes first: a turn that has said nothing yet has no reply.
fun lastAssistantText(transcript: str) > str {
    if (transcript == "") { return ""; }
    final lines = (fs\readFile(transcript) catch "").split("\n");
    var i = lines.len() - 1;
    while (i >= 0) {
        final row: any? = json\parse(lines[i]) catch null;
        i = i - 1;
        if (row == null) { continue; }
        final role = hook\field(row, dotPath: "role");
        if (role == "user") { return ""; }
        if (role == "assistant") {
            final text = textOf(hook\dig(row, dotPath: "message.content"));
            if (text != "") { return text; }
        }
    }
    return "";
}

// readCursorStop reads Cursor's stop. loop_count above 0 means a follow-up message started
// this turn, and a status other than completed means there is no finished reply to judge.
fun readCursorStop(doc: {str: any}) > Stop {
    final looped = (std\parseDouble(hook\field(doc, dotPath: "loop_count")) ?? 0.0) > 0.0;
    final status = hook\field(doc, dotPath: "status");
    if (looped or (status != "" and status != "completed")) {
        return Stop{ reply = "", again = looped };
    }
    return Stop{ reply = lastAssistantText(hook\field(doc, dotPath: "transcript_path")), again = false };
}

// readStop reads the envelope host sent. One it cannot read is a stop with no reply.
fun readStop(input: str, host: str) > Stop {
    final doc: {str: any} = json\parse(input) catch {<str: any>};
    final tool = hook\field(doc, dotPath: "tool_name");
    if (tool != "") { return Stop{ reply = "", again = false, tool = tool }; }
    if (host == CURSOR) { return readCursorStop(doc); }
    final reply: str = doc["last_assistant_message"] ?? "";
    final again: bool = doc["stop_hook_active"] ?? false;
    return Stop{ reply = reply, again = again };
}

// parseFindings reads proofread's JSON findings, whose source ends in
// `:<line>`. Output it cannot read is null.
fun parseFindings(stdout: str) > [Finding]? {
    final parsed = json\parse(stdout) catch null;
    final rows = parsed as? [any];
    if (rows == null) { return null; }
    final found = mut [<Finding>];
    foreach (r in rows) {
        final source: str = r["source"] ?? "";
        var line = 0;
        final at = sourceLine.match(source);
        if (at != null) {
            line = std\parseInt(at![1]) ?? 0;
        }
        found.append(Finding{ rule = r["rule"] ?? "", line = line, message = r["message"] ?? "" });
    }
    return found;
}

// A checkout of magus links its own proofread, relative to the root.
final WORKSPACE_MODULE = "libs/proofread/proofread.buzz";
final WORKSPACE_BINARY = "libs/proofread/gen/proofread";

// STALE_SCRIPT asks the checkout's proofread module whether its build is
// stale: the check judgeRunner makes, never a copy of it.
// Each line carries a prefix: magus buzz drops a line printed empty.
final STALE_SCRIPT = `import "std"; import "./libs/proofread/proofread" as p; std\print("build:" + p\build); std\print("stale:" + p\whyStale("libs/proofread"));`;

final RELEASE_FIX = "Install proofread from https://github.com/egladman/magus/releases, "
    + "or point --proofread or __PROOFREAD_BIN at it.";

// Proofreader is the binary that judges replies. why is "" when it may run,
// else why not, and fix says what makes it runnable.
object Proofreader {
    bin: str,
    why: str = "",
    fix: str = "",
}

// proofreader is the --proofread path, else __PROOFREAD_BIN, else the build a
// checkout of magus links, else proofread on PATH.
fun proofreader(flagged: str, root: str) > Proofreader {
    var named = flagged;
    if (named == "") { named = hook\envOr("__PROOFREAD_BIN", fallback: ""); }
    if (named != "") {
        if (hook\isExecutable(named)) { return Proofreader{ bin = named }; }
        return Proofreader{ bin = named, why = "{named} is not an executable file", fix = RELEASE_FIX };
    }
    if (root != "" and (fs\isFile("{root}/{WORKSPACE_MODULE}") catch false)) {
        return workspaceBuild(root);
    }
    final onPath = proc\which("proofread") catch "";
    if (onPath == "") { return Proofreader{ bin = "proofread", why = "no proofread is on PATH", fix = RELEASE_FIX }; }
    return Proofreader{ bin = onPath };
}

// workspaceBuild is the proofread root links, checked for staleness by the
// magus that judges root.
fun workspaceBuild(root: str) > Proofreader {
    final bin = "{root}/{WORKSPACE_BINARY}";
    final magus = hook\resolveBin(cwd: root);
    if (magus == "" or !hook\isExecutable(magus)) {
        return Proofreader{ bin = bin, why = "no runnable magus can tell whether {bin} is stale",
            fix = hook\magusFix(root) };
    }
    final run = proc\exec(magus, args: ["buzz", "-C", root, "-s", "-e", STALE_SCRIPT], dir: root,
        opts: proc\ExecOptions{quiet = true, allowFailure = true}) catch null;
    final lines = if (run != null and run!.code == 0) run!.stdout.split("\n") else [<str>];
    if (lines.len() < 2 or !lines[0].startsWith("build:") or !lines[1].startsWith("stale:")) {
        var said = "it did not start";
        if (run != null) { said = "it exited {run!.code}: {hook\firstLine(run!.stderr)}"; }
        return Proofreader{ bin = bin, why = "{magus} could not tell whether {bin} is stale, as {said}",
            fix = "Rebuild or update {magus}. {hook\magusFix(root)}" };
    }
    final stale = lines[1].sub(6, len: lines[1].len() - 6);
    if (stale != "") {
        final build = lines[0].sub(6, len: lines[0].len() - 6);
        return Proofreader{ bin = bin, why = "proofread is stale: {stale}", fix = "Run `{build}`." };
    }
    return Proofreader{ bin = bin };
}

// Judgment is one proofread run: its findings, or why it could not judge.
object Judgment {
    found: [Finding],
    failure: str = "",
}

// judge runs bin over reply. With no decisions file it grades the reply
// against MINIMUM; a decisions file sets the agent-reply minimum itself.
fun judge(bin: str, decisions: str, reply: str) > Judgment {
    final argv = mut ["agent-reply", "--stdin", "-o", "json"];
    if (decisions != "") {
        argv.append("--decisions");
        argv.append(decisions);
    } else {
        argv.append("--minimum");
        argv.append("{MINIMUM}");
    }
    final run = proc\exec(bin, args: argv,
        opts: proc\ExecOptions{stdin = reply, quiet = true, allowFailure = true}) catch null;
    if (run == null) { return Judgment{ found = [<Finding>], failure = "{bin} did not start" }; }
    // Exit 1 is a deny finding, written to stdout like any other; 2 is a run that could not judge.
    if (run!.code != 0 and run!.code != 1) {
        return Judgment{ found = [<Finding>], failure = "{bin} exited {run!.code}: {hook\firstLine(run!.stderr)}" };
    }
    final found = parseFindings(run!.stdout);
    if (found == null) {
        return Judgment{ found = [<Finding>], failure = "{bin} printed output that is not JSON findings" };
    }
    return Judgment{ found = found! };
}

// noticeReply tells the person, once per session, that replies go unjudged.
// The reply stays unblocked. Only Claude Code and Codex show a Stop notice.
fun noticeReply(host: str, session: str, why: str, fix: str) > str {
    if (!hook\noticeOnce(session, family: "proofread-reply", file: "proofread-reply.buzz")) { return ""; }
    final text = "proofread-reply judges no reply: {why}. {fix}";
    if (host == CLAUDE_CODE or host == CODEX) {
        return json\stringify({"systemMessage": text}) catch "";
    }
    warn(text);
    return "";
}

// lead opens the advice. claude-code reads it beside the next message.
// codex, cursor and opencode start another turn to send the reply again.
fun lead(host: str) > str {
    if (host == CLAUDE_CODE) {
        return "proofread judged your previous reply as agent-reply. These advise: apply "
            + "them in this reply, and do not mention this check or answer it.";
    }
    return "proofread judged your last reply as agent-reply. Send the reply again with these "
        + "fixed, and do not mention this check.";
}

// advice renders found as the text handed back, or "" when found is empty.
fun advice(found: [Finding], host: str) > str {
    if (found.len() == 0) {
        return "";
    }
    final lines = mut [lead(host)];
    foreach (i, f in found) {
        if (i == MAX_FINDINGS) {
            lines.append("- and {found.len() - MAX_FINDINGS} more");
            break;
        }
        var at = "";
        if (f.line > 0) {
            at = " (line {f.line})";
        }
        lines.append("- {f.rule}{at}: {f.message}");
    }
    return lines.join("\n");
}

// shaped wraps text in the reply host takes from a stop hook.
fun shaped(host: str, text: str) > str {
    if (host == CODEX) {
        return json\stringify({"decision": "block", "reason": text}) catch "";
    }
    if (host == CURSOR) {
        return json\stringify({"followup_message": text}) catch "";
    }
    if (host == OPENCODE) {
        return json\stringify({"prompt": text}) catch "";
    }
    return json\stringify({"hookSpecificOutput": {"hookEventName": "UserPromptSubmit", "additionalContext": text}}) catch "";
}

// PENDING_STORE holds claude-code findings until the next prompt.
final PENDING_STORE = "pending.jsonl";

// COMPACT_AFTER is how many lines the store reaches before a take rewrites it.
final COMPACT_AFTER = 64;

// Pending is one store line. An empty printed means the session's
// findings were handed over.
object Pending {
    session: str,
    printed: str,

    fun toJson() > str {
        return json\stringify({"session": this.session, "printed": this.printed}) catch "";
    }
}

fun pendingFrom(line: str) > Pending? {
    final doc = json\parse(line) catch null;
    final session = hook\field(doc, dotPath: "session");
    if (session == "") { return null; }
    return Pending{ session = session, printed = hook\field(doc, dotPath: "printed") };
}

// pendingStore is the store's path in the per-user state dir, or "" when
// there is none.
fun pendingStore() > str {
    final dir = fs\stateDir("proofread-reply") catch "";
    if (dir == "") { return ""; }
    return "{dir}/{PENDING_STORE}";
}

// queue records printed for session's next prompt. It returns why it could
// not, or "".
fun queue(store: str, session: str, printed: str) > str {
    if (session == "") {
        return "the stop carries no session_id, so its findings cannot wait for the next prompt";
    }
    if (store == "") { return "no state dir holds the findings for the next prompt"; }
    final row = Pending{ session = session, printed = printed }.toJson();
    fs\mkdirAll(fs\dirname(store)) catch void;
    try {
        fs\appendFile(store, content: "{row}\n");
    } catch (err: any) {
        return "could not append to {store}: {err}";
    }
    return "";
}

// take returns what waits for session and marks it handed over. The last
// line per session wins.
fun take(store: str, session: str) > str {
    if (store == "" or session == "") { return ""; }
    final lines = fs\readLines(store) catch [<str>];
    final latest = mut {<str: str>};
    foreach (line in lines) {
        final row = pendingFrom(line);
        if (row != null) { latest[row!.session] = row!.printed; }
    }
    final printed = latest[session] ?? "";
    if (printed == "") { return ""; }
    latest[session] = "";
    if (lines.len() + 1 >= COMPACT_AFTER) {
        compact(store, latest: latest);
    } else {
        fs\appendFile(store, content: Pending{ session = session, printed = "" }.toJson() + "\n") catch void;
    }
    return printed;
}

// compact rewrites store to the sessions still waiting, by rename.
fun compact(store: str, latest: {str: str}) > void {
    final kept = mut [<str>];
    foreach (session, printed in latest) {
        if (printed != "") { kept.append(Pending{ session = session, printed = printed }.toJson()); }
    }
    final next = "{store}.next";
    final body = if (kept.len() == 0) "" else kept.join("\n") + "\n";
    fs\writeFile(next, content: body) catch void;
    fs\rename(next, dst: store) catch void;
}

// held is what a stop prints. claude-code's findings go to store instead.
fun held(store: str, host: str, session: str, printed: str) > str {
    if (host != CLAUDE_CODE or printed == "") { return printed; }
    final why = queue(store, session: session, printed: printed);
    if (why != "") { warn(why); }
    return "";
}

// handOver is what a claude-code prompt event prints: its session's findings.
fun handOver(store: str, input: str) > str {
    return take(store, session: hook\field(json\parse(input) catch null, dotPath: "session_id"));
}

// isPrompt reports whether the claude-code event is the person's message.
fun isPrompt(input: str) > bool {
    final doc = json\parse(input) catch null;
    return hook\field(doc, dotPath: "hook_event_name") == "UserPromptSubmit";
}

// output is what the hook prints for stop, or "" for a reply made while continuing, an
// empty reply, or one with nothing to advise.
fun output(stop: Stop, host: str, judged: fun (reply: str) > [Finding]) > str {
    if (stop.again or stop.reply.trim() == "") {
        return "";
    }
    final text = advice(judged(stop.reply), host: host);
    if (text == "") {
        return "";
    }
    return shaped(host, text: text);
}

fun main(args: [str]) > void {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_FLAG, DECISIONS_FLAG, PROOFREAD_FLAG]) catch null;
    if (parsed == null) { return; }
    foreach (word in parsed!.unknown + parsed!.positionals) {
        warn("unsupported argument \"{word}\"; this hook accepts only {AGENT_FLAG}, {DECISIONS_FLAG} and {PROOFREAD_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    final host = parsed!.values[AGENT_FLAG] ?? "";
    if (host == "") {
        warn("[MGS3024] this hook was not given {AGENT_FLAG}, so nothing was judged. "
            + "Merge what `magus describe harness` prints into the host's hook configuration; "
            + "the commands it prints name the host.");
        return;
    }
    if (!knownHost(host)) {
        warn("no reply shape for host \"{host}\"; this hook answers {CLAUDE_CODE}, {CODEX}, {CURSOR} and {OPENCODE}.");
        return;
    }
    var decisions = parsed!.values[DECISIONS_FLAG] ?? "";
    if (decisions == "") { decisions = hook\envOr("__PROOFREAD_DECISIONS", fallback: ""); }

    final input = io\stdin.readAll() catch "";
    if (host == CLAUDE_CODE and isPrompt(input)) {
        final waiting = handOver(pendingStore(), input: input);
        if (waiting != "") { std\print(waiting); }
        return;
    }
    final stop = readStop(input, host: host);
    if (stop.tool != "") {
        warn("the event is a call to tool \"{stop.tool}\", not a stop, so nothing was judged. Wire this hook to the host's stop event.");
        return;
    }
    if (stop.again or stop.reply.trim() == "") { return; }
    final doc = json\parse(input) catch null;
    final session = hook\field(doc, dotPath: "session_id");
    final cwd = hook\field(doc, dotPath: "cwd");
    final root = hook\workspaceRootAt(if (cwd != "") cwd else ".");
    final reader = proofreader(parsed!.values[PROOFREAD_FLAG] ?? "", root: root);

    var printed = "";
    if (reader.why != "") {
        printed = noticeReply(host, session: session, why: reader.why, fix: reader.fix);
    } else {
        final judgment = judge(reader.bin, decisions: decisions, reply: stop.reply);
        if (judgment.failure != "") {
            printed = noticeReply(host, session: session, why: judgment.failure,
                fix: "Run `{reader.bin} agent-reply --stdin` on a reply and read why.");
        } else {
            printed = held(pendingStore(), host: host, session: session,
                printed: output(stop, host: host, judged: judging(judgment.found)));
        }
    }
    if (printed != "") {
        std\print(printed);
    }
}

fun judging(found: [Finding]) > fun (reply: str) > [Finding] {
    return fun (reply: str) > [Finding] => found;
}

final offer = Finding{ rule = "closing-offer", line = 3, message = "End on the result instead of 'Want me to'." };

test "a Stop envelope gives the reply and whether the agent is continuing" {
    final stop = readStop("\{\"last_assistant_message\":\"Done.\",\"stop_hook_active\":true}", host: CLAUDE_CODE);
    std\assert(stop.reply == "Done." and stop.again, message: "{stop.reply} {stop.again}");
    final codex = readStop("\{\"last_assistant_message\":null,\"stop_hook_active\":false}", host: CODEX);
    std\assert(codex.reply == "" and !codex.again, message: "codex sends a null reply for a turn with no message");
    final empty = readStop("not json", host: CLAUDE_CODE);
    std\assert(empty.reply == "" and !empty.again, message: "unreadable input is an empty stop");
}

test "claude-code findings go out on the next prompt, never at Stop" {
    final printed = output(Stop{ reply = "Done.", again = false }, host: CLAUDE_CODE, judged: judging([offer]));
    final doc: {str: any} = json\parse(printed) catch {<str: any>};
    std\assert(doc["decision"] == null, message: "no decision blocks the stop: {printed}");
    final hooked: {str: any} = doc["hookSpecificOutput"] ?? {<str: any>};
    std\assert(hooked["hookEventName"] == "UserPromptSubmit", message: printed);
    final context: str = hooked["additionalContext"] ?? "";
    std\assert(context.indexOf("- closing-offer (line 3): End on the result") != null, message: context);
    std\assert(context.indexOf("do not mention") != null, message: context);
}

test "a queued finding is handed over once, on the next prompt" {
    final store = "{fs\tempDir("proofread-reply-store-")}/{PENDING_STORE}";
    std\assert(queue(store, session: "s1", printed: "first") == "", message: "queued");
    std\assert(queue(store, session: "s1", printed: "second") == "", message: "queued again");
    std\assert(queue(store, session: "s2", printed: "other") == "", message: "another session");
    std\assert(take(store, session: "s1") == "second", message: "a later stop replaces an earlier one");
    std\assert(take(store, session: "s1") == "", message: "handed over once");
    std\assert(take(store, session: "s2") == "other", message: "sessions keep their own");
    std\assert(take(store, session: "s3") == "", message: "nothing queued is nothing");
    fs\removeAll(fs\dirname(store));
}

test "the store compacts to what is still queued" {
    final store = "{fs\tempDir("proofread-reply-store-")}/{PENDING_STORE}";
    for (var i = 0; i < COMPACT_AFTER + 2; i = i + 1) {
        std\assert(queue(store, session: "s{i}", printed: "p{i}") == "", message: "queued s{i}");
        std\assert(take(store, session: "s{i}") == "p{i}", message: "took s{i}");
    }
    std\assert(queue(store, session: "kept", printed: "waiting") == "", message: "queued kept");
    final lines = fs\readLines(store) catch [<str>];
    std\assert(lines.len() <= COMPACT_AFTER, message: "{lines.len()} lines");
    std\assert(take(store, session: "kept") == "waiting", message: "compaction keeps what waits");
    fs\removeAll(fs\dirname(store));
}

test "a stop with no session id cannot queue its findings" {
    final store = "{fs\tempDir("proofread-reply-store-")}/{PENDING_STORE}";
    std\assert(queue(store, session: "", printed: "lost").indexOf("session_id") != null, message: "says why");
    fs\removeAll(fs\dirname(store));
}

test "a claude-code stop prints nothing and its next prompt prints the findings" {
    final store = "{fs\tempDir("proofread-reply-store-")}/{PENDING_STORE}";
    final printed = output(Stop{ reply = "Done.", again = false }, host: CLAUDE_CODE, judged: judging([offer]));
    std\assert(held(store, host: CLAUDE_CODE, session: "s1", printed: printed) == "", message: "the stop is silent");
    final prompt = `{"hook_event_name": "UserPromptSubmit", "session_id": "s1", "prompt": "next"}`;
    std\assert(handOver(store, input: prompt) == printed, message: "the prompt carries them");
    std\assert(handOver(store, input: prompt) == "", message: "and only once");
    std\assert(held(store, host: CODEX, session: "s1", printed: "x") == "x", message: "other hosts print at stop");
    fs\removeAll(fs\dirname(store));
}

test "a claude-code prompt event is told apart from its stop" {
    std\assert(isPrompt(`{"hook_event_name": "UserPromptSubmit", "prompt": "hi"}`), message: "prompt");
    std\assert(!isPrompt(`{"hook_event_name": "Stop", "last_assistant_message": "Done."}`), message: "stop");
}

test "codex findings continue the turn with a block decision and a reason" {
    final printed = output(Stop{ reply = "Done.", again = false }, host: CODEX, judged: judging([offer]));
    final doc: {str: any} = json\parse(printed) catch {<str: any>};
    std\assert(doc["decision"] == "block", message: printed);
    std\assert(doc["hookSpecificOutput"] == null, message: "Codex's stop output takes no hookSpecificOutput: {printed}");
    final reason: str = doc["reason"] ?? "";
    std\assert(reason.indexOf("- closing-offer (line 3): End on the result") != null, message: reason);
    std\assert(reason.indexOf("Send the reply again") != null, message: reason);
}

test "cursor findings go back as a followup message" {
    final printed = output(Stop{ reply = "Done.", again = false }, host: CURSOR, judged: judging([offer]));
    final doc: {str: any} = json\parse(printed) catch {<str: any>};
    final message: str = doc["followup_message"] ?? "";
    std\assert(message.indexOf("- closing-offer (line 3): End on the result") != null, message: printed);
    std\assert(doc["decision"] == null and doc["hookSpecificOutput"] == null, message: printed);
}

test "opencode findings go back as a prompt to send again" {
    final printed = output(Stop{ reply = "Done.", again = false }, host: OPENCODE, judged: judging([offer]));
    final doc: {str: any} = json\parse(printed) catch {<str: any>};
    final prompt: str = doc["prompt"] ?? "";
    std\assert(prompt.indexOf("- closing-offer (line 3): End on the result") != null, message: printed);
    std\assert(prompt.indexOf("Send the reply again") != null, message: prompt);
    std\assert(doc["decision"] == null and doc["hookSpecificOutput"] == null and doc["followup_message"] == null, message: printed);
}

test "a tool call is not a stop on any host" {
    foreach (host in [CLAUDE_CODE, CODEX, CURSOR, OPENCODE]) {
        final stop = readStop(`{"session_id": "s1", "tool_name": "Bash", "tool_input": {"command": "git stash"}}`, host: host);
        std\assert(stop.tool == "Bash" and stop.reply == "" and !stop.again, message: "{host}: {stop.tool}");
    }
    std\assert(readStop("\{\"last_assistant_message\":\"Done.\"}", host: CODEX).tool == "", message: "a stop names no tool");
}

test "an opencode idle envelope is read like codex's stop" {
    final stop = readStop("\{\"last_assistant_message\":\"Done.\",\"stop_hook_active\":true}", host: OPENCODE);
    std\assert(stop.reply == "Done." and stop.again, message: "{stop.reply} {stop.again}");
}

test "a reply made while continuing is not judged, so the agent cannot loop" {
    foreach (host in [CLAUDE_CODE, CODEX, CURSOR, OPENCODE]) {
        final printed = output(Stop{ reply = "Done.", again = true }, host: host, judged: judging([offer]));
        std\assert(printed == "", message: "{host}: {printed}");
    }
}

test "nothing to advise prints nothing" {
    std\assert(output(Stop{ reply = "Done.", again = false }, host: CODEX, judged: judging([<Finding>])) == "",
        message: "no findings");
    std\assert(output(Stop{ reply = "  ", again = false }, host: CODEX, judged: judging([offer])) == "", message: "no reply");
}

test "advice is capped at MAX_FINDINGS and counts the rest" {
    final many = mut [<Finding>];
    for (var i = 0; i < MAX_FINDINGS + 2; i = i + 1) {
        many.append(offer);
    }
    final text = advice(many, host: CLAUDE_CODE);
    std\assert(text.split("\n").len() == MAX_FINDINGS + 2, message: text);
    std\assert(text.endsWith("- and 2 more"), message: text);
}

test "proofread's findings are read with their line" {
    final found = parseFindings("[\{\"source\":\"stdin:4\",\"rule\":\"bold-label\",\"message\":\"Open.\"}]") ?? [<Finding>];
    std\assert(found.len() == 1 and found[0].rule == "bold-label" and found[0].line == 4, message: "{found.len()}");
    std\assert(parseFindings("not json") == null, message: "unreadable output is not a clean pass");
    std\assert((parseFindings("[]") ?? [offer]).len() == 0, message: "an empty list is a clean pass");
}

test "a binary that is missing or fails says why it judged nothing" {
    final missing = judge("/nonexistent/proofread", decisions: "", reply: "Done.");
    std\assert(missing.failure.indexOf("/nonexistent/proofread") != null, message: missing.failure);
    final named = proofreader("/nonexistent/proofread", root: "");
    std\assert(named.why.indexOf("not an executable") != null and named.fix == RELEASE_FIX, message: named.why);
}

test "the notice reaches the person once per session and never blocks" {
    final session = "proofread-reply-{std\random(min: 0, max: 1000000)}";
    final first: {str: any} = json\parse(noticeReply(CLAUDE_CODE, session: session, why: "w", fix: "f")) catch {<str: any>};
    std\assert(first["systemMessage"] == "proofread-reply judges no reply: w. f", message: "{first}");
    std\assert(first["decision"] == null, message: "the reply is never blocked");
    std\assert(noticeReply(CLAUDE_CODE, session: session, why: "w", fix: "f") == "", message: "once per session");
}

test "a checkout's own proofread is checked for staleness by its magus" {
    final root = fs\tempDir("proofread-reply-root-");
    fs\mkdirAll("{root}/libs/proofread");
    fs\writeFile("{root}/{WORKSPACE_MODULE}", content: "");
    fs\writeFile("{root}/magus.yaml", content: "");
    final reader = proofreader("", root: root);
    std\assert(reader.bin == "{root}/{WORKSPACE_BINARY}", message: reader.bin);
    std\assert(reader.why != "", message: "a checkout with no runnable magus or build is never a clean pass");
    fs\removeAll(root);
}

test "only the hosts with a reply shape are known" {
    std\assert(knownHost(CLAUDE_CODE) and knownHost(CODEX) and knownHost(CURSOR) and knownHost(OPENCODE), message: "wired hosts");
    std\assert(!knownHost("windsurf") and !knownHost(""), message: "a host with no shape");
}

test "a cursor stop that is a follow-up or did not complete judges nothing" {
    final looped = readStop("\{\"status\":\"completed\",\"loop_count\":1}", host: CURSOR);
    std\assert(looped.again, message: "a follow-up turn is a continuation");
    final aborted = readStop("\{\"status\":\"aborted\",\"loop_count\":0,\"transcript_path\":\"/nonexistent\"}", host: CURSOR);
    std\assert(aborted.reply == "" and !aborted.again, message: "an aborted turn has no reply");
}

test "cursor's reply is the last assistant message with text, up to the last user message" {
    final transcript = fs\tempFile("proofread-reply");
    final rows = [
        `{"role":"user","message":{"content":[{"type":"text","text":"question"}]}}`,
        `{"role":"assistant","message":{"content":[{"type":"text","text":"Looking."},{"type":"tool_use","name":"Read"}]}}`,
        `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
        `{"role":"assistant","message":{"content":[{"type":"text","text":"Final answer."}]}}`,
    ];
    fs\writeFile(transcript, content: rows.join("\n") + "\n");
    std\assert(lastAssistantText(transcript) == "Final answer.", message: lastAssistantText(transcript));
    fs\writeFile(transcript, content: rows[0] + "\n");
    std\assert(lastAssistantText(transcript) == "", message: "a turn with no assistant message has no reply");
    std\assert(lastAssistantText("/nonexistent/t.jsonl") == "", message: "a missing transcript has no reply");
    fs\remove(transcript);
}
```

## `magus-buzz-check.buzz`

Wire it after the tools that write a file (Claude Code's `PostToolUse` on
`Write|Edit|MultiEdit`, Codex's on `apply_patch`, Cursor's `postToolUse` on
`Write|StrReplace|Edit`) as `magus buzz -s magus-buzz-check.buzz -- --agent-name <host>`.
It runs `magus buzz --check` on a `.buzz` file the agent just wrote, so a type error
or a conflict marker comes back at edit time rather than at the next run. The harness
spells declare it through `magus/harness.buzzCheckHook`.

It takes the written path from `tool_input.file_path`, else `tool_input.path`, else
the Add File and Update File lines of an `apply_patch` input, and skips any path that
is not a `.buzz` file. A `magusfile.buzz`, and anything under `magusfiles/`, `spells/`
or `hack/`, is checked with `--embedded`, the mode the engine loads it in; any other
`.buzz` file is checked strict. A failure prints `{"decision":"block","reason":...}`,
which the host hands the agent as the reason to fix the file. A pass prints nothing,
and it always exits 0.

It declares no `magus-guard-coverage` line: it judges a file the agent already wrote,
not a call.

```buzz
// magus Buzz check hook: runs `magus buzz --check` on a .buzz file the agent just wrote, so a
// type error or a conflict marker comes back at edit time rather than at the next run.
//
// Run it as `magus buzz -s magus-buzz-check.buzz`; see magus-command.buzz for why `-s` is
// load-bearing. Wire it after the tools that write a file (Claude Code's PostToolUse on
// Write|Edit|MultiEdit, Codex's on apply_patch, Cursor's postToolUse on Write|StrReplace|Edit);
// the harness spells declare it through magus/harness.buzzCheckHook.
//
// Contract: reads the host's event as JSON on stdin and takes the written path from
// tool_input.file_path, else tool_input.path, else the Add File and Update File lines of an
// apply_patch input. A path that is not a .buzz file is skipped, and so
// is a magusfile.buzz: the check cannot bind its magus/spell/* imports.
// An event that names no written file is named on stderr, since the hook
// is wired after a tool that writes none. Anything under magusfiles/,
// spells/ or hack/ is checked with --embedded, the mode the engine loads it
// in; any other .buzz file is checked strict. A
// failure prints {"decision":"block","reason":...} on stdout, which the host hands the agent
// as the reason to fix the file; a pass prints nothing. It always exits 0.
// A check that could not run blocks too, naming the binary and why.
//
// REQUIRED argument: `-- --agent-name <host>`, named like every glue entry. Without it the
// check still runs, and MGS3024 goes to stderr.
//
// NO magus-guard-coverage line: it judges a file the agent already wrote, not a call.
//
// magus-guard-template: 36

import "io";
import "flags";
import "fs";
import "path";
import "encoding/json";
import "proc";
import "std";
import "lib/hook" as hook;

final AGENT_NAME_FLAG = "--agent-name";

fun warn(message: str) > void {
    io\stderr.write("magus-buzz-check.buzz: {message}\n") catch void;
}

// readAgentName reads the host off this script's argv, "" when the entry names none.
fun readAgentName(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME_FLAG]) catch null;
    if (parsed == null) { return ""; }
    foreach (word in parsed!.unknown + parsed!.positionals) {
        warn("unsupported argument \"{word}\"; this hook takes only {AGENT_NAME_FLAG} <host>. Fix the hook command in your host config.");
    }
    return parsed!.values[AGENT_NAME_FLAG] ?? "";
}

// writtenPaths is every path the event says the tool wrote.
fun writtenPaths(event: any?) > [str] {
    final paths = mut [<str>];
    foreach (key in ["tool_input.file_path", "tool_input.path"]) {
        final value = hook\dig(event, dotPath: key);
        if (value -> path) {
            if ((path as? str) -> p) {
                if (p != "") {
                    paths.append(p);
                    return paths;
                }
            }
        }
    }
    foreach (key in ["tool_input.command", "tool_input.input", "tool_input.patch"]) {
        final value = hook\dig(event, dotPath: key);
        if (value == null) { continue; }
        final text = (value as? str) ?? "";
        foreach (line in text.split("\n")) {
            foreach (marker in ["*** Add File: ", "*** Update File: "]) {
                if (line.startsWith(marker)) { paths.append(line.sub(marker.len(), len: line.len() - marker.len()).trim()); }
            }
        }
    }
    return paths;
}

// unjudged is the stderr note for an event that names no written file.
fun unjudged(event: any?) > str {
    final tool = hook\field(event, dotPath: "tool_name");
    final source = if (tool != "") "tool \"{tool}\"" else "the event";
    return "{source} names no written file, so nothing was checked. Wire this hook after the tools that write a file.";
}

// fileWorkspace is the workspace an absolute path lies in, else session.
// A subagent's cwd is the session's checkout while it writes in its own.
fun fileWorkspace(path: str, session: str) > str {
    if (!path.startsWith("/")) { return session; }
    final found = hook\workspaceRootAt(fs\dirname(path));
    return if (found != "") found else session;
}

// embedded reports whether the engine loads rel in embedded mode: the spells,
// magusfiles and hack scripts.
fun embedded(rel: str) > bool {
    foreach (prefix in ["magusfiles/", "spells/", "hack/"]) {
        if (rel.startsWith(prefix)) { return true; }
    }
    return false;
}

fun main(args: [str]) > void {
    if (readAgentName(args) == "") {
        warn("[MGS3024] this hook was not given --agent-name. Merge what `magus describe harness` prints into the host's hook configuration.");
    }
    final event = json\parse(io\stdin.readAll() catch "") catch null;
    final written = writtenPaths(event);
    if (written.len() == 0) {
        warn(unjudged(event));
        return;
    }
    final cwd = hook\field(event, dotPath: "cwd");
    final root = hook\workspaceRootAt(if (cwd != "") cwd else ".");
    final bin = hook\resolveBin(cwd: cwd);
    final reply = blockReply(checkAll(bin, written: written, root: root));
    if (reply != "") { io\stdout.write(reply + "\n") catch void; }
}

// checkAll checks every written .buzz file and returns each failure. A check
// that could not run is a failure too, so the agent learns it did not happen.
// A magusfile.buzz is skipped; the header says why.
fun checkAll(bin: str, written: [str], root: str) > [str] {
    final buzz = written.filter(fun (index: int, path: str) > bool => path.endsWith(".buzz") and fs\basename(path) != "magusfile.buzz");
    if (buzz.len() == 0) { return [<str>]; }
    if (bin == "" or !hook\isExecutable(bin)) {
        final named = if (bin == "") "magus" else bin;
        return ["magus-buzz-check could not run `{named}`: no runnable magus was found, "
            + "so {buzz.join(", ")} went unchecked. {hook\magusFix(root)}"];
    }
    final failures = mut [<str>];
    foreach (file in buzz) {
        final fileRoot = fileWorkspace(file, session: root);
        final check = mut ["buzz", "--check"];
        final dir = if (fileRoot != "") fileRoot else ".";
        var result: proc\ExecResult? = null;
        var raised = "";
        try {
            final rel = if (path\within(fileRoot, path: file)) path\rel(fileRoot, target: file) else file;
            if (embedded(rel)) { check.append("--embedded"); }
            check.append(rel);
            result = proc\exec(bin, args: check, dir: dir, opts: proc\ExecOptions{quiet = true, allowFailure = true});
        } catch (err: any) {
            raised = "{err}";
        }
        if (result == null) {
            failures.append("magus-buzz-check could not run `{bin} {check.join(" ")}`: {raised}");
        } else if (!result!.ok) {
            failures.append("`magus {check.join(" ")}` failed:\n" + (result!.stdout + result!.stderr).trim()
                + "\nFix the file before you go on.");
        }
    }
    return failures;
}

// blockReply is the host's block for failures, "" when there are none.
fun blockReply(failures: [str]) > str {
    if (failures.len() == 0) { return ""; }
    return json\stringify({"decision": "block", "reason": failures.join("\n\n")}) catch "";
}

test "a written path comes from the field the host names it in" {
    std\assert(writtenPaths({"tool_input": {"file_path": "/r/spells/x/spell.buzz"}}).join(",") == "/r/spells/x/spell.buzz", message: "Claude Code");
    std\assert(writtenPaths({"tool_input": {"path": "a.buzz"}}).join(",") == "a.buzz", message: "Cursor");
    std\assert(writtenPaths({"tool_input": {"command": "*** Begin Patch\n*** Update File: hack/a.buzz\n@@\n*** Add File: b.go\n"}}).join(",")
        == "hack/a.buzz,b.go", message: "an apply_patch input");
    std\assert(writtenPaths({"tool_input": {}}).len() == 0, message: "nothing written");
}

test "an event that names no written file is answered with a note naming its tool" {
    final shell = {"session_id": "s1", "tool_name": "Bash", "tool_input": {"command": "git stash"}};
    std\assert(writtenPaths(shell).len() == 0, message: "a shell call writes nothing");
    std\assert(unjudged(shell).startsWith("tool \"Bash\" names no written file"), message: unjudged(shell));
    std\assert(unjudged(null).startsWith("the event names no written file"), message: unjudged(null));
}

test "the engine's own Buzz is checked embedded and everything else strict" {
    std\assert(embedded("spells/harness/codex/spell.buzz") and embedded("hack/dev/a.buzz") and embedded("magusfiles/a.buzz"), message: "spells and scripts");
    std\assert(!embedded("docs/guides/integrations/agents/magus-command.buzz"), message: "a standalone script is strict");
    std\assert((path\rel("/r", target: "/r/spells/a.buzz") catch "") == "spells/a.buzz", message: "under the root");
}

test "a written file is read against its own workspace, not the session's" {
    final spell = "{hook\workspaceRoot()}/spells/golang/spell.buzz";
    final rel = path\rel(fileWorkspace(spell, session: "/elsewhere"), target: spell) catch spell;
    std\assert(embedded(rel), message: rel);
    std\assert(fileWorkspace("spells/a.buzz", session: "/elsewhere") == "/elsewhere", message: "a relative path stays the session's");
    std\assert(fileWorkspace("/nowhere/a.buzz", session: "/elsewhere") == "/elsewhere", message: "no workspace above the path");
}

test "no runnable magus blocks with a reason naming the binary" {
    final failures = checkAll("/nonexistent/magus", written: ["a.buzz", "b.go"], root: "");
    std\assert(failures.len() == 1, message: failures.join("|"));
    std\assert(failures[0].indexOf("could not run `/nonexistent/magus`") != null, message: failures[0]);
    std\assert(failures[0].indexOf("a.buzz went unchecked") != null, message: failures[0]);
    std\assert(blockReply(failures).indexOf("\"decision\":\"block\"") != null, message: blockReply(failures));
}

test "a magusfile is skipped, so it neither runs the check nor needs magus" {
    std\assert(checkAll("/nonexistent/magus", written: ["magusfile.buzz", "libs/x/magusfile.buzz"], root: "").len() == 0, message: "magusfiles are skipped");
    final failures = checkAll("/nonexistent/magus", written: ["libs/x/magusfile.buzz", "a.buzz"], root: "");
    std\assert(failures.len() == 1 and failures[0].indexOf("a.buzz went unchecked") != null, message: failures.join("|"));
}

test "nothing written in Buzz needs no magus and blocks nothing" {
    std\assert(checkAll("", written: ["b.go"], root: "").len() == 0, message: "no .buzz file");
    std\assert(blockReply([<str>]) == "", message: "no failures");
}
```

## `magus-reclaim-disk.buzz`

Wire it to the event your host raises once a turn (Claude Code's and Codex's
`UserPromptSubmit`, Cursor's `beforeSubmitPrompt`; the OpenCode plugin runs it when a
session goes idle) as `magus buzz -s magus-reclaim-disk.buzz -- --agent-name <host>`. The
harness spells declare it through `magus/harness.reclaimDiskHook`.

It catches a disk filling up before it wedges the machine, and needs no daemon. On most
turns it costs one stat: the free-space store at `$XDG_STATE_HOME/magus/disk/samples.jsonl`
was written less than a minute ago. Past that it says what the store holds that nobody was
told, and starts one `reclaimdisk\tick` detached. `magus job fork` and `magus run` start
the same tick.

The tick decides. Below `disk.min_free_mb` it trims each cache a spell gives a budget,
keeping files modified within a day; below `disk.critical_free_mb`, within 6 hours. Below
`disk.min_free_mb`, or losing space faster than `disk.growth_mb_per_hour`, it removes the
two removable worktrees untouched longest and the agent branches nothing needs. It never
removes a worktree holding uncommitted files, a commit no remote has, a live job, a working
process or a lock. `magus doctor` shows the trend and the last results.

It prints what a tick really did, after the tick did it, and otherwise nothing. `--say
stderr` sends the lines to stderr, for a host that reads stdout as a verdict. It always
exits 0, and declares no `magus-guard-coverage` line: it judges no call.

```buzz
// magus reclaim-disk hook: notices a disk filling up early and takes space back in small
// chunks, without the daemon.
//
// Run it as `magus buzz -s magus-reclaim-disk.buzz`; see magus-command.buzz for why `-s`
// is load-bearing. It imports diskwatch, which imports no magus module, and no spell, so
// the workspace stays CLOSED: wire it to an event that fires every turn and a turn pays
// one stat for it.
//
// A thin caller. A store written within the last minute ends the hook at that
// stat. Past it, the hook says what the store holds that nobody was told, and
// starts magus/reclaimdisk's tick detached. The tick decides and acts.
//
// Contract: prints the lines the store holds, written from what a tick really
// did; otherwise nothing. It always exits 0. `--say stderr` sends them to
// stderr, for a host that reads stdout as a verdict. Override:
//
//   __MAGUS_BIN   path to the binary, when it is not on PATH
//
// REQUIRED argument: `-- --agent-name <host>`, which the configuration
// `magus describe harness` prints puts on the command. Without it nothing is sampled and
// MGS3024 goes to stderr.
//
// NO magus-guard-coverage line: it carries no verdict on any input.
//
// magus-guard-template: 36

import "io";
import "std";
import "os";
import "fs";
import "flags";
import "proc";
import "encoding/json";
import "diskwatch";
import "lib/hook" as hook;

final AGENT_NAME_FLAG = "--agent-name";
final SAY_FLAG = "--say";

fun warn(message: str) > void {
    io\stderr.write("magus-reclaim-disk.buzz: {message}\n") catch void;
}

object Args {
    agentName: str,
    say: str,
}

fun readArgs(args: [str]) > Args {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME_FLAG, SAY_FLAG]) catch null;
    if (parsed == null) { return Args{ agentName = "", say = "stdout" }; }
    foreach (word in parsed!.unknown + parsed!.positionals) {
        warn("unsupported argument \"{word}\"; this hook accepts only {AGENT_NAME_FLAG} and {SAY_FLAG}. "
            + "Fix the hook command in your host config.");
    }
    final say = parsed!.values[SAY_FLAG] ?? "stdout";
    if (say != "stdout" and say != "stderr") {
        warn("{SAY_FLAG} takes stdout or stderr, not \"{say}\"; saying it on stdout.");
        return Args{ agentName = parsed!.values[AGENT_NAME_FLAG] ?? "", say = "stdout" };
    }
    return Args{ agentName = parsed!.values[AGENT_NAME_FLAG] ?? "", say = say };
}

fun lastWrite(store: str) > double? {
    final info = fs\stat(store) catch null;
    if (info == null) { return null; }
    return info!.mtime;
}

// tickSnippet is the detached child's program. The cwd goes by flag, so nothing
// an event carries reaches the source.
fun tickSnippet() > str {
    return "import \"os\"; import \"magus/reclaimdisk\" as reclaim; _ = reclaim\\tick(\".\", deadline: os\\time() + reclaim\\CHUNK_MS);";
}

// pending says what the store holds that nobody was told, and marks it told.
fun pending(store: str, now: double) > str? !> any {
    final samples = diskwatch\read(store);
    final said = diskwatch\pendingLine(samples, now: now);
    if (said != null) {
        diskwatch\append(store, s: diskwatch\DiskSample{ at = now, path = "", action = "said" });
    }
    return said;
}

fun main(args: [str]) > void {
    final a = readArgs(args);
    if (a.agentName == "") {
        warn("[MGS3024] this hook was not given --agent-name, so nothing was sampled. "
            + "Merge what `magus describe harness` prints into the host's hook configuration; "
            + "the commands it prints name the host.");
        return;
    }
    final event = io\stdin.readAll() catch "";
    var cwd = hook\field(json\parse(event) catch null, dotPath: "cwd");
    if (cwd == "") { cwd = "."; }

    final store = diskwatch\storePath() catch "";
    if (store == "") { return; }
    final now = os\time();
    // The cheap path every turn takes: one stat, and nothing else, inside the floor.
    if (!diskwatch\due(lastWrite(store), now: now, everyMs: diskwatch\FLOOR_MS)) { return; }

    // An absent magus samples nothing: guessed thresholds are numbers
    // nobody set.
    final bin = hook\resolveBin(cwd: cwd);
    if (bin == "" or !hook\isExecutable(bin)) {
        final host = if (a.say == "stderr") "" else a.agentName;
        final doc = json\parse(event) catch null;
        io\stdout.write(hook\unrecordedNotice(bin, session: hook\field(doc, dotPath: "session_id"),
            host: host, file: "magus-reclaim-disk.buzz", root: hook\workspaceRootAt(cwd))) catch void;
        return;
    }

    var said: str? = null;
    try {
        said = pending(store, now: now);
        _ = proc\exec(bin, args: ["buzz", "-C", cwd, "-e", tickSnippet()], opts: proc\ExecOptions{ detach = true });
    } catch (e) {
        warn("disk sample failed: {e}");
        return;
    }
    if (said -> text) {
        if (a.say == "stderr") {
            io\stderr.write(text + "\n") catch void;
        } else {
            std\print(text);
        }
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
