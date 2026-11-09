### Fixed

- **The push gate reads the session store once and asks git once.** Judging a push folded the
  store three times and ran two git processes per passing `ci` record, about a hundred on a busy
  store, past the 7s budget.
- **The push gate really does answer from one fold and one git call.** One fold answers the
  verdict, the replay and the gated ancestor; one `git rev-list` reads every gated commit's tree
  and ancestry. The trail times the work as `push_coverage`.
