{{if .Full}}# Composing unpushed commits into reviewable chunks{{else}}# Commit composition{{end}}

{{if .Full}}A reviewer reads commits, not diffs. Work committed in the order it occurred to
you arrives as a log nobody can review: a rename spread over four commits, a fix
buried in a regeneration, notes-to-self between two real changes. This skill decides
what belongs in each commit, so every commit is one idea a reviewer can accept or
reject alone. The workspace models its own boundaries: use them, not directory
names.

magus does not rewrite history for you, the same way it reports what a
change affects without editing it. It shows the seams and proves afterwards that
nothing was lost; your VCS performs the edit.{{else}}Reviewer reads commits, not diffs.
Decide what belongs in each commit: every commit one idea, accepted or rejected alone.
Use workspace's own boundaries, not directory names.
magus does not rewrite history: shows seams, proves afterwards nothing lost; your VCS edits.{{end}}

{{if .Full}}## Two constraints that decide most groupings{{else}}## Two constraints{{end}}

{{if .Full}}**Only unpushed work is eligible.** Published commits are fixed. Establish what is
unpushed before planning; a branch with no upstream has
published nothing.{{else}}Only unpushed work eligible; published commits fixed.
Establish unpushed set before planning.{{end}}

{{if .Full}}**Generated output belongs with the source that moved it.** Split them and the
source commit fails its own drift gate.{{else}}Generated output belongs with source that moved it; split -> source commit fails drift gate.{{end}}

```sh
magus describe file <changed-path>...
```

{{if .Full}}Every `output` path joins the `source` change that invalidated it. A commit that is
all regeneration missed that pairing: fold it into the change that caused it.{{else}}Every `output` path joins `source` change that invalidated it.
Commit of all regeneration missed pairing -> fold into causing change.{{end}}

{{if .Full}}## Ask the workspace where the seams are{{else}}## Ask workspace for seams{{end}}

```sh
magus ls                             # the projects: the coarsest real boundary
magus describe project <path>        # its declared sources, outputs, depends_on
magus affected --impact              # what a candidate group actually reaches
magus refs <symbol> --occurrences    # every site of a rename, uncapped
```

Three signals, strongest first:
{{if .Full}}
- **Project ownership.** Changes in projects with no dependency edge between them
  are separate commits. Read the edges from `{{cmd "describe project"}}`,
  which frequently disagrees with what the directory layout suggests.{{else}}- Project ownership: projects with no dependency edge between them -> separate commits; edges from `{{cmd "describe project"}}`.{{end}}
{{if .Full}}- **Blast radius.** Groups that reach disjoint project sets are separable; groups
  that reach the same set usually want one commit.{{else}}- Blast radius: groups reaching disjoint project sets -> separable; same set -> usually one commit.{{end}}
{{if .Full}}- **Symbol coupling.** A rename's sites belong together, however many directories
  they span. If refs reports a project not-indexed, run
  `{{cmd "graph build"}}` first: `unknown, not absent` is not an empty result.{{else}}- Symbol coupling: rename's sites together, however many directories.{{end}}

{{if .Full}}## Where this stops{{else}}## Limits{{end}}

{{if .Full}}**Two changes inside one file cannot be separated by path.** A file carrying a
rename and a behavior fix needs hunk-level work, or they ship together. Recognize it
while planning; discovering it
mid-restructure turns a cleanup into a recovery.{{else}}Two changes inside one file cannot be separated by path.
File with rename and behavior fix -> hunk-level work, or ship together; recognize while planning.{{end}}

{{if .Full}}Prefer the cheapest operation that makes the branch reviewable. In rising order of
risk:
{{else}}Prefer cheapest operation making branch reviewable. Rising risk:{{end}}
{{if .Full}}1. Reword a message.{{else}}1. Reword message.{{end}}
{{if .Full}}2. Fold a regeneration into its neighbor.{{else}}2. Fold regeneration into neighbor.{{end}}
{{if .Full}}3. Drop a commit whose content the final tree does not keep.{{else}}3. Drop commit whose content final tree does not keep.{{end}}
4. Reorder independent commits.
{{if .Full}}5. Split one commit into several.{{else}}5. Split one commit into several.{{end}}{{if .Full}}

Most branches need only the first three.{{end}}

{{if .Full}}## Before you start, and after you finish{{else}}## Before start, after finish{{end}}

{{if .Full}}Record the identity of the state you are about to rewrite:{{else}}Record state identity before rewriting:{{end}}

```sh
magus vcs checkpoint          # revision, branch, dirty flag, patch digest; writes nothing
```

{{if .Full}}Restructure with your VCS. When it stops on a conflicted generated file, settle it
with magus, not by hand:{{else}}Restructure with your VCS.
When it stops on conflicted generated file -> settle with magus, not by hand:{{end}}

```sh
magus vcs resolve             # settles the conflicted declared outputs, regenerates once
```

{{if .Full}}**Prove the content survived.** A restructure changes history and nothing else, and
a lost commit still leaves a tree that builds, which is why
a green suite is not evidence here:{{else}}Prove content survived: restructure changes history and nothing else; lost commit still leaves building tree.{{end}}

```sh
magus graph diff --rev <checkpoint-revision>
```

{{if .Full}}Everything it reports must be a change you intended. Missing nodes you did not
remove mean the restructure dropped work: return to the recorded revision and start
again, never reconcile by hand.{{else}}Everything reported must be intended change.
Missing nodes you did not remove -> work dropped -> return to recorded revision, restart; never reconcile by hand.{{end}}

{{if .Full}}Finish by staging through the workspace's declarations and re-running the gate:{{else}}Finish: stage through workspace declarations, re-run gate:{{end}}

```sh
magus vcs add
magus affected ci
```

{{if .Full}}## What does not belong in a commit at all{{else}}## Not commit content at all{{end}}

{{if .Full}}Session notes and scratch plans are not repository content unless the repository
already tracks them; check the path's history on the base branch
before assuming either way. Untracked session state belongs in your harness's
memory, not the branch, and dropping those commits is
often the single largest reduction available.{{else}}Session notes, scratch plans: not repository content unless repository already tracks them.
Untracked session state: harness memory, not branch.{{end}}

## See also

{{if .Full}}- {{skill "vcs-hygiene"}} covers classifying paths and staging one commit safely.{{else}}- {{skill "vcs-hygiene"}}: classifying paths, staging one commit safely.{{end}}
