---
title: charm diagnostics
page_type: overview
description: Landing page for MGS6xxx diagnostics that flag problems with how a charm's JSON Patch interacts with a target's command.
tags: [charms, diagnostics, error codes, MGS6xxx, json-patch, describe, argv]
---

# Charm diagnostics

Codes in the `MGS6xxx` range flag problems with how a [charm](../../../concepts/charms.md)
interacts with the command it patches: a patch that is valid in shape but does
not apply to a target's argv, and similar mismatches between a charm's declared
intent and the command it lands on. Magus raises them as a typed
`DiagnosticError`, most often from a static preview (`magus describe`) so the gap
is visible before a run.

## Codes

- [MGS6001](MGS6001.md): a charm's patch is well-formed but does not apply to the target's command.
