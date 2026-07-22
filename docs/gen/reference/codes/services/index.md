---
title: services diagnostics
page_type: overview
description: Landing page for MGS5xxx diagnostics that flag problems with long-running service ops, such as near-duplicate services that should be shared instead of run as separate processes.
tags: [services, diagnostics, error codes, MGS5xxx, service op, doctor, sharing]
---

# Services diagnostics

Codes in the `MGS5xxx` range flag problems with long-running [service
ops](../../../concepts/operations.md): services that will run as separate processes when they
look like they should be one shared instance, and (later) op invocations that
contradict their declared kind. Magus raises them at run time (as a typed
`DiagnosticError`) and, where applicable, as a `magus doctor` health check so the
gap is visible before the work runs.

## Codes

- [MGS5001](MGS5001.md): near-duplicate services that will run as separate processes.
- [MGS5002](MGS5002.md): a service op detaches, breaking foreground supervision.
- [MGS5003](MGS5003.md): a command op runs a watcher and never exits.
