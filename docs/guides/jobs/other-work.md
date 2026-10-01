---
title: Splitting other work
description: Use magus jobs to split work that is not a refactor - a dependency upgrade one project at a time, a translation divided by page and heading, and a release checklist whose board is magus ls jobs.
tags:
  [
    jobs,
    magus job,
    dependencies,
    upgrades,
    translation,
    docs,
    release,
    checklist,
    write paths,
  ]
---

# Splitting other work

The [jobs guide](../jobs.md) splits a code change. The same commands split any
work whose pieces are files: each job names the files it may write, so two people
cannot quietly take the same slice, and `magus job wait` tells the person keeping
the list whether a piece is done. Three examples, each verified the way the first
page's are: the transcripts are compared byte for byte by
`cmd/magus/testdata/script/job_people_*.txtar`.

## A dependency upgrade, one project at a time

A shop has two projects, `api` and `web`, each pinning a library in its own
`deps.txt` and each with its own `test` target. The upgrade splits one job per
project, declared together in one stream:

<!-- golden: job_people_upgrade.txtar apply.out -->

```console
$ magus job apply -f - <<'EOF'
{"schema_version": 11, "id": "upgrade/api", "criteria": "api runs left-pad 1.3", "write_paths": ["api/deps.txt"], "check": {"target": "test", "project": "api"}}
{"schema_version": 11, "id": "upgrade/web", "criteria": "web runs left-pad 1.3", "write_paths": ["web/deps.txt"], "check": {"target": "test", "project": "web"}}
EOF
created upgrade/api, declared, with 1 write path(s)
created upgrade/web, declared, with 1 write path(s)
```

Each job's check is that project's own test, so `api` passing says nothing about
`web`. Ana takes `upgrade/api` in her worktree and Ben takes `upgrade/web` in his,
each with `magus job exec`. Who holds what, from anywhere:

<!-- golden: job_people_upgrade.txtar holders.out -->

```console
$ magus ls jobs -o template='{{range .jobs}}{{.id}}  {{.checkout_root}}{{"\n"}}{{end}}'
upgrade/api  ~/src/ana
upgrade/web  ~/src/ben
```

Each bumps the pin, runs `magus run test api` or `magus run test web`, and exits the
job with the run's ref, as on the first page. Whoever keeps the list checks each
piece:

<!-- golden: job_people_upgrade.txtar wait-api.out -->

```console
$ magus job wait upgrade/api
verified upgrade/api, recorded pass
its holder reports it ran magus run test api
goal check: verified (out<hex>)
footprint: where its diff since the checkpoint landed
  api/deps.txt:1-1
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

and the whole set is done when every row reads `pass`:

```sh
for job in upgrade/api upgrade/web; do
  magus job wait "$job" > /dev/null || echo "$job is not done"
done
```

<!-- golden: job_people_upgrade.txtar ls-done.out -->

```console
$ magus ls jobs
JOB          HOLDER   STATE  MODEL  PATHS  PROOF  CHECK
upgrade/api  session  pass   -      1      alone  magus run test api
upgrade/web  session  pass   -      1      alone  magus run test web

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

A job holding `upgrade/api` that also changed web's `deps.txt` would not pass: wait
holds every changed line to the job's write paths.

## Translating pages without sharing one

Two translators divide a docs site by page. The repository marks Markdown with git's
Markdown diff driver, which is what lets magus see headings inside a page:

<!-- golden: job_people_pages.txtar site/.gitattributes -->

```text
*.md diff=markdown
```

Ana forks `fr/install` on the French install page and takes it in her worktree. Ben,
in his, claims the same page for the section he wants:

<!-- golden: job_people_pages.txtar refused.out -->

```console
$ magus job fork fr/upgrading --criteria 'upgrading section in French' --write-paths docs/fr/install.md --check 'lint .'
[error] magus job fork: [MGS3032] job: fr/upgrading declares "docs/fr/install.md", and fr/install (declared, updated 0s ago) already holds "docs/fr/install.md". Both are live and neither is the other's parent, child, or depends_on partner, so docs/fr/install.md has one owner unless the rows say otherwise. fr/install has touched none yet in it so far. Claim `docs/fr/install.md#<declaration>` on both rows, add `--depends-on fr/install` to fr/upgrading, or fold fr/upgrading into fr/install
  see: https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3032/
```

Refused, with no row written: the page is held in another checkout, so it has one
owner until the rows say otherwise. The refusal names the ways out. They split the
page by heading, which narrows Ana's job and declares Ben's in one stream:

<!-- golden: job_people_pages.txtar apply.out -->

```console
$ magus job apply -f - <<'EOF'
{"schema_version": 11, "id": "fr/install", "criteria": "install page in French", "write_paths": ["docs/fr/install.md#Installer"], "check": {"target": "lint", "project": "."}}
{"schema_version": 11, "id": "fr/upgrading", "criteria": "upgrading section in French", "write_paths": ["docs/fr/install.md#Mettre à jour"], "check": {"target": "lint", "project": "."}}
EOF
updated fr/install, still declared:
  write_paths +docs/fr/install.md#Installer -docs/fr/install.md
created fr/upgrading, declared, with 1 write path(s)
```

The part after `#` names a heading the diff driver matches. Once Ben takes his job,
the board shows the shared page and why it is not a conflict:

<!-- golden: job_people_pages.txtar ls.out -->

```console
$ magus ls jobs
JOB           HOLDER   STATE     MODEL  PATHS  PROOF     CHECK
fr/install    session  declared  -      1      alone     magus run lint .
fr/upgrading  session  declared  -      1      disjoint  magus run lint .

overlaps
  fr/install and fr/upgrading claim common ground
    fr/install: docs/fr/install.md#Installer
    fr/upgrading: docs/fr/install.md#Mettre à jour
    claims: disjoint (different declarations of one file: an integration order, not a wait)
    footprints: disjoint

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

When each exits, `magus job wait` places every changed line under the heading it
landed in, and a line in the other translator's section is named as a violation.

## A release checklist

Each item on a release checklist is a job whose goal says what done means. A goal
needs no target: `paths` goals read the diff and the tree.

<!-- golden: job_people_release.txtar apply.out -->

```console
$ magus job apply -f - <<'EOF'
{"schema_version": 11, "id": "release-1.3/changelog", "criteria": "CHANGELOG.md has a 1.3 section", "write_paths": ["CHANGELOG.md"], "goals": [{"id": "changelog", "kind": "paths", "expect": "changed", "paths": ["CHANGELOG.md"]}]}
{"schema_version": 11, "id": "release-1.3/notes", "criteria": "release notes exist for 1.3", "write_paths": ["notes/1.3.md"], "goals": [{"id": "notes", "kind": "paths", "expect": "present", "paths": ["notes/1.3.md"]}]}
EOF
created release-1.3/changelog, declared, with 1 write path(s)
created release-1.3/notes, declared, with 1 write path(s)
```

Two people take an item each in their own worktree. The changelog gets written. The
notes are blocked on numbers that do not exist yet, so their holder says so on the
way out instead of pretending:

<!-- golden: job_people_release.txtar exit-notes.out -->

```console
$ magus job exit release-1.3/notes --stdin <<'EOF'
{"schema_version": 2, "changed_paths": [], "unresolved_risks": ["the notes need the final benchmark numbers"]}
EOF
exited release-1.3/notes, recorded exited, with its result filed. Whoever forked it verifies with `magus job wait release-1.3/notes`
```

The board says both items came back, and nothing more:

<!-- golden: job_people_release.txtar ls-exited.out -->

```console
$ magus ls jobs
JOB                    HOLDER   STATE   MODEL  PATHS  PROOF  CHECK
release-1.3/changelog  session  exited  -      1      alone  -
release-1.3/notes      session  exited  -      1      alone  -

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

Coming back is not being done. The person cutting the release checks each item:

<!-- golden: job_people_release.txtar wait-changelog.out -->

```console
$ magus job wait release-1.3/changelog
verified release-1.3/changelog, recorded pass
goal changelog: verified
footprint: where its diff since the checkpoint landed
  CHANGELOG.md:3-6
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

<!-- golden: job_people_release.txtar wait-notes.out -->

```console
$ magus job wait release-1.3/notes
rejected release-1.3/notes, and its state is unchanged
  job release-1.3/notes is not read-only and the result claims no changed paths at all
  nothing in the diff since 5f6b136f2721af3fb8a117fbf824b24a3a78b059 is inside its write paths (notes/1.3.md)
  goal "notes": nothing matching "notes/1.3.md" is in the tree, so this goal is unmet
goal notes: rejected
footprint: no line changed since the checkpoint
unresolved risks its holder reported
  the notes need the final benchmark numbers
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

Exit 1, every unmet rule named, and the risk repeated where the person deciding will
read it. The board is the checklist:

<!-- golden: job_people_release.txtar ls-board.out -->

```console
$ magus ls jobs
JOB                    HOLDER   STATE   MODEL  PATHS  PROOF  CHECK
release-1.3/changelog  session  pass    -      1      alone  -
release-1.3/notes      session  exited  -      1      alone  -

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

The release waits until every row reads `pass`. An item nobody will finish ends with
`magus job exit <job>`, recorded `no_return`, so the board never shows a skipped item
as a finished one.
