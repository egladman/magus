---
title: Merge queue
description: magus vcs queue validates every change carrying merge intent speculatively and in parallel, then lands each as its own commit.
tags: [merge-queue, queue, pull-request, auto-merge, speculation, github, provider]
---

# Merge queue

Enable auto-merge on a pull request to queue it. `magus vcs queue` stages each approved
change on main plus those ahead of it, gating up to `--depth` stages at once, each running
only what its change adds. Disjoint changes never wait for each other. `--land` merges each
green change as its own commit, by its author, from a separate write-scoped job.
