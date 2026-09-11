# The api summaries are out of step with the source

Several packages carry a `gen/api.md` file summarizing the names they export and the
names they import from other packages. These are produced by tooling that lives in
this repo, not written by hand.

Right now some of them are wrong and some are missing outright. Bring every one of
them back in step with the source, and leave the repo's own check over them passing.

Do not edit those files by hand, and do not change a source file to make a stale
summary look correct.
