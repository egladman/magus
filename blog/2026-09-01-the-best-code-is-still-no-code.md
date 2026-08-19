---
title: "The best code is still no code"
description: "A dependency is a bill you hand your user. A feature is a bill you hand whoever maintains the thing next. Neither one lands on the person who added it, which is most of why we keep adding."
tags: [opinion]
date: 2026-09-01
draft: true
---

# The best code is still no code

<!-- Cut from the 2026-08-18 tools post, where it had grown into its own argument. -->

Every line you ship is a bill somebody else pays. Sometimes it goes to the person
installing your tool. Sometimes it goes to whoever maintains the thing after you
leave. It almost never lands on the person who wrote it, and that is most of why we
keep adding.

Two versions of that below. What you pull into a command line tool, and what you keep
adding to a product that already works.

## What your user sees when your CLI fails

A JavaScript runtime stack trace is not an error message. It is the tool's internals
dumped on somebody who wanted to know what to do next, with the one human-readable
line buried in the middle of it, if it is there at all. We are in 2026 and this is
still how a large share of command line tools report failure. I do not understand why
we are still writing CLIs in TypeScript.

Use the right tool for the job. A CLI is a program a stranger runs on a machine you
will never see, and that argues for something compiled, self-contained, and able to
fail in a sentence. magus ships as one statically linked binary. No runtime, no
`node_modules`, no second toolchain to install.

That last part stopped being a taste argument a while ago. A CLI that pulls a few
hundred transitive dependencies at install time is a CLI with a few hundred chances
to hand somebody else's code to your users. The npm ecosystem has been learning that
in public, through one supply chain compromise after another, and the lesson looks
like it is landing. It is a strange thing to feel vindicated about, because everybody
downstream paid for the lesson.

The frameworks that grew out of that ecosystem strike me the same way: overengineered,
and re-deriving things computer science settled decades ago.

## The pile is not the progress

We can do better than this as an industry, and I mean that as a challenge rather than
a complaint. Shipping a pile of code has never been easier, and it has never been
easier to mistake the pile for progress. Easy to write does not mean good, well
architected, or worth maintaining. Lines of code measures none of that. If we are
going to count anything, fewer lines is the number worth promoting.

That pressure got worse this year. Writing a feature is close to free now, so the only
thing between a codebase and endless creep is somebody deciding not to add one. Jeff
Atwood said it in 2007 and it has only gotten truer: the best code is no code at
all,[^atwood] because every line you bring into the world has to be debugged, read,
understood, and supported. How cheap it was to write changes none of that.

There is research on why we are bad at this. A 2021 Nature paper, people
systematically overlook subtractive changes,[^subtract] found that people handed a
problem reach for what they can add and barely consider what they
could take away, even where removing something is the better answer. It has a name,
additive bias, and it turns up in every code review I sit in. Deleting a feature reads
as a loss. It is usually a gift to whoever maintains the thing next, and we do not
take it seriously enough.

## Enough is enough

Some of that pressure is financial. A company with investors has to show the line
going up every quarter, and "the tool is finished, it does what it should" does not
raise a round. So features keep landing after the useful ones are done, and eventually
you are shipping change at the people who liked it the way it was.

Endless growth does not work in an economy and it does not work in software. There is
a point where enough is enough, and as code gets cheaper to write, having the
forethought to notice that point matters more than it used to.

[^atwood]: Jeff Atwood, [The Best Code is No Code At
    All](https://blog.codinghorror.com/the-best-code-is-no-code-at-all/), 2007.

[^subtract]: Adams, Converse, Hales and Klotz, [People systematically overlook
    subtractive changes](https://www.nature.com/articles/s41586-021-03380-y),
    Nature 592, 2021.
