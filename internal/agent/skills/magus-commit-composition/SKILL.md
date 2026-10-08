# Composing unpushed commits into reviewable chunks

A reviewer reads commits, not diffs.{{if .Full}} Work committed in the order it occurred to
you arrives as a log nobody can review: a rename spread over four commits, a fix
buried in a regeneration, notes-to-self between two real changes.{{end}} This skill decides
what belongs in each commit, so every commit is one idea a reviewer can accept or
reject alone. The workspace models its own boundaries: use them, not directory
names.

magus does not rewrite history for you{{if .Full}}, the same way it reports what a
change affects without editing it{{end}}. It shows the seams and proves afterwards that
nothing was lost; your VCS performs the edit.

## Two constraints that decide most groupings

**Only unpushed work is eligible.** Published commits are fixed. Establish what is
unpushed before planning{{if .Full}}; a branch with no upstream has
published nothing{{end}}.

**Generated output belongs with the source that moved it.** Split them and the
source commit fails its own drift gate.

```sh
magus describe file <changed-path>...
```

Every `output` path joins the `source` change that invalidated it. A commit that is
all regeneration missed that pairing: fold it into the change that caused it.

## Ask the workspace where the seams are

```sh
magus ls                             # the projects: the coarsest real boundary
magus describe project <path>        # its declared sources, outputs, depends_on
magus affected --impact              # what a candidate group actually reaches
magus refs <symbol> --occurrences    # every site of a rename, uncapped
```

Three signals, strongest first:

- **Project ownership.** Changes in projects with no dependency edge between them
  are separate commits. Read the edges from `magus describe project`{{if .Full}},
  which frequently disagrees with what the directory layout suggests{{end}}.
- **Blast radius.** Groups that reach disjoint project sets are separable; groups
  that reach the same set usually want one commit.
- **Symbol coupling.** A rename's sites belong together, however many directories
  they span{{if .Full}}. If refs reports a project not-indexed, run `magus graph
  build` first: `unknown, not absent` is not an empty result{{end}}.

## Where this stops

**Two changes inside one file cannot be separated by path.** A file carrying a
rename and a behavior fix needs hunk-level work, or they ship together. Recognize it
while planning{{if .Full}}; discovering it
mid-restructure turns a cleanup into a recovery{{end}}.

Prefer the cheapest operation that makes the branch reviewable. In rising order of
risk:

1. Reword a message.
2. Fold a regeneration into its neighbor.
3. Drop a commit whose content the final tree does not keep.
4. Reorder independent commits.
5. Split one commit into several.{{if .Full}}

Most branches need only the first three.{{end}}

## Before you start, and after you finish

Record the identity of the state you are about to rewrite:

```sh
magus vcs checkpoint          # revision, branch, dirty flag, patch digest; writes nothing
```

Restructure with your VCS. When it stops on a conflicted generated file, settle it
with magus, not by hand:

```sh
magus vcs resolve             # settles the conflicted declared outputs, regenerates once
```

**Prove the content survived.** A restructure changes history and nothing else, and
a lost commit still leaves a tree that builds{{if .Full}}, which is why
a green suite is not evidence here{{end}}:

```sh
magus graph diff --rev <checkpoint-revision>
```

Everything it reports must be a change you intended. Missing nodes you did not
remove mean the restructure dropped work: return to the recorded revision and start
again, never reconcile by hand.

Finish by staging through the workspace's declarations and re-running the gate:

```sh
magus vcs add
magus affected ci
```

## What does not belong in a commit at all

Session notes and scratch plans are not repository content unless the repository
already tracks them{{if .Full}}; check the path's history on the base branch
before assuming either way{{end}}. Untracked session state belongs in your harness's
memory, not the branch{{if .Full}}, and dropping those commits is
often the single largest reduction available{{end}}.

## See also

- {{skill "vcs-hygiene"}} covers classifying paths and staging one commit safely.
