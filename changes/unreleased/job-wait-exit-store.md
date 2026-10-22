### Changed

- **A read-only job declaring no check or goal passes when its holder files a result.** `magus job exit --stdin` records it `pass`, with the end reason `returned`, and gives back its checkout; it used to wait for a `magus job wait` that refused it, then end `no_return` as "returned ungraded". A read-only job with a script check is still graded by `magus job wait`, and one ended with no result is still `no_return`.
- **A wrong `-o template` field names the record's nested field paths.** The error lists `.job.state` and `.gates[].output_ref` beside the top-level keys, three levels deep and at most 40 paths, instead of the top-level keys alone.
- **The refusal of a leftover `jobs.json` names the command that clears it.** Once no older magus writes the one-document plan, `mv <dir>/jobs.json <dir>/jobs.json.old` sets it aside; the refusal prints that command with the store's own path.
