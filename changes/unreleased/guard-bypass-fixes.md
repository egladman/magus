### Security

- **A launcher the guard does not know is judged by what it launches.** `watch -n1 git
  stash`, `flock`, `ssh` and any unlisted program running a VCS command or a rewriter
  meet that command's rules; only programs known to treat operands as data are skipped.
- **A whole-tree exemption must prove its paths are narrower than the tree.** `git stash
  push -- .`, `git restore :/`, `git add *`, `jj restore .` and `hg revert .` are refused;
  a path the guard cannot read as narrower counts as the whole tree.
- **A git option is judged by what it sets.** An abbreviation (`git reset --har`,
  `--no-verif`) or a cluster (`git add -Av`) meets the full option's rule. `git checkout
  -f`, `git switch --discard-changes` and `git read-tree -u --reset` are refused as
  whole-tree; `git commit -a` and `git commit .` as staging everything.
- **Paths are judged where the kernel opens them.** A symlink before a `..` no longer
  hides a write into a denied path: `a/l/../x.go` is graded as the file it lands in.
- **Every in-place sed is refused, named files or not.** `-i.bak`, `-ni`, `--in-place=SUF`
  and abbreviated `--in-pl` deny with `-i`; the remedy names the editor tool and the Buzz
  `edit` module.
