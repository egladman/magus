---
title: "I audited my own MCP server"
description: "magus exposes 22 MCP tools. Twenty of them wrap a command the CLI already has. The two that do not turned out to be bugs."
tags: [opinion]
date: 2026-08-25
draft: true
---

# I audited my own MCP server

In the last post I said that a well-designed CLI does not need an MCP server, that
magus ships one anyway, and that I was seriously considering removing it. That was a
position I held on instinct. This is me checking it against the actual surface, which
is a thing I could have done at any point and had not.

First, scope. MCP covers a lot of ground and I am not arguing with most of it. I am
talking about one shape: an MCP server that exists so an AI coding assistant can
drive a tool already sitting on your machine. Wrapping a remote API, or a service
with no command line at all, is a different question with a different answer.

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
| `magus_insight`        | nothing. See below        |
| `magus_ledger`         | nothing. See below        |

Twenty of the twenty-two are a wrapper over something the CLI already does. Not
loosely equivalent, not a convenience shape over several commands: the same
capability, reachable two ways, documented twice, and maintained twice.

Two of them are already duplicated inside the MCP surface itself. `magus_output` and
`magus_tail_log` both fetch a captured run log, and both correspond to
`magus query output`. I did not notice until I put them in a table.

## The two that are not wrappers are both defects

I expected the leftovers to be the interesting part, the places where a protocol
layer had earned something. That is not what they turned out to be.

**`magus_insight` wraps a command that no longer exists.** The tool is still exposed,
still carries its full description, still advertises six lenses: hotspots, files,
affinity, ownership, trend, unreferenced. `magus insight` was removed from the CLI.
An agent that calls the tool is reaching for a capability the command line cannot
reach any more. The MCP surface drifted off the thing it wraps and nothing caught it,
because nothing was checking that the two agreed.

**`magus_ledger` has no CLI equivalent at all.** There is no `magus ledger`
subcommand. The delegation ledger is reachable over MCP and nowhere else, which
inverts the rule I claim to hold. It is the one place in this whole surface where the
MCP server is not redundant, and the reason it is not redundant is that I let a
capability land there first and never gave it a command. That is not MCP earning its
place. That is me skipping a step.

So the audit did not find a protocol layer doing work the CLI could not. It found
twenty duplicates, one stale tool pointing at a deleted command, and one capability
that went in the wrong door.

## The part I got wrong about my own argument

Here is where I have to update something I have said in public more than once.

I have been describing the MCP server as redundant, and mostly it is. But the
descriptions attached to those tools are not redundant, and that is the part I did
not think carefully about.

Read what `magus_run_target` tells an agent: use this instead of raw language tools,
because the raw tool bypasses the cache, the sandbox, and affected tracking. Read
`magus_describe_file`: it tells you to run it over a whole dirty tree before reading
diffs or committing, and what a declared output means. Read `magus_query`: prefer
this over grep, and here is when a bare free-text query will not match a code symbol.

Those are not API descriptions. They are teaching. They tell the caller which tool to
reach for, what mistake they are about to make, and what to do instead. That is real
value and I built it because agents were getting it wrong without it.

The uncomfortable part is where I put it. All of that guidance lives in the MCP
descriptions, and a person running `magus run` on the command line never sees any of
it. I wrote a teaching layer and then attached it to the surface that only machines
read.

That is the actual finding, and it is worse than redundancy. Redundancy costs
maintenance. This cost me the thing itself: I solved the right problem, in the wrong
place, for the wrong audience.

## Where the guidance belongs

A command line should nudge you when you get off track. Not block you, not scold you,
just say the thing you would have wanted to know. magus already does this in places:
a mistyped target gets the canonical spelling once, and more than sixty diagnostic
codes each carry a page explaining what tripped and what to do instead.

The MCP descriptions are the same idea, aimed at the smaller audience. Every one of
them that is worth saying to an agent is worth saying to a person, and the person is
the one who cannot page through a tool manifest before deciding what to run.

So the work is not deleting the MCP server. The work is moving what is good about it
into the CLI, where both readers get it, and then seeing what is left.

## What I am going to do

Fix the two defects first, because they are bugs regardless of what happens to the
server. `magus_insight` goes, since the command it wraps is gone. `magus_ledger` gets
a `magus ledger` subcommand, because a capability reachable only over a protocol
layer is exactly the thing I spent a whole post arguing against.

Then the interesting one: take the guidance out of the tool descriptions and put it
in the CLI, as hints and as errors that say what to do next. That is worth doing
whether or not the server survives.

After that I will look again. My guess is that what remains is twenty wrappers and no
argument for keeping them, and that removing the server costs nothing an agent
actually needs. But I would rather find that out by doing the work in the right order
than by acting on the instinct I started with, which is what I was about to do.

If it does get removed, it goes through the same
[compatibility contract](https://eli.gladman.cc/magus/concepts/compatibility/) as
anything else. It will not vanish out from under anyone.

One more thing worth saying plainly, since the first post was partly about people
shipping AI integrations instead of fixing their tools: I did the same thing. Not as
badly, and not instead of fixing the CLI, but I put teaching where the machines were
because that was where the complaints were coming from. The pressure that produces
that decision is real, and I felt it, and I would not have caught it if I had not sat
down and made the table.

If any of this made you curious about the tool itself, there is
[more about magus](https://eli.gladman.cc/magus/).
