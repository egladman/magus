### Fixed

- **The push gate reads the session store once and asks git once.** Judging a `git push`
  folded the store three times and started two git processes per passing `ci` record,
  about a hundred on a busy store, so the guard ran out of its 7s budget. One fold now
  answers the verdict, the replay and the gated ancestor, and one `git rev-list` reads
  every gated commit's tree and ancestry. The trail times the work as `push_coverage`.
