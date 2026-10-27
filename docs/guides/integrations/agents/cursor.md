---
title: Cursor
description: Wiring magus into Cursor (AGENTS.md for guidance, one hook script for its wired events, the opt-in reply check on stop, and what Cursor's contract cannot express).
tags: [agents, cursor, AGENTS.md, guard, hooks]
---

# Cursor

Cursor reads an `AGENTS.md` at the repository root and Agent Skills from
`.agents/skills`, `.cursor/skills` and `.claude/skills`, and it runs hooks as
programs with the event on stdin. One self-contained script covers every event
magus uses, so installing the guard is a single download. The reply check on
`stop` is a separate script you wire on purpose; magus does not ship it in the
hook config.

| what            | where                                                                          |
| --------------- | ------------------------------------------------------------------------------ |
| always-on rules | `AGENTS.md` (you paste the block; magus never writes it)                       |
| skills          | `.agents/skills`, short and full forms                                         |
| guard wiring    | `.cursor/hooks.json`                                                           |
| quiet output    | not set: Cursor has no env setting; export `MAGUS_LOG_SILENT=true` yourself    |
| shell commands  | `preToolUse`: deny and advise reach the model; the command runs as sent        |
| file writes     | deny and advise both reach the model                                           |
| MCP calls       | `beforeMCPExecution` (deny/ask), advise on `postToolUse`                       |
| failed calls    | `postToolUseFailure` carries the call's advice                                 |
| session start   | `sessionStart` (`env` + size-budgeted `session --brief`)                       |
| compaction      | `preCompact`; the next `postToolUse` carries the brief again                   |
| checkpoint      | `sessionEnd`                                                                   |
| your messages   | `beforeSubmitPrompt`, recorded and always let through (not under `-p`)         |
| skill loads     | the `Read` of a `SKILL.md` on `preToolUse`                                     |
| subagent spawns | `preToolUse` (`Task`), judged as a spawn                                       |
| reply check     | opt-in: `stop`, a `followup_message` Cursor submits as another turn (not `-p`) |
| scout agent     | `.cursor/agents/magus-scout.md`, `readonly: true`                              |
| session load    | [`magus-session-load-cursor.buzz`](session-load.md#cursor)                     |
| MCP             | [MCP](../mcp.md)                                                               |

## Skills

`magus agent install` writes the skills to `.agents/skills` in both forms, the
short skill and its full twin, as for Codex. cursor-agent 2026.10.01 read a
`SKILL.md` from each of `.agents/skills`, `.cursor/skills` and `.claude/skills`
in a probe, so the one install serves Cursor and Codex. It also prints the managed
magus block when your `AGENTS.md` is missing it or carrying a stale one, and you
paste it in. [Skills](skills.md) covers the block, its stamp, and the drift check
that grades it.

Cursor has no skill tool: an agent loads a skill by reading its `SKILL.md`. That
`Read` reaches `preToolUse`, and the script hands it to `magus shell` as the read
it is, which magus records as the load. The spell's limits say this host's loads are seen, and
the rules that require a skill load hold here as they do on Claude Code.

`magus agent harness install` fills `.cursor/magus/tools` with the spell's tool rows, its
limits and a host file naming cursor, where magus finds them by the entry's `--agent-name`. A
tool the rows list as touching nothing passes before any rule loads.

An `advise` verdict reaches the model here too, on `postToolUse` rather than at
the moment the call is gated; that guidance being in `AGENTS.md` as well is the
same belt-and-braces every host gets, not a substitute for it.

### No `.cursor/rules` file

Cursor's always-on prose is `AGENTS.md`, so magus writes no `.cursor/rules/*.mdc`
for its own behavior. Cursor's built-in Grep, Glob and Read never reach
`beforeShellExecution`, and a rules file would only ask the model to search
differently. The hook sends Grep's native input to the shared guard, retaining its
path, file filters, output mode and context options. Glob and Read are restated as
the shell shapes the guard already judges. Advice reaches the model on `postToolUse`. A rules
file beside that is host glue that `magus agent harness verify` cannot see and
`magus describe harness` does not print, and it vanishes the next time someone
treats `.cursor/` as disposable host state.

Grep follows the same rules as a shell search: an indexed symbol search across
the tree is refused with `magus refs`; a declaration lookup with context lines
is refused with a command that reads that declaration. Known graph entities are
routed to their equivalent queries. Literal prose and regex searches the graph
cannot answer still run. A native tool name alone is no reason to refuse a search.

## MCP

MCP client config is yours. `magus describe harness cursor` only prints a short hint
(and a docs pointer); Magus does not write `.cursor/mcp.json`.

```sh
magus server start
magus config mcp connector create --name cursor --expires 366d   # shown once: store it as MAGUS_MCP_TOKEN
magus describe harness cursor   # prints the hook entries, their merge command, and the MCP hint
```

Then register Magus under Cursor Settings -> Tools & MCP (or hand-write
`.cursor/mcp.json` / `~/.cursor/mcp.json`). Endpoint
`http://127.0.0.1:7391/mcp`; bind the token with `${env:MAGUS_MCP_TOKEN}` if you
prefer env interpolation. Restart Cursor after changing the client config.
`magus status --probe=mcp` must report serving. Full notes: [MCP](../mcp.md).
An agent uses the CLI fallback when MCP is unavailable; it does not manually
start Magus solely to obtain tools.

## Quiet output

`MAGUS_LOG_SILENT=true` is `-s` on every magus command: verdicts, refs and failures,
without each reason or a wait note under a minute. Cursor has no setting for the
environment of the commands its agent runs, and the `env` a `sessionStart` reply
carries reaches later hooks only. So `.cursor/hooks.json` sets nothing, and the
guard hands every shell command back as Cursor sent it: magus does not transform
what a host sends. To quiet magus there, set `MAGUS_LOG_SILENT=true` in the
environment that starts Cursor.

## Guard hook

Prefer wiring the Cursor harness from the root magusfile when you bounce between
hosts; `magus describe harness` then covers every wired provider:

```buzz
import "magus";
import "ghcr.io/egladman/magus/spells/harness/cursor";
magus\harness.provider(cursor);
```

Declare the spell in `magus.yaml` with the tag cd's `spell-publish` step pushed, and
run your lock target with `:update` to pin its digest in `magus.lock`, so the harness
versions apart from your magus binary; see
[Remote spells](../../../reference/remote-spells.md).

```sh
magus describe harness
magus agent harness verify
```

`magus describe harness` prints each entry the host files lack and the one command
that merges them. magus never writes them: read the command, run it yourself, then
verify.

To adapt that Buzz harness without modifying Magus source: copy the spell into
the workspace, change only the import path (for example
`import "harness/cursor" as cursor`), edit the workspace Buzz, then describe, merge,
and verify again. Details:
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md) and
[Recurring guard friction](guard.md#recurring-guard-friction).

Or target Cursor alone:

```sh
magus describe harness cursor
magus agent harness verify --id cursor
```

That prints opaque fragments naming
`magus buzz -s docs/guides/integrations/agents/cursor-hook.buzz -- --agent-name cursor`.
The script takes the host's name from that argument and nowhere else, and refuses a call
without it ([MGS3024](../../../reference/codes/sandbox/MGS3024.md)). It needs neither a
POSIX shell nor `jq`. A portable install copies the script to `.cursor/hooks/cursor-hook.buzz`
and points every event at that copy, name included:

```json
{
  "version": 1,
  "hooks": {
    "sessionStart": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "timeout": 10 }],
    "beforeShellExecution": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "failClosed": true, "timeout": 10 }],
    "beforeMCPExecution": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "failClosed": true, "timeout": 10 }],
    "preToolUse": [{ "matcher": "*", "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "failClosed": true, "timeout": 10 }],
    "postToolUse": [
      { "matcher": "Shell|Write|StrReplace|Delete|Edit|NotebookEdit|Grep|Glob|Read|WebSearch|WebFetch|MCP:.*", "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "timeout": 10 }
    ],
    "postToolUseFailure": [
      { "matcher": "Shell|Write|StrReplace|Delete|Edit|NotebookEdit|Grep|Glob|Read|WebSearch|WebFetch|MCP:.*", "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "timeout": 10 }
    ],
    "preCompact": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "timeout": 10 }],
    "subagentStart": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "timeout": 10 }],
    "sessionEnd": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "timeout": 10 }],
    "beforeSubmitPrompt": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor", "timeout": 10 }]
  }
}
```

The reply check is a second script, [`proofread-reply.buzz`](guard-templates.md#proofread-replybuzz),
that you add on purpose; see [Judging the reply a turn ends with](#judging-the-reply-a-turn-ends-with).

It is deliberately self-contained rather than delegating to the shared
templates: needing five files to install a guard is how a guard ends up not
installed. The script branches on the `hook_event_name` every Cursor hook
carries, so one file serves all of them.

Cursor splits across two events what every other host delivers from one, and
that is the thing to read before changing this wiring. A gating event
(`preToolUse`, `beforeShellExecution`, `beforeMCPExecution`) carries a `deny`,
because Cursor sends `user_message` and `agent_message` only with a denial. An
`advise` therefore has no channel there and needs the `additional_context` of
`postToolUse` or `postToolUseFailure`, which arrive after the call. So the gating
event judges the call once and files the verdict under the conversation, the
`tool_use_id` and the tool name, in `$XDG_STATE_HOME/magus/cursor-hook`
(`CURSOR_HOOK_STATE_DIR` overrides it), and the after-call event answers from that
file without asking magus again. One call leaves one row in the activity trail,
as on every other host. The tool name is part of the key because Cursor reuses one
`tool_use_id` for a `Read` and the `Write` after it (probed on cursor-agent
2026.10.01). A filed verdict whose after-call event never came is removed after a
day.

The shell is gated on `preToolUse`, and a command that passes runs exactly as Cursor
sent it. Cursor already gives a command empty stdin (probed live), and magus
transforms nothing a host sends, on this host or any other. `beforeShellExecution` then sees the same line, recognizes it as already
judged, and allows it; it judges only a command `preToolUse` never saw, which keeps
it as the fallback.

A push at a commit no passing gate covers gets the verdict `ask`, and
`preToolUse` answers `permission: "ask"`: Cursor shows you the reason,
which names the commit and the gate state, and approving publishes it. A push the
gate covers is allowed without a prompt. A session bound to a job lease is denied,
because workers do not publish. Any decision the script does not know is denied,
never allowed.

The `matcher` values are a regex (Cursor's own validator: outside `""` and `"*"`
the string must compile). One entry per event is enough: the script is the same
file on every arm by design, and it branches on payload shape. `preToolUse` matches
every tool (`*`): a call the script does not restate and that carries no path goes to
magus whole, which passes a tool it knows touches nothing and names one no rule can read
(see [Every tool reaches the guard](guard.md#every-tool-reaches-the-guard)). Scope the
`postToolUse` regex to tools that carry a command, a path, a Grep/Glob pattern, a Read
path, a WebSearch / WebFetch query, or an MCP call (`MCP:.*`, the name Cursor gives an MCP
tool after the call); a postToolUse with none of those returns `{}`. Grep,
Glob, and Read never reach `beforeShellExecution`. They are restated as the
shell shapes the rules already know (`rg …`, `find <dir> -name …`, `cat …` for
an unbounded Read, `sed -n 'a,bp'` when Read already carries a limit) and gated
on `preToolUse`, because a deny has to land before the tool runs. `postToolUse`
still carries the advise for a search the graph answers better. WebSearch and
WebFetch ride `postToolUse`: when
`kind=link` has citations matching the query, the script injects those URLs as
`additional_context` so the next open-web look prefers package/docs hosts this
tree already depends on. Cursor Agent tools spell the path field `path`; the
script also accepts `file_path`.

```buzz
// magus guard for Cursor. One file, every event.
//
// Run it as `magus buzz -s cursor-hook.buzz -- --agent-name cursor`.
// `-s` is load-bearing: without it a BZZ advisory on stderr reads to the host as a hook
// error. The command is an argv. Cursor splits it and runs magus itself, so there is no
// shell and no jq. Flags after `--` are this file's argv.
//
// It imports no magus module. A script that reads a workspace member opens the
// workspace, and that costs roughly 700ms on every tool call; this file starts
// in about 10ms and asks the binary to judge.
//
// The host name is `--agent-name` and nowhere else: never the event's shape,
// never the environment, never a default. Without it the gating events are
// denied (MGS3024). magus reads what else it knows of Cursor from its harness spell,
// among it that Cursor loads a skill by reading its SKILL.md: that Read reaches preToolUse,
// where this file hands it over as the load it is.
//
// Cursor splits across two events what every other host delivers from one. A deny
// needs a gating event (preToolUse, beforeShellExecution, beforeMCPExecution), because
// user_message and agent_message arrive only with a denial. An advise needs
// postToolUse or postToolUseFailure additional_context, which arrives after the call.
// So the gating event judges the call once and files the verdict under the
// conversation, the tool_use_id and the tool name (Cursor reuses one tool_use_id for a
// Read and the Write after it), and the after-call event answers from that file without
// asking magus again: one call, one row in the activity trail.
//
// The shell is gated on preToolUse and the command runs exactly as Cursor sent it: Cursor
// already gives a command empty stdin, and magus does not rewrite what a host sends.
// beforeShellExecution then lets the line through unjudged; it judges only a command
// preToolUse never saw.
//
// Grep, Glob, and Read never reach beforeShellExecution. They are restated as
// the shell commands the guard already judges, and gated on preToolUse, because
// a deny has to land before the tool runs.
//
// A Task call reaches preToolUse like any other tool, and is gated there as the spawn it
// is: the brief and the subagent type go to `magus shell` in the envelope it recognizes a
// spawn by, so a workspace spawn rule can deny it. subagentStart records the same spawn
// again from the child's side where Cursor fires it; cursor-agent never has.
//
// preToolUse matches every tool. A call none of the above restates, carrying no file path,
// goes to `magus shell` whole, as the envelope it reads: magus passes a tool its known-tool
// table calls inert, and names one no rule can read (unjudged-tool).
//
// beforeSubmitPrompt hands the person's message to `magus shell --message`, which records
// the topics it raises and judges nothing; the reply only says to go on.
//
// beforeMCPExecution carries tool_name, tool_input (a JSON string) and mcp_server_name.
// Magus grades MCP as mcp__<server>__<tool>, so this file rewrites the envelope before
// judging. postToolUse names the call MCP:<tool> with its input as an object and no
// server, so the verdict is filed under the tool and the input in a canonical form.
//
// sessionStart sets PATH / __MAGUS_BIN for later hooks and may inject a brief. preCompact
// marks the conversation, and the next postToolUse carries the brief again, since a
// compacted context lost the first one. The brief is size-budgeted: a nested `magus
// session --brief` has been measured (MAGUS_PPROF=mem, buzz --profile) at tens of MB of
// workspace Buzz load on this tree, so a brief over SESSION_BRIEF_MAX_BYTES is dropped.
//
// magus-guard-template: 34
// magus-guard-coverage: schema=2 host=cursor input=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=cursor input=path deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=2 host=cursor input=mcp deny=model advise=model pass=none ask=human

import "std";
import "flags";
import "io";
import "encoding/json";
import "env";
import "fs";
import "math";
import "os";
import "proc";
import "crypto";
import "lib/hook" as hook;

// SESSION_BRIEF_MAX_BYTES caps additional_context from session --brief. Measured
// post-lease-cap briefs on this tree are a few KB; anything larger is a store that
// grew past the brief's own caps and must not refill Cursor's window (or RSS).
final SESSION_BRIEF_MAX_BYTES = 16384;

final ALLOW = `{"permission":"allow"}`;

// The only reply a beforeSubmitPrompt hook owes Cursor: the message goes through.
final SUBMIT_CONTINUE = `{"continue":true}`;
final EMPTY = `{}`;

final UNAVAILABLE_TEXT = "magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set __MAGUS_BIN to its path, to restore the guard.";
final UNREADABLE = "magus guard is NOT running for this call: the hook event on stdin was not JSON, so its deny and advise rules were not applied. Check the Cursor hook configuration and version.";
final UNNAMED = "[MGS3024] this hook was not given --agent-name, so nothing was judged. Merge what 'magus describe harness' prints into the host's hook configuration; the commands it prints name the host.";

final AGENT_NAME = "--agent-name";

// How Cursor names an MCP call on preToolUse and postToolUse.
final MCP_TOOL_PREFIX = "MCP:";

// A filed verdict whose after-call event never came (an interrupted call, a crashed
// session) is removed after a day, in milliseconds.
final STATE_MAX_AGE_MS = 86400000.0;

// One judgment. The attributed call and the unattributed retry share it, so the
// payload cannot differ between them.
object Guard {
    bin: str,
    payload: str,
}

// Verdict is the part of `magus shell -o json` this file renders.
object Verdict {
    decision: str,
    reason: str,
    context: str,
}

// Scope is the attribution one event shares across every judgment it makes.
object Scope {
    bin: str,
    agent: str,
    session: str,
    transcript: str,
}

fun firstField(node: any?, keys: [str]) > str {
    foreach (key in keys) {
        final value = hook\field(node, dotPath: key);
        if (value != "") { return value; }
    }
    return "";
}

fun count(node: any?, key: str) > double {
    return std\parseDouble(hook\field(node, dotPath: key)) ?? 0.0;
}

// quote makes one shell word. The restatement is judged as a command line, and a
// pattern that contains a space or a quote has to stay one argument.
fun quote(s: str) > str {
    return "'" + s.replace("'", with: "'\\''") + "'";
}

fun argv(name: str, args: [str]) > str {
    final parts = mut [name];
    foreach (arg in args) { parts.append(quote(arg)); }
    return parts.join(" ");
}


fun warn(message: str) > void {
    io\stderr.write("cursor-hook.buzz: {message}\n") catch void;
}

// Entry is what the hook entry's argv declares: the host name, "" when it names none.
object Entry {
    agent: str,
}

fun readArgs(args: [str]) > Entry {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME]) catch null;
    if (parsed == null) { return Entry{ agent = "" }; }
    foreach (word in parsed!.unknown) {
        warn("unsupported argument {word}; this call was judged WITHOUT it");
    }
    return Entry{
        agent = parsed!.values[AGENT_NAME] ?? "",
    };
}

fun unavailableNotice(text: str) > str {
    return hook\envOr("__MAGUS_UNAVAILABLE_RESPONSE", fallback: text);
}

fun denyBoth(message: str) > str {
    return json\stringify({
        "permission": "deny",
        "user_message": message,
        "agent_message": message,
    }) catch ALLOW;
}

// stateDir holds the verdicts a gating event files for the event after it. It is under
// the person's home rather than the shared temp dir, where another user could plant a
// verdict and put words in front of the model.
fun stateDir() > str {
    final declared = env\get("CURSOR_HOOK_STATE_DIR") catch "";
    if (declared != "") { return declared; }
    final home = env\get("HOME") catch "";
    final stateHome = hook\envOr("XDG_STATE_HOME", fallback: "{home}/.local/state");
    return "{stateHome}/magus/cursor-hook";
}

fun statePath(kind: str, parts: [str]) > str {
    final digest = crypto\sha256Hex(parts.join("\n")) catch "";
    return "{stateDir()}/{kind}-{digest.sub(0, len: 32)}";
}

// remember is best effort: a record that cannot be written leaves the after-call event
// to judge the call itself, which costs a second row and loses nothing else.
fun remember(kind: str, parts: [str], value: str) > void {
    fs\mkdirAll(stateDir()) catch void;
    fs\writeFile(statePath(kind, parts: parts), content: value) catch void;
}

// recall consumes a record, so a later call that happens to share its key cannot read it.
fun recall(kind: str, parts: [str]) > str? {
    final path = statePath(kind, parts: parts);
    final held = fs\readFile(path) catch null;
    if (held == null) { return null; }
    fs\remove(path) catch void;
    return held;
}

fun prune() > void {
    final dir = stateDir();
    final now = os\time();
    foreach (name in fs\listDir(dir) catch [<str>]) {
        final info = fs\stat("{dir}/{name}") catch null;
        if (info == null) { continue; }
        final mtime = info!.mtime as? double;
        if (mtime != null and now - mtime! > STATE_MAX_AGE_MS) {
            fs\remove("{dir}/{name}") catch void;
        }
    }
}

// canonical renders a value as JSON with every object's keys sorted, so the input
// beforeMCPExecution sends as a string and the one postToolUse sends as an object key
// the same record.
fun canonical(value: any?) > str {
    if (value is {str: any}) {
        final node = value as {str: any};
        final keys = node.keys().cloneMutable();
        keys.sort(fun (left: str, right: str) > bool => left < right);
        final parts = mut [<str>];
        foreach (key in keys) {
            parts.append((json\stringify(key) catch "\"\"") + ":" + canonical(node[key]));
        }
        return "\{" + parts.join(",") + "}";
    }
    if (value is [any]) {
        final parts = mut [<str>];
        foreach (item in value as [any]) { parts.append(canonical(item)); }
        return "[" + parts.join(",") + "]";
    }
    return json\stringify(value) catch "null";
}

fun globLine(input: any?) > str {
    final pattern = firstField(input, keys: ["glob_pattern", "glob"]);
    if (pattern == "") { return ""; }
    final dir = firstField(input, keys: ["target_directory", "path"]);
    if (dir != "" and dir != ".") { return argv("find", args: [dir, "-name", pattern]); }
    return argv("find", args: [".", "-name", pattern]);
}

fun readLine(input: any?) > str {
    final file = firstField(input, keys: ["path", "file_path"]);
    if (file == "") { return ""; }
    final limit = count(input, key: "limit");
    if (limit <= 0.0 or limit > 300.0) { return argv("cat", args: [file]); }
    var start = count(input, key: "offset");
    if (start <= 0.0) { start = 1.0; }
    final range = "{math\trunc(start)},{math\trunc(start + limit - 1.0)}p";
    return argv("sed", args: ["-n", range, file]);
}

// searchPayload preserves Grep's native options for the shared guard. Glob and Read
// are the shell lines they would have been. A Glob names the directory
// it is rooted at, so a search of a directory the policy refuses is visible.
// A Read with no limit, or a limit past 300 lines, is cat; any other limit is a sed
// range, matching the Claude Code and Codex hook.
fun searchPayload(scope: Scope, event: any?) > str {
    final tool = hook\field(event, dotPath: "tool_name");
    final input = hook\dig(event, dotPath: "tool_input");
    if (tool == "Grep") { return envelope(scope, event: event, tool: tool, input: input ?? {<str: any>}); }
    if (tool == "Glob") { return globLine(input); }
    if (tool == "Read") { return readLine(input); }
    return "";
}

// skillRead reports whether path is a .../skills/<name>/SKILL.md, the file whose Read
// is how Cursor loads a skill.
fun skillRead(path: str) > bool {
    final parts = path.split("/");
    final n = parts.len();
    return n >= 3 and parts[n - 1] == "SKILL.md" and parts[n - 3] == "skills" and parts[n - 2] != "";
}

fun linkTerms(event: any?) > str {
    final tool = hook\field(event, dotPath: "tool_name");
    final input = hook\dig(event, dotPath: "tool_input");
    if (tool == "WebSearch") { return firstField(input, keys: ["search_term", "query", "search_query"]); }
    if (tool == "WebFetch") { return hook\field(input, dotPath: "url"); }
    return "";
}

fun citations(stdout: str) > [str] {
    final urls = mut [<str>];
    foreach (line in stdout.split("\n")) {
        if (line == "" or urls.len() >= 8) { continue; }
        var url = line;
        if (url.startsWith("link:")) { url = url.sub(5, len: url.len() - 5); }
        if (url != "") { urls.append(url); }
    }
    return urls;
}

// linkBias names kind=link citations this workspace already depends on, so the
// next open-web look prefers those hosts. Empty when nothing matches.
fun linkBias(bin: str, terms: str) > str {
    if (terms == "") { return ""; }
    final result = proc\exec(bin, args: ["query", "kind=link", terms, "-o", "name"], opts: {
        "quiet": true,
        "allow_failure": true,
    }) catch null;
    if (result == null or result!.code != 0) { return ""; }
    final urls = citations(result!.stdout);
    if (urls.len() == 0) { return ""; }
    final rows = mut [<str>];
    foreach (url in urls) { rows.append("  - {url}"); }
    return "This workspace already cites related docs (kind=link). Prefer these over a broad web search so results stay on packages and references this tree depends on:\n"
        + rows.join("\n") + "\n"
        + "Refine the next search with site:<host> from those URLs, or WebFetch one directly. List them again: ./magus query kind=link "
        + quote(terms) + " -o name";
}

fun judge(guard: Guard, extra: [str]) > proc\ExecResult? {
    final args = mut ["shell"];
    foreach (arg in extra) { args.append(arg); }
    return proc\exec(guard.bin, args: args, opts: {
        "quiet": true,
        "allow_failure": true,
        "stdin": guard.payload,
    }) catch null;
}

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
    return "magus guard is NOT running: {guard.bin} ({version}) could not judge this call, "
        + "so its deny and advise rules are unenforced. It said: {why}. "
        + "Rebuild or update THAT binary to restore the guard.";
}

final UNKNOWN_FLAG = "flag provided but not defined";

// rejectedFlag is magus-command.buzz's: only a flag the binary predates is asked again.
fun rejectedFlag(result: proc\ExecResult?) > bool {
    return result != null
        and result!.code != 0
        and result!.stdout.trim() == ""
        and result!.stderr.indexOf(UNKNOWN_FLAG) != null;
}

// judged returns magus's answer, or "" when the binary could not give one. Each attempt
// drops flags an older binary may reject, newest first, and runs only after the one
// before it was refused for such a flag.
fun judged(guard: Guard, attempts: [[str]], family: str, session: str) > str {
    var result: proc\ExecResult? = null;
    foreach (extra in attempts) {
        result = judge(guard, extra: extra);
        if (!rejectedFlag(result)) { break; }
    }
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        if (hook\noticeOnce(session, family: family, file: "cursor-hook.buzz")) {
            io\stderr.write(failureNotice(guard, failed: result) + "\n") catch void;
        }
        return "";
    }
    return hook\trimTrailingNewlines(result!.stdout);
}

fun parseVerdict(stdout: str) > Verdict? {
    final parsed = json\parse(stdout) catch null;
    final decision = hook\field(parsed, dotPath: "decision");
    if (decision == "") { return null; }
    return Verdict{
        decision = decision,
        reason = hook\field(parsed, dotPath: "reason"),
        context = hook\field(parsed, dotPath: "context"),
    };
}

fun shellArgs(scope: Scope, asPath: bool, ask: bool) > [str] {
    final extra = mut [<str>];
    if (asPath) { extra.append("--path"); }
    if (ask) { extra.append("--renders-ask"); }
    foreach (word in ["-o", "json", AGENT_NAME, scope.agent, "--transport", "buzz", "--session", scope.session, "--transcript", scope.transcript]) {
        extra.append(word);
    }
    return extra;
}

// verdict asks magus about one call, or returns null when it could not answer. ask is
// whether the reply can put an ask in front of the person, which only a gating event can.
fun verdict(scope: Scope, payload: str, asPath: bool, ask: bool, family: str) > Verdict? {
    final guard = Guard{ bin = scope.bin, payload = payload };
    final attributed = shellArgs(scope, asPath: asPath, ask: ask);
    final bare = mut ["-o", "json"];
    if (asPath) { bare.append("--path"); }
    final answer = judged(guard, attempts: [attributed, bare], family: family, session: scope.session);
    if (answer == "") { return null; }
    final got = parseVerdict(answer);
    if (got == null and hook\noticeOnce(scope.session, family: "unreadable-verdict", file: "cursor-hook.buzz")) {
        io\stderr.write("magus guard is NOT running for this call: {scope.bin} answered something that is not a verdict, so its deny and advise rules were not applied. Update that binary.\n") catch void;
    }
    return got;
}

// gateReply renders a verdict for a gating event. An ask is Cursor's own approval
// prompt. Pass and advise both allow, because a gating event has no context field; the
// advice waits for the after-call event. A decision this file does not know is refused,
// so a copy older than the guard contract cannot read a new verdict as consent.
fun gateReply(v: Verdict) > str {
    if (v.decision == "deny" or v.decision == "ask") {
        return json\stringify({
            "permission": v.decision,
            "user_message": v.reason,
            "agent_message": v.reason,
        }) catch ALLOW;
    }
    if (v.decision == "pass" or v.decision == "advise") { return ALLOW; }
    return json\stringify({
        "permission": "deny",
        "user_message": "magus guard returned the decision {v.decision}, which this hook does not know. Update docs/guides/integrations/agents/cursor-hook.buzz from the magus docs.",
        "agent_message": "magus guard returned the decision {v.decision}, which this hook does not know, so it refuses the call rather than allow it.",
    }) catch ALLOW;
}

// fileVerdict keeps what the after-call event owes the model. A denied call has no
// postToolUse, but it may have a postToolUseFailure, which must not judge it again.
fun fileVerdict(scope: Scope, event: any?, v: Verdict) > void {
    final id = hook\field(event, dotPath: "tool_use_id");
    if (id == "") { return; }
    var context = "";
    if (v.decision == "advise") { context = v.context; }
    remember("verdict", parts: [scope.session, id, hook\field(event, dotPath: "tool_name")], value: context);
}

fun gateCall(scope: Scope, event: any?, payload: str, asPath: bool, family: str) > str {
    final v = verdict(scope, payload: payload, asPath: asPath, ask: true, family: family);
    if (v == null) { return ALLOW; }
    fileVerdict(scope, event: event, v: v!);
    return gateReply(v!);
}

// gateShell judges a shell command on preToolUse. The line beforeShellExecution will see
// is marked as judged.
fun gateShell(scope: Scope, event: any?) > str {
    final command = hook\field(event, dotPath: "tool_input.command");
    if (command == "") { return ALLOW; }
    final v = verdict(scope, payload: command, asPath: false, ask: true, family: "failed-command");
    if (v == null) { return ALLOW; }
    fileVerdict(scope, event: event, v: v!);
    if (v!.decision == "ask") {
        // A person who approves it is not asked twice: the approved command reaches
        // beforeShellExecution as typed.
        remember("shell", parts: [scope.session, command], value: "");
    }
    if (v!.decision == "pass" or v!.decision == "advise") {
        remember("shell", parts: [scope.session, command], value: "");
    }
    return gateReply(v!);
}

fun advice(scope: Scope, payload: str, asPath: bool, family: str) > str {
    final v = verdict(scope, payload: payload, asPath: asPath, ask: false, family: family);
    if (v == null or v!.decision != "advise") { return ""; }
    return v!.context;
}

fun gates(eventName: str) > bool {
    return eventName == "beforeShellExecution"
        or eventName == "beforeMCPExecution"
        or eventName == "preToolUse"
        or eventName == "subagentStart";
}

fun mcpInput(event: any?) > any? {
    final input = hook\dig(event, dotPath: "tool_input");
    if (input is str) { return json\parse(input as str) catch input; }
    return input;
}

fun mcpKey(scope: Scope, tool: str, event: any?) > [str] {
    return [scope.session, tool, canonical(mcpInput(event))];
}

// mcpPayload rewrites Cursor's beforeMCPExecution into the mcp__<server>__<tool>
// envelope magus shell grades. A missing server or tool is the empty string: the
// caller denies rather than guessing.
fun mcpPayload(event: any?) > str {
    final server = hook\field(event, dotPath: "mcp_server_name");
    var tool = hook\field(event, dotPath: "tool_name");
    if (server == "" or tool == "") { return ""; }
    if (!tool.startsWith("mcp__")) {
        tool = "mcp__{server}__{tool}";
    }
    final envelope = {
        "tool_name": tool,
        "tool_input": mcpInput(event) ?? {<str: any>},
        "session_id": readSession(event),
        "cwd": firstField(event, keys: ["cwd"]),
        "hook_event_name": "beforeMCPExecution",
    };
    return json\stringify(envelope) catch "";
}

fun gateMcp(scope: Scope, event: any?) > str {
    final payload = mcpPayload(event);
    if (payload == "") {
        return denyBoth("magus guard: beforeMCPExecution missing mcp_server_name or tool_name; refusing rather than guessing");
    }
    final v = verdict(scope, payload: payload, asPath: false, ask: true, family: "failed-mcp");
    if (v == null) { return ALLOW; }
    var context = "";
    if (v!.decision == "advise") { context = v!.context; }
    remember("mcp", parts: mcpKey(scope, tool: hook\field(event, dotPath: "tool_name"), event: event), value: context);
    return gateReply(v!);
}

// brief is the capped `magus session --brief`, or "" when there is none or it is over
// SESSION_BRIEF_MAX_BYTES. The nested call claims host capacity; a full budget (exit 75)
// answers nothing. Profile a suspect run with MAGUS_PPROF=mem:/tmp/s.mem (and buzz
// --profile on the outer hook).
fun brief(bin: str, root: str) > str {
    final result = proc\exec(bin, args: ["session", "--brief"], opts: {
        "quiet": true,
        "allow_failure": true,
    }) catch null;
    if (result == null) { return ""; }
    var context = hook\trimTrailingNewlines(result!.stdout);
    if (context == "") { return ""; }
    if (root != "" and (fs\isFile("{root}/AGENTS.md") catch false)) {
        context = context + "\nstanding rules: AGENTS.md; re-read it, the summary above is not it";
    }
    if (context.len() <= SESSION_BRIEF_MAX_BYTES) { return context; }
    if (hook\noticeOnce("", family: "brief-budget", file: "cursor-hook.buzz")) {
        io\stderr.write("magus guard: session --brief was {context.len()} bytes, over SESSION_BRIEF_MAX_BYTES={SESSION_BRIEF_MAX_BYTES}; dropping it\n") catch void;
    }
    return "";
}

// sessionStartReply puts this checkout's magus first for later hook runs, and may hand
// the model the brief. Env always; context only when the brief fits.
fun sessionStartReply(bin: str) > str {
    final root = hook\workspaceRoot();
    if (root == "") { return EMPTY; }
    final pathEnv = env\get("PATH") catch "";
    final envMap = mut {<str: str>};
    if (pathEnv != "") {
        envMap["PATH"] = "{root}:{pathEnv}";
    } else {
        envMap["PATH"] = root;
    }
    if (bin != "" and hook\isExecutable(bin)) {
        envMap["__MAGUS_BIN"] = bin;
    } else {
        final local = "{root}/magus";
        if (hook\isExecutable(local)) { envMap["__MAGUS_BIN"] = local; }
    }
    final reply = mut {<str: any>};
    reply["env"] = envMap;
    if (bin != "" and hook\isExecutable(bin)) {
        final context = brief(bin, root: root);
        if (context != "") { reply["additional_context"] = context; }
    }
    return json\stringify(reply) catch EMPTY;
}

// afterCall is what the model is owed after a call: the verdict its gating event filed,
// or, for a call no gating event saw, a judgment made now.
fun afterCall(scope: Scope, event: any?) > str {
    final tool = hook\field(event, dotPath: "tool_name");
    if (tool.startsWith(MCP_TOOL_PREFIX)) {
        final mcpTool = tool.sub(MCP_TOOL_PREFIX.len());
        return recall("mcp", parts: mcpKey(scope, tool: mcpTool, event: event)) ?? "";
    }
    final id = hook\field(event, dotPath: "tool_use_id");
    if (id != "") {
        final held = recall("verdict", parts: [scope.session, id, tool]);
        if (held != null) { return held!; }
    }
    final command = hook\field(event, dotPath: "tool_input.command");
    if (command != "") { return advice(scope, payload: command, asPath: false, family: "failed-command"); }
    final search = searchPayload(scope, event: event);
    if (search != "") { return advice(scope, payload: search, asPath: false, family: "failed-search"); }
    final terms = linkTerms(event);
    if (terms != "") { return linkBias(scope.bin, terms: terms); }
    final writePath = toolPath(event);
    if (writePath != "") { return advice(scope, payload: writePath, asPath: true, family: "failed-path"); }
    return "";
}

// afterCallReply adds the brief to the first after-call reply following a compaction.
fun afterCallReply(scope: Scope, event: any?) > str {
    var context = afterCall(scope, event: event);
    if (recall("compact", parts: [scope.session]) != null) {
        final rebrief = brief(scope.bin, root: hook\workspaceRoot());
        if (rebrief != "" and context != "") { context = context + "\n\n" + rebrief; }
        if (rebrief != "" and context == "") { context = rebrief; }
    }
    if (context == "") { return EMPTY; }
    return json\stringify({"additional_context": context}) catch EMPTY;
}

fun readEventName(event: any?) > str {
    final named = hook\field(event, dotPath: "hook_event_name");
    if (named != "") { return named; }
    // A payload naming no event is judged by shape, so a Cursor that stopped
    // sending the field cannot take every arm to the silent default.
    if (hook\field(event, dotPath: "command") != "") { return "beforeShellExecution"; }
    if (firstField(hook\dig(event, dotPath: "tool_input"), keys: ["file_path", "path"]) != "") { return "preToolUse"; }
    return "";
}

fun toolPath(event: any?) > str {
    return firstField(hook\dig(event, dotPath: "tool_input"), keys: ["file_path", "path"]);
}

fun checkpoint(bin: str, agent: str, session: str, transcript: str) > void {
    proc\exec(bin, args: ["session", "checkpoint", AGENT_NAME, agent, "--session", session, "--transcript", transcript], opts: {
        "quiet": true,
        "allow_failure": true,
    }) catch void;
}

// recordSpawn reshapes Cursor's task into the prompt magus recognizes a spawn by.
// The parent is parent_conversation_id: conversation_id on this event is the child.
fun recordSpawn(bin: str, agent: str, event: any?) > void {
    final body = {
        "hook_event_name": hook\field(event, dotPath: "hook_event_name"),
        "session_id": firstField(event, keys: ["parent_conversation_id", "conversation_id"]),
        "transcript_path": hook\field(event, dotPath: "transcript_path"),
        "tool_input": {
            "prompt": hook\field(event, dotPath: "task"),
            "subagent_type": hook\field(event, dotPath: "subagent_type"),
        },
    };
    proc\exec(bin, args: ["shell", AGENT_NAME, agent], opts: {
        "quiet": true,
        "allow_failure": true,
        "stdin": json\stringify(body) catch "",
    }) catch void;
}

// recordMessage hands the person's message to `magus shell --message` and reads nothing
// back: a binary too old for the flag costs the record, never the message.
fun recordMessage(scope: Scope, event: any?) > void {
    final message = hook\field(event, dotPath: "prompt");
    if (message == "") { return; }
    proc\exec(scope.bin, args: [
        "shell", "--message", AGENT_NAME, scope.agent, "--transport", "buzz",
        "--session", scope.session, "--transcript", scope.transcript,
        "--event", "beforeSubmitPrompt",
    ], opts: {
        "quiet": true,
        "allow_failure": true,
        "stdin": message,
    }) catch void;
}

// envelope restates a tool call in the shape magus shell reads a host's call by.
fun envelope(scope: Scope, event: any?, tool: str, input: any) > str {
    return json\stringify({
        "tool_name": tool,
        "tool_input": input,
        "session_id": scope.session,
        "cwd": firstField(event, keys: ["cwd"]),
        "transcript_path": scope.transcript,
        "hook_event_name": "preToolUse",
    }) catch "";
}

// taskPayload restates a Task call as the envelope magus recognizes a spawn by, or "" when
// the call carries no prompt to hand it.
fun taskPayload(scope: Scope, event: any?) > str {
    final input = hook\dig(event, dotPath: "tool_input");
    if (hook\field(input, dotPath: "prompt") == "") { return ""; }
    return envelope(scope, event: event, tool: "Task", input: input);
}

fun unnamed(eventName: str) > void {
    warn(UNNAMED);
    if (eventName == "beforeSubmitPrompt") {
        io\stdout.write(SUBMIT_CONTINUE) catch void;
        return;
    }
    if (eventName == "beforeShellExecution" or eventName == "preToolUse") {
        io\stdout.write(denyBoth(UNNAMED)) catch void;
        return;
    }
    if (eventName == "subagentStart") { io\stdout.write(ALLOW) catch void; }
}

fun readSession(event: any?) > str {
    return firstField(event, keys: ["session_id", "conversation_id"]);
}

fun gateTool(scope: Scope, event: any?) > str {
    final tool = hook\field(event, dotPath: "tool_name");
    if (tool == "Task") {
        final spawn = taskPayload(scope, event: event);
        if (spawn == "") { return ALLOW; }
        return gateCall(scope, event: event, payload: spawn, asPath: false, family: "failed-spawn");
    }
    if (tool == "Shell") { return gateShell(scope, event: event); }
    final path = toolPath(event);
    if (tool == "Read" and skillRead(path)) {
        final load = envelope(scope, event: event, tool: "Read", input: {"file_path": path});
        return gateCall(scope, event: event, payload: load, asPath: false, family: "failed-skill");
    }
    final search = searchPayload(scope, event: event);
    if (search != "") { return gateCall(scope, event: event, payload: search, asPath: false, family: "failed-search"); }
    if (path != "") { return gateCall(scope, event: event, payload: path, asPath: true, family: "failed-path"); }
    // beforeMCPExecution judges an MCP call, with the server name this event lacks.
    if (tool.startsWith(MCP_TOOL_PREFIX)) { return ALLOW; }
    // Every other call goes to magus whole: it passes a tool its table knows touches
    // nothing, and names one no rule can read rather than letting it run unseen.
    final input = hook\dig(event, dotPath: "tool_input") ?? {<str: any>};
    final call = envelope(scope, event: event, tool: tool, input: input);
    return gateCall(scope, event: event, payload: call, asPath: false, family: "failed-call");
}

fun dispatch(event: any?, eventName: str, scope: Scope) > str {
    if (eventName == "sessionStart") {
        prune();
        return sessionStartReply(scope.bin);
    }
    if (eventName == "sessionEnd") {
        checkpoint(scope.bin, agent: scope.agent, session: scope.session, transcript: scope.transcript);
        prune();
        return "";
    }
    if (eventName == "subagentStart") {
        recordSpawn(scope.bin, agent: scope.agent, event: event);
        return ALLOW;
    }
    if (eventName == "beforeSubmitPrompt") {
        recordMessage(scope, event: event);
        return SUBMIT_CONTINUE;
    }
    if (eventName == "preCompact") {
        remember("compact", parts: [scope.session], value: "");
        return EMPTY;
    }
    if (eventName == "beforeMCPExecution") { return gateMcp(scope, event: event); }
    if (eventName == "beforeShellExecution") {
        final command = hook\field(event, dotPath: "command");
        if (recall("shell", parts: [scope.session, command]) != null) { return ALLOW; }
        return gateCall(scope, event: event, payload: command, asPath: false, family: "failed-command");
    }
    if (eventName == "preToolUse") { return gateTool(scope, event: event); }
    if (eventName == "postToolUse" or eventName == "postToolUseFailure") { return afterCallReply(scope, event: event); }
    return "";
}

fun main(args: [str]) > void {
    final event = json\parse(io\stdin.readAll() catch "") catch null;
    if (event == null) {
        // No event name to gate on, so this answers with an explicit allow, said
        // out loud.
        if (hook\noticeOnce("", family: "unreadable", file: "cursor-hook.buzz")) {
            io\stderr.write(UNREADABLE + "\n") catch void;
        }
        io\stdout.write(ALLOW) catch void;
        return;
    }
    final eventName = readEventName(event);
    final entry = readArgs(args);
    if (entry.agent == "") {
        unnamed(eventName);
        return;
    }
    final bin = hook\resolveBin(cwd: hook\field(event, dotPath: "cwd"), session: readSession(event), file: "cursor-hook.buzz");
    if (bin == "" or !hook\isExecutable(bin)) {
        if (hook\noticeOnce(readSession(event), family: "unavailable", file: "cursor-hook.buzz")) {
            io\stderr.write(unavailableNotice(text: UNAVAILABLE_TEXT) + "\n") catch void;
        }
        if (gates(eventName)) { io\stdout.write(ALLOW) catch void; }
        if (eventName == "beforeSubmitPrompt") { io\stdout.write(SUBMIT_CONTINUE) catch void; }
        return;
    }
    final scope = Scope{
        bin = bin,
        agent = entry.agent,
        session = readSession(event),
        transcript = hook\field(event, dotPath: "transcript_path"),
    };
    io\stdout.write(dispatch(event, eventName: eventName, scope: scope)) catch void;
}
```

### Checking the Buzz it writes

A second `postToolUse` entry, on `Write|StrReplace|Edit`, runs
[`magus-buzz-check.buzz`](guard-templates.md#magus-buzz-checkbuzz), which runs
`magus buzz --check` on a `.buzz` file the agent just wrote and hands a failure back
as the reason to fix it:

```json
{
  "hooks": {
    "postToolUse": [
      {
        "matcher": "Write|StrReplace|Edit",
        "command": "magus buzz -s docs/guides/integrations/agents/magus-buzz-check.buzz -- --agent-name cursor",
        "timeout": 30
      }
    ]
  },
  "version": 1
}
```

## Notifications

Cursor can run a command from its agent hooks. Shape the event into the
canonical envelope and pipe it to `magus session notify`; see [Attention hooks](notifications.md).

## Recording where the work stands

The `sessionEnd` entry in the wiring above records the revision, branch and
dirtiness of the tree when a session ends; `magus session` lists it. The script
handles that arm itself, calling `magus session checkpoint` rather than the
shared [`magus-checkpoint.buzz`](guard-templates.md#magus-checkpointbuzz), so this
host stays a one-file install; the shared template does the identical job if you
would rather point `sessionEnd` at it with `--agent-name cursor` on the command.

`sessionEnd` carries `session_id` and every hook carries `transcript_path`, so
both pointers are recorded. They are pointers magus stores and never opens, and a
checkpoint without them still says where the work sits, which is the part a person
coming back needs.

`magus session checkpoint --note "..."` writes the same record by hand.

## Judging the reply a turn ends with

Nothing here is wired by default: the reply check judges your agent's replies with
the `proofread` binary, which is a choice, not part of the guard. To turn it on, download
[`proofread-reply.buzz`](guard-templates.md#proofread-replybuzz) and add this entry to
`.cursor/hooks.json`:

```json
{
  "version": 1,
  "hooks": {
    "stop": [{ "command": "magus buzz -s .cursor/hooks/proofread-reply.buzz -- --agent-name cursor", "timeout": 10, "loop_limit": 1 }]
  }
}
```

`stop` then runs the script with `--agent-name cursor`, which runs the `proofread`
binary over the turn's last reply. Cursor's `stop` event carries `status`, `loop_count` and `transcript_path`
and no reply text, so the script reads the reply out of the transcript: the last
assistant message that has text, stopping at the last user message. When there are
findings it prints `{"followup_message": "..."}`, which Cursor submits as another
turn, so the agent is asked to send the reply again, fixed. A `stop` hook cannot
block; this is the only channel it has.

The script stays silent when `loop_count` is above 0, so it never judges a reply it
asked for, when `status` is not `completed`, when `transcript_path` is null, and
when `proofread` is missing. The entry also sets `"loop_limit": 1`, under Cursor's
default of 5. `__PROOFREAD_BIN` names the binary and `__PROOFREAD_DECISIONS` a
decisions file. The transcript's JSON lines (a `role`, and `message.content` text
parts) were read from Cursor's own transcript files on one machine and are not
documented, so this arm is **unverified live**: check that a turn with findings
produces a follow-up before relying on it.

## Lease capture

`preToolUse` on `Task` is the spawn row. Its `tool_input` carries `description`,
`prompt`, `subagent_type`, `run_in_background` and, when the agent picked one,
`model` (probed on cursor-agent 2026.10.01). The script restates the call as the
envelope magus recognizes a spawn by, a `tool_input` carrying a `prompt`, and gates
it, so a workspace [`magus\guard.spawn`](../../../reference/guard-spawn.md) rule can
deny it and a title of the form `<parent>/<role> <job>` names the job. One with no
`prompt` has nothing to judge and is allowed. To join a subagent's own calls to a
job, write the marker line documented in [Any other host](any-host.md#lease-capture)
at the top of the prompt you hand the subagent.

`subagentStart` is wired too, and records the same spawn from the child's side,
reshaped from its `task`, `subagent_type` and `parent_conversation_id`; it never
judges, and always allows. **cursor-agent never fires it**: in a probe on 2026.10.01
with `subagentStart` and `subagentStop` both hooked, a `Task` ran a child to
completion and neither event fired, nor did `postToolUse` for the `Task` call. The
child's own calls arrive on `preToolUse` and `postToolUse` under a new
`conversation_id` that names no parent, so nothing a hook sees links a child to
its spawn or reports the child's context size. The wiring stays for a Cursor build
that fires it.

## Coverage and limits

Every claim below that cites a probe was run against cursor-agent 2026.10.01
(`~/.local/bin/cursor-agent -p`) with logging hooks in a scratch workspace; the rest
cite [Cursor hooks](https://cursor.com/docs/agent/hooks).

**Ten of Cursor's twenty-one hook events are wired.** `sessionStart`,
`beforeShellExecution`, `beforeMCPExecution`, `preToolUse`, `postToolUse`,
`postToolUseFailure`, `preCompact`, `subagentStart`, `sessionEnd` and
`beforeSubmitPrompt`. `stop` carries the opt-in reply check and nothing by default.
The rest are not wired, for these reasons:

- `subagentStop` carries `summary`, `modified_files` and `agent_transcript_path`
  and names no subagent id (Cursor hooks), and it never fired in the probe below.
- `afterShellExecution`, `afterMCPExecution`, `afterFileEdit`, `afterTabFileEdit`,
  `afterAgentResponse` and `afterAgentThought` are observation only (Cursor hooks),
  and `postToolUse` already carries the advise.
- `beforeReadFile` and `beforeTabFileRead` would gate a read the `preToolUse`
  `Read` matcher already gates; in the probe `beforeReadFile` fired after
  `preToolUse` for every `Read`.
- `workspaceOpen`: nothing in magus consumes it.

**Hook all eighteen agent events at once and a run can die.** With every agent
event hooked, cursor-agent runs sometimes ended `Connection lost ...
WritableIterable is closed` (probed); the ten above have not shown it.

**`-p` fires no turn events.** Under `cursor-agent -p`, `sessionStart` and
`sessionEnd` fired and `beforeSubmitPrompt`, `stop` and `afterAgentResponse` did
not (probed). So a headless run records none of your messages and gets no reply
check if you wired one; an interactive session does both.

**`subagentStart` never fires in the CLI.** With `subagentStart` and `subagentStop`
hooked, a `Task` ran a child to completion and neither fired; neither did
`postToolUse` for the `Task` call (probed). The child's calls reach `preToolUse` under
its own `conversation_id`, which names no parent. So the spawn row is the parent's
`preToolUse` `Task`, and magus learns no child id, which leaves the child's calls
unattributed to its spawn and its context size unfiled.

**No per-agent context size.** A Cursor transcript line carries no token usage
(probed: `{role, message.content}` and `{type: "turn_ended"}` only), and no hook
names a subagent's usage. `preCompact` carries `context_tokens` for the conversation
compacting (Cursor hooks), which the script does not file.

**Compaction re-briefs on the next tool call, not at once.** `preCompact` answers
`user_message` only (Cursor hooks), which the person sees and the model does not. So
it marks the conversation, and the next `postToolUse` or `postToolUseFailure`
carries the capped `magus session --brief` beside its own advice. A compaction
followed by no tool call gets no brief. `preCompact` was not exercised live: `-p`
cannot be made to compact on demand.

**Session start is wired.** `sessionStart` sets `env.PATH` / `__MAGUS_BIN` and
hands the model `magus session --brief` via `additional_context` when the brief is
at most `SESSION_BRIEF_MAX_BYTES` (16KiB); a code word sent that way reached the
model under `-p` (probed). Profile a heavy run with `MAGUS_PPROF=mem:/tmp/s.mem` and
`magus buzz --profile` on the outer hook: the Buzz compile of the hook itself is
milliseconds; the nested brief pays the workspace load. Every hook entry carries
`"timeout": 10`, so a brief slower than that is lost with the reply.

**MCP calls are gated on `beforeMCPExecution`, and advised on `postToolUse`.**
`beforeMCPExecution` carries `tool_name`, `mcp_server_name` and `tool_input` as a
JSON string; the script parses it and rewrites the call to `mcp__<server>__<tool>`
before `magus shell` grades it. `postToolUse` names the same call `MCP:<tool>`, with
`tool_input` as an object and no server (probed), so the verdict is filed under the
tool and the input with its keys sorted, and the advice an MCP call earns reaches
the model there. `failClosed` is set so a hook crash blocks the tool.

**A failed call keeps its advice.** `postToolUseFailure` carries `error_message`,
`failure_type`, `is_interrupt` and `tool_use_id`, and its `additional_context`
reached the model (probed), so the advice its gating event filed is delivered there.

**Reading the reply out of the transcript** (above) is the one arm that rests on an
undocumented file format, and `stop` does not fire under `-p`, so it is unverified
live.

Cursor fails open on a hook crash or malformed JSON unless the hook sets
`failClosed`. The script above matches that stance instead of pretending to be
stricter than the surrounding contract. For strict behavior, set `failClosed` on
the hook and change its missing-binary branch to a deny.

A [session-load adapter](session-load.md#cursor) reads Cursor's transcripts into
`magus session load`, as the other hosts' adapters do.

## Verify

```sh
magus doctor
```

**guard binary** names the binary a hook would resolve; **guard wiring** probes it
with a known-denied command and checks that a host config invokes a template whose
version marker is current.
