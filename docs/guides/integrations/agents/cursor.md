---
title: Cursor
description: Wiring magus into Cursor - AGENTS.md for guidance, one self-contained hook script for all five of its events, and the one job Cursor's contract cannot express.
tags: [agents, cursor, AGENTS.md, guard, hooks]
---

# Cursor

Cursor does not read Agent Skills directories. It reads an `AGENTS.md` at the
repository root, and it runs hooks as programs with the event on stdin. One
self-contained script covers every event magus uses, so installing the whole
integration is a single download.

| what             | where                                                                                         |
| ---------------- | --------------------------------------------------------------------------------------------- |
| always-on rules  | `AGENTS.md` (you paste the block; magus never writes it)                                      |
| guard wiring     | `.cursor/hooks.json`                                                                          |
| command surface  | deny and advise both reach the model                                                          |
| file surface     | deny and advise both reach the model                                                          |
| MCP call surface | not wired: `beforeMCPExecution`/`afterMCPExecution` exist, their payload does not (see below) |
| checkpoint       | `sessionEnd`                                                                                  |
| lease            | `subagentStart` (unverified live, see below)                                                  |
| MCP              | [MCP](../mcp.md)                                                                              |

## Skills

There is no skills directory to install into. Run `magus agent install` anyway:
it prints the managed magus block when your `AGENTS.md` is missing it or
carrying a stale one, and you paste it in. [Skills](skills.md) covers the block,
its stamp, and the drift check that grades it.

Because Cursor has no Agent Skills surface, it cannot enforce a short-versus-full
skill-form choice. Keep that repository guidance explicit and user-owned in
`AGENTS.md`; do not claim a model or provider setting selects it automatically.

An `advise` verdict reaches the model here too, on `postToolUse` rather than at
the moment the call is gated; that guidance being in `AGENTS.md` as well is the
same belt-and-braces every host gets, not a substitute for it.

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

## Guard hook

Prefer wiring the Cursor harness from the root magusfile when you bounce between
hosts; `magus describe harness` then covers every wired provider:

```buzz
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
    "beforeShellExecution": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor" }],
    "preToolUse": [{ "matcher": "Write|StrReplace|Delete|Edit|NotebookEdit|Grep|Glob|Read", "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor" }],
    "postToolUse": [
      { "matcher": "Shell|Write|StrReplace|Delete|Edit|NotebookEdit|Grep|Glob|Read|WebSearch|WebFetch", "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor" }
    ],
    "subagentStart": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor" }],
    "sessionEnd": [{ "command": "magus buzz -s .cursor/hooks/cursor-hook.buzz -- --agent-name cursor" }]
  }
}
```

It is deliberately self-contained rather than delegating to the shared
templates: needing five files to install a guard is how a guard ends up not
installed. The script branches on the `hook_event_name` every Cursor hook
carries, so one file serves all of them.

Cursor splits across two events what every other host delivers from one, and
that is the thing to read before changing this wiring. A gating event
(`beforeShellExecution`, `preToolUse`) carries a `deny`, because Cursor sends
`user_message` and `agent_message` only with a denial. An `advise` therefore has
no channel there and needs `postToolUse.additional_context`, which arrives after
the call. So a judged call runs magus twice on this host and leaves two rows in
the activity trail, where every other host leaves one.

A push at a commit no passing gate covers gets the verdict `ask`, and
`beforeShellExecution` answers `permission: "ask"`: Cursor shows you the reason,
which names the commit and the gate state, and approving publishes it. A push the
gate covers is allowed without a prompt. A session bound to a job lease is denied,
because workers do not publish. Any decision the script does not know is denied,
never allowed.

The `matcher` values are a regex (Cursor's own validator: outside `""` and `"*"`
the string must compile). One entry per event is enough: the script is the same
file on every arm by design, and it branches on payload shape. Scope the regex
to tools that carry a command, a path, a Grep/Glob pattern, a Read path, or a
WebSearch / WebFetch query; a postToolUse with none of those returns `{}`. Grep,
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
// Run it as `magus buzz -s cursor-hook.buzz -- --agent-name cursor`. `-s` is
// load-bearing: without it a BZZ advisory on stderr reads to the host as a hook
// error. The command is an argv. Cursor splits it and runs magus itself, so there
// is no shell and no jq. Flags after `--` are this file's argv.
//
// It imports no magus module. A script that reads a workspace member opens the
// workspace, and that costs roughly 700ms on every tool call; this file starts
// in about 10ms and asks the binary to judge.
//
// The host name is `--agent-name` and nowhere else: never the event's shape,
// never the environment, never a default. Without it the gating events are
// denied (MGS3024).
//
// Cursor splits across two events what every other host delivers from one.
// A deny needs a gating event, because user_message and agent_message arrive
// only with a denial. An advise needs postToolUse.additional_context, which
// arrives after the call. So a judged call runs magus twice here.
//
// Grep, Glob, and Read never reach beforeShellExecution. They are restated as
// the shell commands the guard already judges, and gated on preToolUse, because
// a deny has to land before the tool runs.
//
// magus-guard-template: 19
// magus-guard-coverage: schema=1 host=cursor surface=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=1 host=cursor surface=path deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=1 host=cursor surface=mcp deny=none advise=none pass=none ask=none
// The mcp row is none because beforeMCPExecution and afterMCPExecution name no
// field for the tool. This file does not guess one.

import "std";
import "flags";
import "io";
import "encoding/json";
import "math";
import "proc";
import "lib/hook" as hook;

// Cursor's gating reply. An ask is Cursor's own approval prompt. Pass and advise
// both allow, because a gating event has no context field: an advise sent here
// would collapse into a bare allow and drop the text. A decision this file does
// not know is refused, so a copy older than the guard contract cannot read a new
// verdict as consent.
final PERMISSION_DENY = `{"permission":"deny","user_message":{{toJson .reason}},"agent_message":{{toJson .reason}}}`;
final PERMISSION_ASK = `{"permission":"ask","user_message":{{toJson .reason}},"agent_message":{{toJson .reason}}}`;
final PERMISSION_ALLOW = `{"permission":"allow"}`;
final GATE_UNKNOWN = `{{else}}{"permission":"deny","user_message":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know. Update docs/guides/integrations/agents/cursor-hook.buzz from the magus docs.")}},"agent_message":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it.")}}}{{end}}`;
final GATE = `{{if eq .decision "deny"}}` + PERMISSION_DENY
    + `{{else if eq .decision "ask"}}` + PERMISSION_ASK
    + `{{else if eq .decision "pass"}}` + PERMISSION_ALLOW
    + `{{else if eq .decision "advise"}}` + PERMISSION_ALLOW
    + GATE_UNKNOWN;

// postToolUse is the advise channel. Anything else renders an empty object,
// which Cursor accepts as no opinion.
final ADVISE = `{{if eq .decision "advise"}}{"additional_context":{{toJson .context}}}{{else}}{}{{end}}`;

final ALLOW = `{"permission":"allow"}`;
final EMPTY = `{}`;

final UNAVAILABLE_TEXT = "magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set __MAGUS_BIN to its path, to restore the guard.";
final UNREADABLE = "magus guard is NOT running for this call: the hook event on stdin was not JSON, so its deny and advise rules were not applied. Check the Cursor hook configuration and version.";
final UNNAMED = "[MGS3024] this hook was not given --agent-name, so nothing was judged. Merge what 'magus describe harness' prints into the host's hook configuration; the commands it prints name the host.";

final AGENT_NAME = "--agent-name";

// One judgment. The attributed call and the unattributed retry share it, so the
// payload cannot differ between them.
object Guard {
    bin: str,
    payload: str,
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

fun agentNameOf(args: [str]) > str {
    final parsed = flags\parse(args, switches: [<str>], valued: [AGENT_NAME]) catch null;
    if (parsed == null) { return ""; }
    foreach (word in parsed!.unknown) {
        warn("unsupported argument {word}; this call was judged WITHOUT it");
    }
    return parsed!.values[AGENT_NAME] ?? "";
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

fun grepLine(input: any?) > str {
    final pattern = hook\field(input, dotPath: "pattern");
    if (pattern == "") { return ""; }
    final file = firstField(input, keys: ["path", "file_path"]);
    if (file != "" and file != ".") { return argv("rg", args: [pattern, file]); }
    return argv("rg", args: [pattern]);
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

// searchCommand is the shell line a Grep, Glob, or Read would have been, or ""
// for every other tool. A scoped Grep names its file. A Glob names the directory
// it is rooted at, so a search of a directory the policy refuses is visible.
// A Read with no limit, or a limit past 300 lines, is cat; any other limit is a sed
// range, matching the Claude Code and Codex hook.
fun searchCommand(event: any?) > str {
    final tool = hook\field(event, dotPath: "tool_name");
    final input = hook\dig(event, dotPath: "tool_input");
    if (tool == "Grep") { return grepLine(input); }
    if (tool == "Glob") { return globLine(input); }
    if (tool == "Read") { return readLine(input); }
    return "";
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
    final text = "This workspace already cites related docs (kind=link). Prefer these over a broad web search so results stay on packages and references this tree depends on:\n"
        + rows.join("\n") + "\n"
        + "Refine the next search with site:<host> from those URLs, or WebFetch one directly. List them again: ./magus query kind=link "
        + quote(terms) + " -o name";
    return json\stringify({"additional_context": text}) catch "";
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

// judged returns the rendered reply, or "" when the binary could not answer.
// A non-zero status with a reply is a deny, which Cursor reads from stdout;
// retrying that would judge the call twice. Empty stdout with a non-zero status
// is a binary that rejected a flag, and the retry drops the attribution so an
// older binary can still answer.
fun judged(guard: Guard, extra: [str], family: str, session: str) > str {
    var result = judge(guard, extra: extra);
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        result = judge(guard, extra: [<str>]) catch null;
    }
    if (result == null or (result!.code != 0 and hook\trimTrailingNewlines(result!.stdout) == "")) {
        if (hook\noticeOnce(session, family: family, file: "cursor-hook.buzz")) {
            io\stderr.write(failureNotice(guard, failed: result) + "\n") catch void;
        }
        return "";
    }
    return hook\trimTrailingNewlines(result!.stdout);
}

fun shellArgs(agent: str, session: str, transcript: str, template: str, ask: bool, asPath: bool) > [str] {
    final extra = mut [<str>];
    if (asPath) { extra.append("--path"); }
    if (ask) { extra.append("--renders-ask"); }
    extra.append("-o");
    extra.append("template={template}");
    foreach (word in [AGENT_NAME, agent, "--transport", "buzz", "--session", session, "--transcript", transcript]) {
        extra.append(word);
    }
    return extra;
}

// Scope is the attribution one event shares across every judgment it makes.
object Scope {
    bin: str,
    agent: str,
    session: str,
    transcript: str,
}

fun verdict(scope: Scope, payload: str, template: str, ask: bool, asPath: bool, family: str) > str {
    final guard = Guard{ bin = scope.bin, payload = payload };
    return judged(guard, extra: shellArgs(scope.agent, session: scope.session, transcript: scope.transcript, template: template, ask: ask, asPath: asPath), family: family, session: scope.session);
}

fun gates(eventName: str) > bool {
    return eventName == "beforeShellExecution" or eventName == "preToolUse" or eventName == "subagentStart";
}

fun eventNameOf(event: any?) > str {
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

fun unnamed(eventName: str) > void {
    warn(UNNAMED);
    if (eventName == "beforeShellExecution" or eventName == "preToolUse") {
        io\stdout.write(denyBoth(UNNAMED)) catch void;
        return;
    }
    if (eventName == "subagentStart") { io\stdout.write(ALLOW) catch void; }
}

fun sessionOf(event: any?) > str {
    return firstField(event, keys: ["session_id", "conversation_id"]);
}

fun gate(scope: Scope, payload: str, asPath: bool, family: str) > str {
    final got = verdict(scope, payload: payload, template: GATE, ask: true, asPath: asPath, family: family);
    if (got == "") { return ALLOW; }
    return got;
}

fun advise(scope: Scope, payload: str, asPath: bool, family: str) > str {
    final got = verdict(scope, payload: payload, template: ADVISE, ask: false, asPath: asPath, family: family);
    if (got == "") { return EMPTY; }
    return got;
}

fun dispatch(event: any?, eventName: str, bin: str, agent: str) > str {
    final scope = Scope{
        bin = bin,
        agent = agent,
        session = sessionOf(event),
        transcript = hook\field(event, dotPath: "transcript_path"),
    };
    if (eventName == "sessionEnd") {
        checkpoint(scope.bin, agent: scope.agent, session: scope.session, transcript: scope.transcript);
        return "";
    }
    if (eventName == "subagentStart") {
        recordSpawn(scope.bin, agent: scope.agent, event: event);
        return ALLOW;
    }
    if (eventName == "beforeShellExecution") {
        return gate(scope, payload: hook\field(event, dotPath: "command"), asPath: false, family: "failed-command");
    }
    if (eventName == "preToolUse") {
        final search = searchCommand(event);
        if (search != "") { return gate(scope, payload: search, asPath: false, family: "failed-search"); }
        final writePath = toolPath(event);
        if (writePath == "") { return ALLOW; }
        return gate(scope, payload: writePath, asPath: true, family: "failed-path");
    }
    if (eventName == "postToolUse") {
        final command = hook\field(event, dotPath: "tool_input.command");
        if (command != "") { return advise(scope, payload: command, asPath: false, family: "failed-command"); }
        final search = searchCommand(event);
        if (search != "") { return advise(scope, payload: search, asPath: false, family: "failed-search"); }
        final terms = linkTerms(event);
        if (terms != "") {
            final bias = linkBias(scope.bin, terms: terms);
            if (bias != "") { return bias; }
            return EMPTY;
        }
        final writePath = toolPath(event);
        if (writePath != "") { return advise(scope, payload: writePath, asPath: true, family: "failed-path"); }
        return EMPTY;
    }
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
    final eventName = eventNameOf(event);
    final agent = agentNameOf(args);
    if (agent == "") {
        unnamed(eventName);
        return;
    }
    final bin = hook\resolveBin();
    if (bin == "" or !hook\isExecutable(bin)) {
        if (hook\noticeOnce(sessionOf(event), family: "unavailable", file: "cursor-hook.buzz")) {
            io\stderr.write(unavailableNotice(text: UNAVAILABLE_TEXT) + "\n") catch void;
        }
        if (gates(eventName)) { io\stdout.write(ALLOW) catch void; }
        return;
    }
    io\stdout.write(dispatch(event, eventName: eventName, bin: bin, agent: agent)) catch void;
}
```

## Notifications

Cursor can run a command on its agent hook surface. Shape the event into the
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

## Lease capture

`subagentStart` fires when Cursor hands work to a sub-agent, and carries the
handed `task`, the `subagent_type`, and `parent_conversation_id` for the parent
side. That is everything magus records as a spawn, under different names, so the
script reshapes the payload into the canonical envelope before piping it: magus
recognizes a spawn by a `tool_input` carrying a `prompt`, never by a tool name it
would have to enumerate per host.

It records; it does not judge. A lease prompt is prose, so the verdict is always
a pass and the arm always allows. To join those events to a job, write the
marker line documented in [Any other host](any-host.md#lease-capture) at the top
of the prompt you hand the sub-agent.

This one is **unverified live**. An open Cursor forum report says
`subagentStart` and `subagentStop` never fire while `beforeShellExecution` from
the same `hooks.json` works normally. The wiring ships anyway, because a hook that
never fires costs nothing and a missing one cannot be found; check
`magus session` for `agent_spawn` events before relying on it.

## Coverage and limits

**The MCP call surface is declared but not wired.** Cursor's hooks schema DOES
name `beforeMCPExecution` and `afterMCPExecution` - the MCP-call twins of
`beforeShellExecution` and `preToolUse`/`postToolUse` above - so this is not
the "transport does not carry it" gap it is on Codex and OpenCode. What is
missing is the payload: no schema, published or transcribed, says what field
on those two events carries the tool name and params, and this script does not
wire an event whose shape it cannot verify - the same caution `subagentStart`
below already gets ("unverified live"). Confirm the payload against a real
Cursor session before flipping this.

**Both surfaces now reach the model on both decisions.** That is new, and it cost
two events per judged call: the write gate moved from `afterFileEdit`, which
fires once the write has landed, to `preToolUse`, which blocks it; and the
advisory moved from stderr prose to `postToolUse.additional_context`. Reporting a
declared-output edit after the call is not a concession, since that rule only ever
explains, on every host. Reporting it to the PERSON was, and that is what changed.

**Judging twice is the price.** A gating event carries no message on an allow, so
the explanation has to come from a second event, and magus is asked about the same
call twice. The activity trail therefore carries two rows per judged call here.

**Post-compaction rehydration is not expressible.** Every other host has an event
that hands a compacted session its state back. Cursor's `preCompact` returns
`user_message` only, which reaches the person and not the model, and
`beforeSubmitPrompt` explicitly cannot inject context. So there is nothing to wire
and nothing is faked: run `magus session --brief` and paste it, or read it
yourself. `sessionStart.additional_context` does reach the model, but it fires
when nothing has been lost yet.

**A tool failure carries no hint.** `postToolUseFailure` has no response fields at
all, so the one place a host could explain a failing command is closed here.

Cursor fails open on a hook crash or malformed JSON unless the hook sets
`failClosed`. The script above matches that stance instead of pretending to be
stricter than the surrounding contract. For strict behavior, set `failClosed` on
the hook and change its missing-binary branch to a deny.

The magus half is verified: the verdict shape this script parses is checked
against this repository's binary. The Cursor half is written against the
product's published hook documentation and has not been executed here, so
confirm it against [Cursor hooks](https://cursor.com/docs/agent/hooks).

There is also no session-load adapter for this host, where the other three ship
one. Nothing in Cursor prevents it; nobody has written it.

## Verify

```sh
magus doctor
```

**guard binary** names the binary a hook would resolve; **guard wiring** probes it
with a known-denied command and checks that a host config invokes a template whose
version marker is current.
