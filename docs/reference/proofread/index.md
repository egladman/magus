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

Each rule also has a dimension, the cost its findings name to the reader:

- **evidence**: a claim says more or less than what was shown
- **stance**: the text reads as a verdict on a person, or as addressed to someone else
- **structure**: the reader has to reconstruct the order or the purpose
- **economy**: words that carry nothing
- **conventions**: a house or genre convention is broken

## Shape

| Rule                                        | Code    | Dimension   | Default        | Catches                                                                                               |
| ------------------------------------------- | ------- | ----------- | -------------- | ----------------------------------------------------------------------------------------------------- |
| [lead-context](lead-context.md)             | PRF1001 | structure   | deny           | a change description that does not open with a paragraph naming what a reader can now do              |
| [reply-voice](reply-voice.md)               | PRF1002 | structure   | deny           | text that answers a prompt the reader never saw: a reply opener, a bold-label item, a stock label     |
| [second-person](second-person.md)           | PRF1003 | conventions | deny           | we, us, our or ours in a guide, which speaks to the reader as you                                     |
| [step-verb](step-verb.md)                   | PRF1004 | structure   | deny           | a numbered step of a guide that does not open with its imperative verb                                |
| [subject-mood](subject-mood.md)             | PRF1010 | conventions | advise         | a commit subject opening in the past tense, the third person or a gerund ("added", "fixes", "making") |
| [subject-length](subject-length.md)         | PRF1011 | structure   | deny           | a commit subject over 100 bytes                                                                       |
| [subject-period](subject-period.md)         | PRF1012 | conventions | deny           | a commit subject ending in a period                                                                   |
| [body-separator](body-separator.md)         | PRF1013 | structure   | deny           | a commit body that starts on the line after the subject                                               |
| [issue-repro](issue-repro.md)               | PRF1020 | evidence    | advise         | a bug report with neither what happened against what was expected, nor steps to reproduce it          |
| [changelog-heading](changelog-heading.md)   | PRF1030 | conventions | deny           | a changelog version heading that is not "## [version] - date" or "## [Unreleased]"                    |
| [changelog-group](changelog-group.md)       | PRF1031 | conventions | varies by kind | a changelog heading that is not Added, Changed, Deprecated, Removed, Fixed or Security                |
| [changelog-entry](changelog-entry.md)       | PRF1032 | stance      | advise         | a changelog entry that names a Go identifier and says little else                                     |
| [voice-drift](voice-drift.md)               | PRF1040 | stance      | advise         | a text whose style measures outside its author's own range on two or more features of a voice file    |
| [suppression-unused](suppression-unused.md) | PRF1090 | evidence    | deny           | a suppression comment that gives no reason or that matched no finding                                 |

## Tone

| Rule                              | Code    | Dimension | Default | Catches                                                                                     |
| --------------------------------- | ------- | --------- | ------- | ------------------------------------------------------------------------------------------- |
| [blame](blame.md)                 | PRF2001 | stance    | off     | a person or past work as the subject of a fault, and contempt for code or a decision        |
| [verdict](verdict.md)             | PRF2002 | stance    | off     | a judgment word standing in for the behavior it judges ("was broken", "a mess")             |
| [absolute](absolute.md)           | PRF2003 | evidence  | advise  | never, nobody or nothing as a claim about the past ("has never fired", "nobody checked")    |
| [intent](intent.md)               | PRF2004 | stance    | off     | a motive given to a tool or a person ("guessed", "pretends")                                |
| [credit](credit.md)               | PRF2005 | stance    | advise  | a change description that removes or replaces something and says nothing of what it was for |
| [condescension](condescension.md) | PRF2006 | stance    | off     | a word that tells the reader how hard a step should feel or what they should already know   |

## Claims and hedges

| Rule              | Code    | Dimension | Default | Catches                                                                                |
| ----------------- | ------- | --------- | ------- | -------------------------------------------------------------------------------------- |
| [claim](claim.md) | PRF3001 | evidence  | advise  | a measurement, a comparison or a completion with no evidence in its sentence or bullet |
| [hedge](hedge.md) | PRF3002 | evidence  | off     | a softener qualifying a claim ("might fix", "could potentially")                       |

## Generated-writing tells

| Rule                                  | Code    | Dimension | Default        | Catches                                                                                                 |
| ------------------------------------- | ------- | --------- | -------------- | ------------------------------------------------------------------------------------------------------- |
| [filler](filler.md)                   | PRF4001 | economy   | advise         | throat-clearing ("Note that") and filler adverbs ("simply", "basically")                                |
| [wordy](wordy.md)                     | PRF4002 | economy   | varies by kind | a phrase with a shorter equivalent ("in order to")                                                      |
| [signpost](signpost.md)               | PRF4003 | structure | varies by kind | an announcement standing where the point should be ("Here's the thing", "Let's dive in")                |
| [chatbot](chatbot.md)                 | PRF4004 | stance    | off            | text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer                 |
| [leak](leak.md)                       | PRF4005 | evidence  | varies by kind | residue of a tool or a template: a citation marker or an unfilled placeholder                           |
| [buzzword](buzzword.md)               | PRF4006 | economy   | deny           | a word chosen to sound significant rather than to say what is so ("delve", "tapestry")                  |
| [buzzword-weak](buzzword-weak.md)     | PRF4007 | economy   | advise         | a buzzword that also has an ordinary sense ("crucial", "landscape")                                     |
| [vague](vague.md)                     | PRF4008 | evidence  | deny           | weight or consensus asserted with nothing named ("experts argue", "the stakes are high")                |
| [closer](closer.md)                   | PRF4009 | structure | deny           | a sentence that opens by announcing it restates the text above ("In conclusion,")                       |
| [contrast](contrast.md)               | PRF4010 | economy   | off            | a claim made by denying its opposite first ("not just X, it is Y")                                      |
| [ing-tail](ing-tail.md)               | PRF4011 | economy   | advise         | a participle clause added to claim significance (", highlighting the importance of")                    |
| [staccato](staccato.md)               | PRF4012 | structure | off            | three or more consecutive sentences of six words or fewer in one paragraph                              |
| [heading-case](heading-case.md)       | PRF4013 | structure | advise         | a heading whose every word after the first is capitalized                                               |
| [participles](participles.md)         | PRF4020 | economy   | advise         | a text that hangs present participial clauses on its sentences at a generated-writing rate              |
| [nominalizations](nominalizations.md) | PRF4021 | economy   | advise         | a text whose verbs are turned into nouns (validation, agreement, stability) at a generated-writing rate |

## House style

| Rule                          | Code    | Dimension   | Default     | Catches                                                                  |
| ----------------------------- | ------- | ----------- | ----------- | ------------------------------------------------------------------------ |
| [terms](terms.md)             | PRF5001 | conventions | off (house) | a spelling the glossary replaces ("sub-agent")                           |
| [dash](dash.md)               | PRF5002 | conventions | off (house) | an em dash, an en dash or a spaced double hyphen in prose                |
| [ascii](ascii.md)             | PRF5003 | conventions | off (house) | a curly quote, an ellipsis character or an emoji in prose                |
| [tense](tense.md)             | PRF5004 | conventions | off (house) | a claim in the future tense, or a first-person account of a change       |
| [attribution](attribution.md) | PRF5005 | conventions | off (house) | credit to a tool or an agent, or an account of how the work was produced |

## Doc comments

| Rule                                    | Code    | Dimension   | Default     | Catches                                                                    |
| --------------------------------------- | ------- | ----------- | ----------- | -------------------------------------------------------------------------- |
| [comment-block](comment-block.md)       | PRF6001 | economy     | off (house) | a doc comment over 250 words                                               |
| [comment-sentence](comment-sentence.md) | PRF6002 | economy     | off (house) | a doc comment sentence over 60 words                                       |
| [name-suffix](name-suffix.md)           | PRF6003 | conventions | off (house) | a function or method name whose last word is Of or For                     |
| [aside](aside.md)                       | PRF6004 | conventions | off (house) | a spaced hyphen spelling an em dash in a doc comment                       |
| [history](history.md)                   | PRF6005 | conventions | off (house) | a doc comment phrase narrating the change rather than the code ("used to") |
| [docstub](docstub.md)                   | PRF6006 | economy     | off (house) | a one-line doc comment that only repeats the symbol's name                 |

## Agent instructions

| Rule                                  | Code    | Dimension   | Default     | Catches                                                                                    |
| ------------------------------------- | ------- | ----------- | ----------- | ------------------------------------------------------------------------------------------ |
| [terse-sentence](terse-sentence.md)   | PRF7001 | economy     | off (house) | a sentence of agent instructions over 25 words                                             |
| [terse-paragraph](terse-paragraph.md) | PRF7002 | economy     | off (house) | a paragraph or list item of agent instructions over 60 words                               |
| [bare-rule](bare-rule.md)             | PRF7003 | conventions | off (house) | "rule" in agent instructions with no mechanism named                                       |
| [template](template.md)               | PRF7004 | structure   | off (house) | an agent-instructions template that does not render, so neither of its forms can be judged |

## Review replies

| Rule                                          | Code    | Dimension | Default | Catches                                                                                          |
| --------------------------------------------- | ------- | --------- | ------- | ------------------------------------------------------------------------------------------------ |
| [reply-opener](reply-opener.md)               | PRF8001 | stance    | off     | a sentence of a review reply that opens by contradicting ("No,", "As I said")                    |
| [judgment-as-fact](judgment-as-fact.md)       | PRF8002 | stance    | off     | a recommendation in a review reply stated as a fact, with no reason given                        |
| [stacked-hedge](stacked-hedge.md)             | PRF8003 | stance    | advise  | two softeners in one sentence of a review reply, or an apology before its point                  |
| [long-thread](long-thread.md)                 | PRF8004 | stance    | advise  | a review reply that is its author's fourth or later in a thread                                  |
| [nonspecific](nonspecific.md)                 | PRF8010 | evidence  | advise  | a review reply that judges or asks for a change and names no code, path, line, example or reason |
| [why-opener](why-opener.md)                   | PRF8011 | stance    | advise  | a review reply sentence that opens "Why did you" or "Why would you"                              |
| [bare-imperative](bare-imperative.md)         | PRF8012 | stance    | off     | a short command in a review reply that gives no reason anywhere ("Fix this.")                    |
| [all-caps](all-caps.md)                       | PRF8013 | stance    | off     | words in capitals for emphasis in a review reply ("DO NOT", "NEVER")                             |
| [repeated-marks](repeated-marks.md)           | PRF8014 | stance    | off     | a run of question or exclamation marks in a review reply ("??", "!!", "?!")                      |
| [bold-label](bold-label.md)                   | PRF8020 | structure | advise  | list items or paragraphs of an agent reply that open with a bold label                           |
| [closing-offer](closing-offer.md)             | PRF8021 | stance    | advise  | an agent reply whose last paragraph offers more work or asks leave to go on                      |
| [request-recap](request-recap.md)             | PRF8022 | economy   | advise  | an agent reply that opens by restating what the person asked                                     |
| [option-list](option-list.md)                 | PRF8023 | structure | advise  | an agent reply that lays out labeled options or alternatives                                     |
| [unbacked-done](unbacked-done.md)             | PRF8024 | evidence  | advise  | a claim of done, fixed, verified or passing with no command, output ref, file or link beside it  |
| [short-reply-heading](short-reply-heading.md) | PRF8025 | structure | advise  | a heading in an agent reply under 300 words                                                      |
| [agreement-opener](agreement-opener.md)       | PRF8026 | stance    | advise  | an agent reply that opens with praise or agreement ("Great question", "You're right")            |

## Messages a program prints

| Rule                                      | Code    | Dimension   | Default | Catches                                                                                                |
| ----------------------------------------- | ------- | ----------- | ------- | ------------------------------------------------------------------------------------------------------ |
| [message-length](message-length.md)       | PRF9001 | economy     | deny    | a message longer than its rune cap, 160 unless the caller names one                                    |
| [message-rationale](message-rationale.md) | PRF9002 | structure   | deny    | a message that joins more than one reason (so, because, a semicolon, ", which")                        |
| [message-commands](message-commands.md)   | PRF9003 | structure   | deny    | a message naming more than one backticked command                                                      |
| [message-tag](message-tag.md)             | PRF9004 | conventions | deny    | a message opening with a component tag ("server: ") or carrying a marker such as "[AGENT]"             |
| [help-sentence](help-sentence.md)         | PRF9010 | economy     | deny    | a sentence of help text over 40 words                                                                  |
| [help-length](help-length.md)             | PRF9011 | economy     | deny    | help text over 240 runes                                                                               |
| [tool-lead](tool-lead.md)                 | PRF9020 | structure   | advise  | a description whose first sentence opens on the tool itself, holds under 4 words or runs past 40 words |
| [tool-boundary](tool-boundary.md)         | PRF9021 | evidence    | advise  | a description that names no case where the tool is the wrong choice and no tool to use instead         |
| [tool-length](tool-length.md)             | PRF9022 | economy     | advise  | a tool or skill description over 1024 runes                                                            |
| [tool-selling](tool-selling.md)           | PRF9023 | stance      | deny    | a word that sells the tool (powerful, seamless, effortless, state-of-the-art) or a buzzword            |
