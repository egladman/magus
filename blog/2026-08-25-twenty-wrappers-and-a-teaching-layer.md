---
title: "Twenty wrappers and a teaching layer"
description: "magus exposes 22 MCP tools. Twenty wrap a command the CLI already has. The two that do not are drift, and the guidance bolted to all of them is the part a person at a terminal never sees."
tags: [opinion]
date: 2026-08-25
draft: true
---

# Twenty wrappers and a teaching layer

magus ships an MCP server. I built it, I use it, it works, and I have been telling
people I might remove it.

That was instinct. I had never checked it against the surface itself, which I could
have done at any point and had not. So I made a table.

First, scope, because MCP covers a lot of ground and I am not arguing with most of
it. I am talking about one shape: an MCP server that exists so an AI coding
assistant can drive a tool already sitting on your machine. Wrapping a remote API,
or a service with no command line at all, is a different question with a different
answer.

## What is actually in there

magus exposes 22 MCP tools. Here is every one of them against the CLI command it
corresponds to.

| MCP tool               | CLI equivalent            |
| ---------------------- | ------------------------- |
| `magus_describe`       | `magus describe`          |
| `magus_describe_file`  | `magus describe file`     |
| `magus_where`          | `magus where`             |
| `magus_query`          | `magus query`             |
| `magus_explain`        | `magus explain`           |
| `magus_path`           | `magus path`              |
| `magus_refs`           | `magus refs`              |
| `magus_stats`          | `magus graph stats`       |
| `magus_output`         | `magus query output`      |
| `magus_tail_log`       | `magus query output`      |
| `magus_run_target`     | `magus run`               |
| `magus_run_affected`   | `magus affected`          |
| `magus_affected_explain` | `magus affected --explain` |
| `magus_affected_plan`  | `magus affected --plan`   |
| `magus_doctor`         | `magus doctor`            |
| `magus_status`         | `magus status`            |
| `magus_config_get`     | `magus config`            |
| `magus_memory`         | `magus memory`            |
| `magus_diff`           | `magus diff`              |
| `magus_vcs_checkpoint` | `magus vcs checkpoint`    |
| `magus_insight`        | none. `magus\insight()` in Buzz |
| `magus_ledger`         | none, anywhere. See below |

Twenty of the twenty-two wrap something the CLI already does. The same capability,
reachable two ways, documented twice, maintained twice.

Two of them duplicate each other inside the MCP surface. `magus_output` and
`magus_tail_log` both fetch a captured run log, and both correspond to
`magus query output`. I did not notice until I put them in a table.

## The two that are not wrappers

I expected the leftovers to be the interesting part, the places where a protocol
layer had earned something. They show where my surfaces have drifted apart instead.

**`magus_insight` has no CLI counterpart, but the capability is not gone.**
`magus insight` was removed as a subcommand. The analysis itself is alive and well:
`magus\insight()` in the Buzz module returns the whole thing as a typed report, and
the MCP tool is backed by its own handler rather than by shelling out to a command.
So nothing here is dangling. What happened is narrower and, for me, more pointed: of
the three ways into magus, the command line is the one that lost this.

**`magus_ledger` has no counterpart anywhere else.** There is no `magus ledger`
subcommand and no ledger client in the Buzz module either. The lease ledger is
reachable over MCP and nowhere else. That is the one place in this surface where the
server is not redundant, and the reason is not that a protocol layer earned it. I let
a capability land in the newest door and never gave it one anywhere else.

So the audit did not turn up a protocol layer doing work nothing else could. It
turned up twenty duplicates and two capabilities that a person at a terminal cannot
reach.

## Structured output was never an AI feature

The redundancy is the expected result, which is the part I want to sit on for a
moment.

The magus CLI already prints structured output: `-o json`, `-o name`,
`-o template=<go-template>`, and an output reference for anything that ran. Point an
agent at `magus ls -o json` and it gets what the MCP tool would have handed it. If
your CLI is consistent and predictable, a protocol layer on top has close to nothing
left to do. I think we got lazy over the last ten or fifteen years and started
treating a confusing CLI as the natural state of things, and an MCP server is often
filling a gap that should never have opened.

Datadog shipped a CLI called Pup and marketed it as an agentic CLI.[^pup] Look at
what it does: commands structured so you can navigate them without going to the
documentation, responses available in JSON and YAML, errors carrying detail and
hints. That is a well-built command line. It is what a command line has owed you for
forty years. Calling it agentic is branding stuck on table stakes, and it bothers me
because it teaches people that structured output is an AI feature rather than the
baseline it always was.

Not all of it is branding, to be fair. Auto-approving confirmation prompts when the
caller is not a terminal is a real behavior change, and scoped OAuth tokens instead
of long-lived API keys is a real security decision. Both are worth shipping. Neither
is what the word agentic is doing in that sentence.

Two caveats, because I am painting broadly. What counts as a well-designed CLI is
partly subjective, which is why I named consistency and predictability rather than
something fuzzier. And the argument assumes your CLI can reach everything, which is
not automatic. Some of what magus can do you reach through `magus buzz` rather than
a subcommand of its own. Those are the same modules a magusfile calls, down the same
code paths, so it is one surface rather than a side door, but keeping it that way is
work I have to keep doing.

Where you are wrapping something you do not own or cannot change, a protocol layer
earns its place. But if your CLI needs an MCP server before an agent can drive it,
the problem sits upstream of the MCP server.

## The part I got wrong about my own argument

Here is where I have to update something I have said in public more than once.

I have been describing the MCP server as redundant, and mostly it is. The
descriptions attached to those tools are not, and that is the part I did not think
about carefully.

Read what `magus_run_target` tells an agent: use this instead of raw language tools,
because the raw tool bypasses the cache, the sandbox, and affected tracking. Read
`magus_describe_file`: it tells you to run it over a whole dirty tree before reading
diffs or committing, and what a declared output means. Read `magus_query`: prefer
this over grep, and here is when a bare free-text query will not match a code symbol.

That is teaching, not API documentation. It tells the caller which tool to reach for,
what mistake they are about to make, and what to do instead. I built it because
agents were getting it wrong without it, and it is the most useful thing in the
server.

The uncomfortable part is where I put it. All of that guidance lives in the MCP
descriptions, and a person running `magus run` on the command line never sees any of
it. I wrote a teaching layer and attached it to the surface that only machines read.

That is the finding, and it is worse than redundancy. Redundancy costs maintenance.
This one cost me the thing itself.

## Where the guidance belongs

A command line should nudge you when you get off track. Not by blocking you, and not
by scolding you, but by saying the thing you would have wanted to know. magus already
does this in places: a mistyped target gets the canonical spelling once, and more
than sixty diagnostic codes each carry a page explaining what tripped and what to do
instead.

The MCP descriptions are the same idea aimed at the smaller audience. Everything in
them worth saying to an agent is worth saying to a person, and the person is the one
who cannot page through a tool manifest before deciding what to run.

So the work is not deleting the MCP server. The work is moving what is good about it
into the CLI, where both readers get it, and then seeing what is left.

## What I am going to do

Close the drift first, because it is wrong regardless of what happens to the server.
The ledger needs a client in the Buzz module, the same way insight has one. That is
where a capability belongs here: the Buzz module is the layer every surface calls
into, so putting it there gives a magusfile, a `magus buzz` script, and anything
built on top of them the same access, rather than reserving it for whichever door I
happened to build most recently.

Then take the guidance out of the tool descriptions and put it in the CLI, as hints
and as errors that say what to do next. That is worth doing whether or not the server
survives.

After that I will look again. My guess is that what remains is twenty wrappers and no
argument for keeping them, and that removing the server costs nothing an agent
needs. But I would rather find that out by doing the work in the right order than by
acting on the instinct I started with, which is what I was about to do.

If it does get removed, it goes through the same
[compatibility contract](https://eli.gladman.cc/magus/concepts/compatibility/) as
anything else. It will not vanish out from under anyone.

One more thing worth saying, since I have spent a post complaining about people
shipping AI integrations instead of fixing their tools: I did the same thing. Not as
badly, and not instead of fixing the CLI, but I put teaching where the machines were
because that was where the complaints were coming from. The pressure that produces
that decision is real, I felt it, and I would not have caught it without sitting down
and making the table.

If any of this made you curious about the tool itself, there is
[more about magus](https://eli.gladman.cc/magus/).

[^pup]: Datadog, [Pup CLI](https://docs.datadoghq.com/cli/), announced as
    [live Datadog access for AI agents from the command
    line](https://www.datadoghq.com/blog/give-your-ai-agents-live-datadog-access-from-the-command-line/).
