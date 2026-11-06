---
title: PullsService
generated_from: reference/api/
description: PullsService serves the PR board and the author filter that scopes it.
tags: [api, proto, connect, grpc, pullsservice]
---

# PullsService

PullsService serves the PR board and the author filter that scopes it.

Package `magus.pulls.v1alpha1`, defined in `proto/magus/pulls/v1alpha1/pulls.proto`. Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto). Part of the [daemon API](../../index.md).

## Methods

### GetPulls

GetPulls returns the last board the poller produced. It never calls the forge.

`POST /magus.pulls.v1alpha1.PullsService/GetPulls`: unary. Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

Takes [GetPullsRequest](#getpullsrequest), returns [GetPullsResponse](#getpullsresponse).

### SetAuthors

SetAuthors replaces the author filter and triggers one sync now, respecting the poll floor.

`POST /magus.pulls.v1alpha1.PullsService/SetAuthors`: unary. Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

Takes [SetAuthorsRequest](#setauthorsrequest), returns [SetAuthorsResponse](#setauthorsresponse).

### CheckoutPull

CheckoutPull makes an isolated local checkout of a pull's head commit by running `magus vcs checkout`, and reports where it is and how to remove it.

`POST /magus.pulls.v1alpha1.PullsService/CheckoutPull`: unary. Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

Takes [CheckoutPullRequest](#checkoutpullrequest), returns [CheckoutPullResponse](#checkoutpullresponse).

## Messages

### AuthorFilter

AuthorFilter says whose pull requests the board shows.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field          | Type            | # | Description                                                                                   |
| -------------- | --------------- | - | --------------------------------------------------------------------------------------------- |
| `authors`      | repeated string | 1 |                                                                                               |
| `is_default`   | bool            | 2 | is\_default is true when no filter was ever saved, so the board shows the viewer's own pulls. |
| `viewer`       | string          | 3 | viewer is the login the forge credential belongs to.                                          |
| `every_author` | bool            | 4 | every\_author is true when the filter is saved as empty, meaning every author.                |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### Board

Board is everything the console renders for one repository.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field         | Type                             | # | Description                                                                     |
| ------------- | -------------------------------- | - | ------------------------------------------------------------------------------- |
| `repo`        | string                           | 1 | repo is the owner/name the board describes.                                     |
| `filter`      | [AuthorFilter](#authorfilter)    | 2 |                                                                                 |
| `sync`        | [SyncState](#syncstate)          | 3 |                                                                                 |
| `main`        | [BranchState](#branchstate)      | 4 | main is the base branch, its CI pinned above the rows.                          |
| `pulls`       | [repeated Pull](#pull)           | 5 |                                                                                 |
| `patterns`    | [repeated Pattern](#pattern)     | 6 |                                                                                 |
| `merge_order` | [repeated MergeStep](#mergestep) | 7 | merge\_order is the suggested order to merge the open pulls, first entry first. |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### BranchState

BranchState is a branch and the CI on its head commit.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field           | Type                    | # | Description                                                           |
| --------------- | ----------------------- | - | --------------------------------------------------------------------- |
| `name`          | string                  | 1 |                                                                       |
| `sha`           | string                  | 2 |                                                                       |
| `url`           | string                  | 3 |                                                                       |
| `ci`            | [CiState](#cistate)     | 4 |                                                                       |
| `first_failure` | [FailedJob](#failedjob) | 5 | first\_failure is the first failing check, unset when nothing failed. |
| `passed`        | int32                   | 6 |                                                                       |
| `failed`        | int32                   | 7 |                                                                       |
| `pending`       | int32                   | 8 | pending counts checks queued or running.                              |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### CheckoutPullRequest

CheckoutPullRequest names the pull to check out.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field    | Type  | # | Description                                             |
| -------- | ----- | - | ------------------------------------------------------- |
| `number` | int32 | 1 | number is the pull request number on the current board. |

Used by: [CheckoutPull (request)](pulls.md#checkoutpull).

### CheckoutPullResponse

CheckoutPullResponse is the record `magus vcs checkout -o json` printed.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field    | Type            | # | Description                                                                      |
| -------- | --------------- | - | -------------------------------------------------------------------------------- |
| `path`   | string          | 1 | path is the directory of the new checkout.                                       |
| `remove` | string          | 2 | remove is the command that removes the checkout.                                 |
| `log`    | repeated string | 3 | log is what the checkout did, one line per step, including the bootstrap it ran. |
| `error`  | string          | 4 | error is the failure text, empty on success.                                     |

Used by: [CheckoutPull (response)](pulls.md#checkoutpull).

### FailedJob

FailedJob is one failing check with the first error lines the forge reported for it.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field        | Type            | # | Description                                                                        |
| ------------ | --------------- | - | ---------------------------------------------------------------------------------- |
| `name`       | string          | 1 |                                                                                    |
| `url`        | string          | 2 |                                                                                    |
| `lines`      | repeated string | 3 | lines are the first error lines, at most five.                                     |
| `output_ref` | string          | 4 | output\_ref is a magus output reference found in those lines, empty when none was. |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### GetPullsRequest

GetPullsRequest asks for the current board.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

No fields.

Used by: [GetPulls (request)](pulls.md#getpulls).

### GetPullsResponse

GetPullsResponse carries the last board the poller produced.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field   | Type            | # | Description                                        |
| ------- | --------------- | - | -------------------------------------------------- |
| `board` | [Board](#board) | 1 | board is the last good board, with its sync state. |

Used by: [GetPulls (response)](pulls.md#getpulls).

### MergeStep

MergeStep is one pull in the suggested merge order.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field    | Type   | # | Description                                                                                      |
| -------- | ------ | - | ------------------------------------------------------------------------------------------------ |
| `number` | int32  | 1 |                                                                                                  |
| `note`   | string | 2 | note is one sentence on why the pull sits where it does, for example waits on a main regression. |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### Overlap

Overlap is another open pull request that changes files this one also changes.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field    | Type            | # | Description |
| -------- | --------------- | - | ----------- |
| `number` | int32           | 1 |             |
| `files`  | repeated string | 2 |             |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### Pattern

Pattern is one cross-PR finding: a regression, or a runner backlog.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field           | Type                        | # | Description                                                                                |
| --------------- | --------------------------- | - | ------------------------------------------------------------------------------------------ |
| `kind`          | [PatternKind](#patternkind) | 1 |                                                                                            |
| `check`         | string                      | 2 | check is the check the pattern is about, empty for a runner backlog.                       |
| `first_line`    | string                      | 3 | first\_line is the error line the failing pulls share.                                     |
| `pulls`         | repeated int32              | 4 | pulls are the pull numbers the pattern covers.                                             |
| `since_sha`     | string                      | 5 | since\_sha is the newest base commit at or before the earliest failure, when a regression. |
| `since_subject` | string                      | 6 |                                                                                            |
| `since_pull`    | int32                       | 7 |                                                                                            |
| `on_main`       | bool                        | 8 | on\_main is true when the same check also fails on the base branch.                        |
| `message`       | string                      | 9 | message is the one sentence the board shows for the pattern.                               |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### Pull

Pull is one open pull request row on the board.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field           | Type                             | #  | Description                                                                                 |
| --------------- | -------------------------------- | -- | ------------------------------------------------------------------------------------------- |
| `number`        | int32                            | 1  |                                                                                             |
| `title`         | string                           | 2  |                                                                                             |
| `author`        | string                           | 3  |                                                                                             |
| `url`           | string                           | 4  |                                                                                             |
| `branch`        | string                           | 5  |                                                                                             |
| `head_sha`      | string                           | 6  |                                                                                             |
| `behind_by`     | int32                            | 7  | behind\_by is how many commits the base has that the head does not. -1 when unknown.        |
| `mergeable`     | [Mergeable](#mergeable)          | 8  |                                                                                             |
| `ci`            | [CiState](#cistate)              | 9  |                                                                                             |
| `queued_for`    | Duration                         | 10 | queued\_for is how long the earliest queued check has waited. Unset when nothing is queued. |
| `passed`        | int32                            | 11 |                                                                                             |
| `failed`        | int32                            | 12 |                                                                                             |
| `pending`       | int32                            | 13 | pending counts checks queued or running.                                                    |
| `first_failure` | [FailedJob](#failedjob)          | 14 | first\_failure is the first failing check, unset when nothing failed.                       |
| `review`        | [Review](#review)                | 15 |                                                                                             |
| `blocked`       | repeated string                  | 16 | blocked is one plain sentence per reason the pull cannot merge now.                         |
| `queue`         | [QueueStatus](#queuestatus)      | 17 | queue is the merge queue's status for this pull, shown as its own column.                   |
| `pushed_at`     | Timestamp                        | 18 |                                                                                             |
| `stale`         | bool                             | 19 | stale is true when nothing was pushed for longer than the configured stale window.          |
| `draft`         | bool                             | 20 |                                                                                             |
| `overlaps`      | [repeated Overlap](#overlap)     | 21 | overlaps are the other open pulls that change files this pull also changes.                 |
| `failed_jobs`   | [repeated FailedJob](#failedjob) | 22 | failed\_jobs are every failing check, expanded in place by the console.                     |
| `fetch_ref`     | string                           | 23 | fetch\_ref is the ref a clone fetches to get head\_sha, for example refs/pull/12/head.      |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### QueueStatus

QueueStatus is the merge queue's view of one pull request.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field         | Type   | # | Description                                                                       |
| ------------- | ------ | - | --------------------------------------------------------------------------------- |
| `state`       | string | 1 | state is the queue's word for where the pull sits, for example queued or merging. |
| `description` | string | 2 | description is one sentence the queue wrote about the state.                      |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### SetAuthorsRequest

SetAuthorsRequest replaces the author filter.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field     | Type            | # | Description                                                       |
| --------- | --------------- | - | ----------------------------------------------------------------- |
| `authors` | repeated string | 1 | authors are the logins to show. An empty list means every author. |

Used by: [SetAuthors (request)](pulls.md#setauthors).

### SetAuthorsResponse

SetAuthorsResponse carries the board after the filter change.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field   | Type            | # | Description                                                                               |
| ------- | --------------- | - | ----------------------------------------------------------------------------------------- |
| `board` | [Board](#board) | 1 | board is the board as it stands after the filter change, before the triggered sync lands. |

Used by: [SetAuthors (response)](pulls.md#setauthors).

### SyncState

SyncState is when the board was taken and why the poller is waiting as long as it is.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Field            | Type      | #  | Description                                                                                       |
| ---------------- | --------- | -- | ------------------------------------------------------------------------------------------------- |
| `synced_at`      | Timestamp | 1  |                                                                                                   |
| `next_sync_at`   | Timestamp | 2  |                                                                                                   |
| `failed_at`      | Timestamp | 3  | failed\_at is when the last sync failed. Unset when the last sync succeeded.                      |
| `interval`       | Duration  | 4  | interval is the wait chosen before the next sync.                                                 |
| `cadence_reason` | string    | 5  | cadence\_reason is one sentence saying why the interval is what it is, shown on the board.        |
| `sync_error`     | string    | 6  | sync\_error is the last sync's failure, shown on the board while the last good board stands.      |
| `rate_limit`     | int32     | 7  | rate\_limit is the forge's request budget for the window.                                         |
| `rate_remaining` | int32     | 8  | rate\_remaining is how many requests remain in the window.                                        |
| `rate_reset`     | Timestamp | 9  |                                                                                                   |
| `requests`       | int32     | 10 | requests is how many forge requests the last sync sent.                                           |
| `revalidated`    | int32     | 11 | revalidated is how many of those answered 304, which the forge does not count against the budget. |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

## Enums

### CiState

CiState is the rolled-up result of a commit's checks, with the queue context already removed.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Value                  | # | Description                                                                     |
| ---------------------- | - | ------------------------------------------------------------------------------- |
| `CI_STATE_UNSPECIFIED` | 0 |                                                                                 |
| `CI_STATE_GREEN`       | 1 | GREEN means every check passed or was skipped.                                  |
| `CI_STATE_RED`         | 2 | RED means at least one check failed.                                            |
| `CI_STATE_RUNNING`     | 3 | RUNNING means at least one check is running and none failed.                    |
| `CI_STATE_QUEUED`      | 4 | QUEUED means at least one check is waiting for a runner and none failed or ran. |
| `CI_STATE_NONE`        | 5 | NONE means the commit has no checks at all.                                     |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### Mergeable

Mergeable is the forge's answer on whether the head merges into the base cleanly.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Value                   | # | Description                                                                        |
| ----------------------- | - | ---------------------------------------------------------------------------------- |
| `MERGEABLE_UNSPECIFIED` | 0 |                                                                                    |
| `MERGEABLE_MERGEABLE`   | 1 |                                                                                    |
| `MERGEABLE_CONFLICTING` | 2 | CONFLICTING means the head cannot merge into the base without resolving conflicts. |
| `MERGEABLE_UNKNOWN`     | 3 | UNKNOWN means the forge has not computed the answer yet.                           |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### PatternKind

PatternKind is the closed set of cross-PR patterns the board reports.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Value                         | # | Description                                                                                           |
| ----------------------------- | - | ----------------------------------------------------------------------------------------------------- |
| `PATTERN_KIND_UNSPECIFIED`    | 0 |                                                                                                       |
| `PATTERN_KIND_REGRESSION`     | 1 | REGRESSION means several PRs fail the same check with the same first line.                            |
| `PATTERN_KIND_RUNNER_BACKLOG` | 2 | RUNNER\_BACKLOG means checks across PRs have waited on runners too long, which is capacity, not code. |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

### Review

Review is the state of a pull request's reviews.

Source: [pulls.proto](https://github.com/egladman/magus/blob/main/proto/magus/pulls/v1alpha1/pulls.proto).

| Value                      | # | Description                                                                |
| -------------------------- | - | -------------------------------------------------------------------------- |
| `REVIEW_UNSPECIFIED`       | 0 |                                                                            |
| `REVIEW_APPROVED`          | 1 |                                                                            |
| `REVIEW_CHANGES_REQUESTED` | 2 |                                                                            |
| `REVIEW_REQUIRED`          | 3 | REVIEW\_REQUIRED means reviewers were requested and none has reviewed yet. |
| `REVIEW_NONE`              | 4 |                                                                            |

Used by: [GetPulls (response)](pulls.md#getpulls), [SetAuthors (response)](pulls.md#setauthors).

