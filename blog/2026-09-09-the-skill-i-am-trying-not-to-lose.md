---
title: "The skill I am trying not to lose"
description: "I said the worry underneath all this is atrophy and then left it there. Here is what the research actually supports, the one claim I will defend, and the numbers that make it awkward for me to defend it."
tags: [opinion]
date: 2026-09-09
draft: true
---

# The skill I am trying not to lose

In the tools post I wrote that the worry underneath all of this is atrophy, that
craft behaves like muscle and there are parts of mine I have stopped working. I
left it there, because I had nothing behind it but a feeling. I have been reading
since, and I have some numbers about my own habits now, so here is the
follow-through.

## What I have to concede first

I build this thing with agents, constantly. Over twenty-one days, agents ran
113,430 shell commands in this repository. I did not go looking for that figure
to make a point; it fell out of other work on session data, and it is what makes
the rest of this awkward to write.

magus ships a guard: a hook that judges a command before an agent runs it and
refuses the ones that destroy work or route around the build. It fails open on
purpose, because a guard that fails closed takes your whole session down with it
the moment the binary is missing. So I audited it against my own sessions. In 22
of 203 recent sessions the guard was not running at all, and those sessions
carried 17% of every command. The mechanism I would point at if you asked how I
keep this honest was off for roughly one session in nine, and I only know because
I went and checked.

Then there is where I put the teaching. From the wrappers post, about magus's own
MCP server: "I wrote a teaching layer and attached it to the surface that only
machines read." Everything I know about driving this tool well, when to query the
graph instead of grepping, what a declared output means, which mistake you are
about to make, lives where a person at a terminal never encounters it. If I am
going to argue that the operator's skill is what deserves protecting, I should
say plainly that I spent a year teaching the models instead.

None of this is written by somebody who abstained.

## What the research actually supports

I went looking for the study that says this makes you worse, because I half
believed it. It is not there in the shape people quote it in.

| finding | source |
| --- | --- |
| A tutor that gave answers: 48% better while students had it, 17% worse than control once it was withdrawn. A tutor that gave hints instead: 127% better, with the later damage gone. | Bastani et al., PNAS 122(26), 2025 |
| Confidence in the tool predicts less critical thinking. Confidence in yourself predicts more. | Lee, Sarkar et al., CHI 2025 |
| 61.4% of AI-generated pull requests show no review activity at all, against 34.5% of human ones. | Duma et al., EASE 2026, [arXiv 2605.02273](https://arxiv.org/abs/2605.02273) |
| 77% of one group could not finish a thirty-minute task with the assistant taken away, against 39% of a group whose tool required a stated reason before accepting output. No cost to output while the tool was on. | Sankaranarayanan, [arXiv 2602.20206](https://arxiv.org/abs/2602.20206). A preprint, so weigh it as such |
| Automate the easy parts and what is left for the operator is the hard part, in worse conditions, with less practice. | Bainbridge, 1983 |

The famous result, developers 19% slower while believing they were faster, needs
its own sentence, because METR walked it back themselves: their 2026 rerun puts
the effect in a confidence interval that crosses zero. The part I still find
damning has nothing to do with speed. Those developers were wrong about their own
experience in the direction that flattered them, which is why I do not trust my
own sense of whether any of this is working for me.

The other direction has evidence too, and I am not going to bury it. A 2026 study
found cognitive offloading positively associated with self-efficacy, which is
roughly the opposite of the story I would like to be telling.

Two studies I am not citing, both of which you have probably seen: the one with
the EEG scans is an unreviewed preprint with a published methodological critique
and no replication, and the other one I could not reach a primary source for at
all. They are the two most shared and the two weakest, which is its own finding
about how this gets discussed.

## The claim I will make

Bastani is the one I keep returning to, because of the third cell. Answers made
students better while they had them and worse afterwards. Hints made them much
better and left nothing behind. That is not a compromise between using this stuff
and not using it. Heavy use with the judgment kept in is the better cell on both
axes, and I do not have to argue anybody down to a smaller life to defend it.

The skill at risk has a name in this year's work, and the name is good:
corrective competence. You can ship code that works and be unable to fix it when
the assistant is wrong or gone. That is what the 77% measures. It says nothing
about knowledge or about whether you understand the domain. It asks whether you
can take over.

So the claim is narrow on purpose. I am not saying agents make you stupid, and I
am not saying they make you slow, which the evidence settles against me anyway. I
am saying that repairing the thing yourself is a different skill from producing
it, and that it degrades quietly, because nothing in an ordinary week tests it.

## What magus does about it

Four mechanisms, all of them already in the tool.

magus never calls a model. There is no adapter, deliberately, and every capability
in the tool runs with no agent anywhere in the loop. That is what makes the
promise on the doctrine page real rather than decorative: a workspace that goes a
Friday without an agent loses nothing but speed.

The knowledge store is human-authored by construction. There is no author field
to spoof, because authorship rides version control, and no agent surface writes
notes.

The read receipt is deliberately hard to perform. `--ack` records that you read a
changeset, it refuses to run without a terminal, agent hosts are denied it
outright, and the count is never shown to anyone but you. There is no team view
and no pull-request comment, because a read measure a second person can see
becomes a number people game.

The guard advises far more than it denies, and denies only what cannot be undone.
A refusal that leaves you with no next step just sends you around the tool, where
nothing can account for the work.

## What magus cannot tell you

I cannot answer the question this piece is about with my own tool, today.

magus does not know which code a human wrote and which an agent wrote. The
session records exist, but the field that would say which host produced a write
comes through empty from the CLI, and the two session stores do not join. No git
trailer is written or read anywhere. When an agent consults magus through MCP,
the consultation is recorded without attribution, so I cannot tell you who asked.
There is no node kind for a decision either: I can query what changed and who
committed it, and not who decided.

Which means the drift I am worried about is exactly the drift I have no
instrument for. So the next thing to build is a way to measure it, and I would
rather spend the effort there than on another rule I would be the first to route
around.

## Could and should

The other half of what started this: just because you can build something does
not mean you should. I do not think that sentiment survives being said out loud.
Everybody agrees with it and it changes nobody's behavior, mine included.

What does survive is a list. The doctrine page now carries a record of refusals:
the things magus could have had and does not, or had and lost, with what decided
each. An advisory I wanted, killed because the behavior it would have caught
happened 0.8% of the time and every case I read by hand was a false positive. A
denial I was ready to ship, dropped after measuring that it would fire on 45
commands in the entire corpus and answer maybe 60% of them correctly. A guard
verdict I built across fourteen files and reverted the same day, once I found
that two of the four host integrations would have silently allowed what it was
meant to stop. Machine-wide admission control, deleted. A rotation trigger,
deleted rather than keep promising something it could not deliver without a
daemon running. Forty-seven half-finished features audited before I shared any of
this, each one killed, finished, or pinned with its reason, and one of the kills
was wrong and got reversed.

That list is the only version of the sentiment I trust, because you can check it
against the repository and tell me where I am wrong. My restraint on its own is a
mood, and it moves with how interesting the idea looked that week.
