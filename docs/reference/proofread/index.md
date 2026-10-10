---
title: "Proofread rules"
description: "Every rule proofread applies to written text: what each one catches, the decision it takes by default on each kind of text, and its code."
tags: [proofread, rules, reference]
aliases: [reference/prose]
---

# Proofread rules

What proofread checks in the text a person or an agent writes. Each rule has a
default decision on each kind of text: a **deny** is refused by the caller, an
**advise** is reported once and lets the text through, and an **off** rule, house
style, reports nothing until a repository turns it on. A decisions table changes any
of them.

A finding carries the rule's name and its `PRF` code, and links to the rule's page
here. The first digit of a code is its family. `proofread rules` prints the
same list as JSON.

## Shape

| Rule                              | Code    | Default | Catches                                                                                           |
| --------------------------------- | ------- | ------- | ------------------------------------------------------------------------------------------------- |
| [lead-context](lead-context.md)   | PRF1001 | deny    | a change description that does not open with a paragraph naming what a reader can now do          |
| [reply-voice](reply-voice.md)     | PRF1002 | deny    | text that answers a prompt the reader never saw: a reply opener, a bold-label item, a stock label |
| [second-person](second-person.md) | PRF1003 | deny    | we, us, our or ours in a guide, which speaks to the reader as you                                 |
| [step-verb](step-verb.md)         | PRF1004 | deny    | a numbered step of a guide that does not open with its imperative verb                            |

## Tone

| Rule                              | Code    | Default | Catches                                                                                     |
| --------------------------------- | ------- | ------- | ------------------------------------------------------------------------------------------- |
| [blame](blame.md)                 | PRF2001 | deny    | a person or past work as the subject of a fault, and contempt for code or a decision        |
| [verdict](verdict.md)             | PRF2002 | advise  | a judgment word standing in for the behavior it judges ("was broken", "a mess")             |
| [absolute](absolute.md)           | PRF2003 | advise  | never, nobody or nothing as a claim about the past ("has never fired", "nobody checked")    |
| [intent](intent.md)               | PRF2004 | advise  | a motive given to a tool or a person ("guessed", "pretends")                                |
| [credit](credit.md)               | PRF2005 | advise  | a change description that removes or replaces something and says nothing of what it was for |
| [condescension](condescension.md) | PRF2006 | deny    | a word that tells the reader how hard a step should feel or what they should already know   |

## Claims and hedges

| Rule              | Code    | Default | Catches                                                                                |
| ----------------- | ------- | ------- | -------------------------------------------------------------------------------------- |
| [claim](claim.md) | PRF3001 | advise  | a measurement, a comparison or a completion with no evidence in its sentence or bullet |
| [hedge](hedge.md) | PRF3002 | deny    | a softener qualifying a claim ("might fix", "could potentially")                       |

## Generated-writing tells

| Rule                              | Code    | Default        | Catches                                                                                  |
| --------------------------------- | ------- | -------------- | ---------------------------------------------------------------------------------------- |
| [filler](filler.md)               | PRF4001 | deny           | throat-clearing ("Note that") and filler adverbs ("simply", "basically")                 |
| [wordy](wordy.md)                 | PRF4002 | deny           | a phrase with a shorter equivalent ("in order to")                                       |
| [signpost](signpost.md)           | PRF4003 | deny           | an announcement standing where the point should be ("Here's the thing", "Let's dive in") |
| [chatbot](chatbot.md)             | PRF4004 | deny           | text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer  |
| [leak](leak.md)                   | PRF4005 | deny           | residue of a tool or a template: a citation marker or an unfilled placeholder            |
| [buzzword](buzzword.md)           | PRF4006 | deny           | a word chosen to sound significant rather than to say what is so ("delve", "tapestry")   |
| [buzzword-weak](buzzword-weak.md) | PRF4007 | advise         | a buzzword that also has an ordinary sense ("crucial", "landscape")                      |
| [vague](vague.md)                 | PRF4008 | deny           | weight or consensus asserted with nothing named ("experts argue", "the stakes are high") |
| [closer](closer.md)               | PRF4009 | deny           | a sentence that opens by announcing it restates the text above ("In conclusion,")        |
| [contrast](contrast.md)           | PRF4010 | varies by kind | a claim made by denying its opposite first ("not just X, it is Y")                       |
| [ing-tail](ing-tail.md)           | PRF4011 | advise         | a participle clause added to claim significance (", highlighting the importance of")     |
| [staccato](staccato.md)           | PRF4012 | varies by kind | three or more consecutive sentences of six words or fewer in one paragraph               |
| [heading-case](heading-case.md)   | PRF4013 | advise         | a heading whose every word after the first is capitalized                                |

## House style

| Rule                          | Code    | Default     | Catches                                                                  |
| ----------------------------- | ------- | ----------- | ------------------------------------------------------------------------ |
| [terms](terms.md)             | PRF5001 | off (house) | a spelling the glossary replaces ("sub-agent")                           |
| [dash](dash.md)               | PRF5002 | off (house) | an em dash, an en dash or a spaced double hyphen in prose                |
| [ascii](ascii.md)             | PRF5003 | off (house) | a curly quote, an ellipsis character or an emoji in prose                |
| [tense](tense.md)             | PRF5004 | off (house) | a claim in the future tense, or a first-person account of a change       |
| [attribution](attribution.md) | PRF5005 | off (house) | credit to a tool or an agent, or an account of how the work was produced |

## Doc comments

| Rule                                    | Code    | Default     | Catches                                                                    |
| --------------------------------------- | ------- | ----------- | -------------------------------------------------------------------------- |
| [comment-block](comment-block.md)       | PRF6001 | off (house) | a doc comment over 250 words                                               |
| [comment-sentence](comment-sentence.md) | PRF6002 | off (house) | a doc comment sentence over 60 words                                       |
| [name-suffix](name-suffix.md)           | PRF6003 | off (house) | a function or method name whose last word is Of or For                     |
| [aside](aside.md)                       | PRF6004 | off (house) | a spaced hyphen spelling an em dash in a doc comment                       |
| [history](history.md)                   | PRF6005 | off (house) | a doc comment phrase narrating the change rather than the code ("used to") |
| [docstub](docstub.md)                   | PRF6006 | off (house) | a one-line doc comment that only repeats the symbol's name                 |

## Agent instructions

| Rule                                  | Code    | Default     | Catches                                                                                    |
| ------------------------------------- | ------- | ----------- | ------------------------------------------------------------------------------------------ |
| [terse-sentence](terse-sentence.md)   | PRF7001 | off (house) | a sentence of agent instructions over 25 words                                             |
| [terse-paragraph](terse-paragraph.md) | PRF7002 | off (house) | a paragraph or list item of agent instructions over 60 words                               |
| [bare-rule](bare-rule.md)             | PRF7003 | off (house) | "rule" in agent instructions with no mechanism named                                       |
| [template](template.md)               | PRF7004 | off (house) | an agent-instructions template that does not render, so neither of its forms can be judged |

## Review replies

| Rule                                    | Code    | Default | Catches                                                                         |
| --------------------------------------- | ------- | ------- | ------------------------------------------------------------------------------- |
| [reply-opener](reply-opener.md)         | PRF8001 | deny    | a sentence of a review reply that opens by contradicting ("No,", "As I said")   |
| [judgment-as-fact](judgment-as-fact.md) | PRF8002 | advise  | a recommendation in a review reply stated as a fact, with no reason given       |
| [stacked-hedge](stacked-hedge.md)       | PRF8003 | advise  | two softeners in one sentence of a review reply, or an apology before its point |
| [long-thread](long-thread.md)           | PRF8004 | advise  | a review reply that is its author's fourth or later in a thread                 |
