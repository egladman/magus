### Changed

- **A read-only job declaring no check or goal passes when its holder files a result.** `magus job exit --stdin` records it `pass`, with the end reason `returned`, and gives back its checkout; it used to wait for a `magus job wait` that refused it, then end `no_return`.
