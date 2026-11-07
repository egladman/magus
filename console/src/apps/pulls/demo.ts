// demo.ts - an invented board for the Pulls app, shown under #demo.
// The repo and people are ACME's, like the other apps' demo data.

import { create } from "@bufbuild/protobuf";
import { DurationSchema, timestampFromMs } from "@bufbuild/protobuf/wkt";
import {
  AuthorFilterSchema,
  BoardSchema,
  BranchStateSchema,
  CiState,
  FailedJobSchema,
  MergeStepSchema,
  Mergeable,
  OverlapSchema,
  PatternKind,
  PatternSchema,
  PullSchema,
  QueueStatusSchema,
  Review,
  SyncStateSchema,
  type Board,
  type FailedJob,
} from "@wire/pulls/v1alpha1/pulls_pb";

const MIN = 60_000;
const HOUR = 60 * MIN;

const durationFromMs = (ms: number) =>
  create(DurationSchema, { seconds: BigInt(Math.floor(ms / 1000)) });

const lint = (n: number): FailedJob =>
  create(FailedJobSchema, {
    name: "lint console",
    url: "https://github.com/acme/acme/actions/runs/" + n,
    lines: ["src/session.ts:41 unused import", "src/session.ts:88 missing return type"],
    outputRef: "grd" + n.toString(16).padStart(8, "0"),
  });

export function demoBoard(now: number = Date.now()): Board {
  const main = create(BranchStateSchema, {
    name: "main",
    sha: "9f3c2e1a7b4d",
    url: "https://github.com/acme/acme/commit/9f3c2e1a7b4d",
    ci: CiState.GREEN,
    passed: 14,
  });
  const pulls = [
    create(PullSchema, {
      number: 640,
      title: "add the audience list to the token verifier",
      author: "egladman",
      url: "https://github.com/acme/acme/pull/640",
      headSha: "a1b2c3d4e5f6",
      fetchRef: "refs/pull/640/head",
      mergeable: Mergeable.MERGEABLE,
      ci: CiState.GREEN,
      passed: 14,
      review: Review.APPROVED,
      queue: create(QueueStatusSchema, { state: "success", description: "next to merge" }),
      pushedAt: timestampFromMs(now - 2 * HOUR),
      overlaps: [create(OverlapSchema, { number: 643, files: ["web/session.ts", "go/verify.go"] })],
    }),
    create(PullSchema, {
      number: 643,
      title: "read the audience on the web client",
      author: "egladman",
      url: "https://github.com/acme/acme/pull/643",
      headSha: "0f9e8d7c6b5a",
      fetchRef: "refs/pull/643/head",
      behindBy: 3,
      mergeable: Mergeable.MERGEABLE,
      ci: CiState.RED,
      passed: 9,
      failed: 1,
      pending: 2,
      firstFailure: lint(643),
      failedJobs: [lint(643)],
      review: Review.REQUIRED,
      blocked: ["lint console failing: src/session.ts:41 unused import", "review required"],
      pushedAt: timestampFromMs(now - 5 * 24 * HOUR),
      stale: true,
      overlaps: [create(OverlapSchema, { number: 640, files: ["web/session.ts", "go/verify.go"] })],
    }),
    create(PullSchema, {
      number: 645,
      title: "move the retry loop into the client",
      author: "egladman",
      url: "https://github.com/acme/acme/pull/645",
      headSha: "7a6b5c4d3e2f",
      fetchRef: "refs/pull/645/head",
      mergeable: Mergeable.CONFLICTING,
      ci: CiState.QUEUED,
      queuedFor: durationFromMs(2 * HOUR),
      pending: 6,
      review: Review.NONE,
      draft: true,
      blocked: ["conflicts with main", "waiting on runners for 2h", "draft"],
      pushedAt: timestampFromMs(now - 3 * HOUR),
    }),
  ];
  return create(BoardSchema, {
    repo: "acme/acme",
    filter: create(AuthorFilterSchema, {
      authors: ["egladman"],
      isDefault: true,
      viewer: "egladman",
    }),
    sync: create(SyncStateSchema, {
      syncedAt: timestampFromMs(now - 12_000),
      nextSyncAt: timestampFromMs(now + 48_000),
      interval: durationFromMs(60_000),
      cadenceReason: "checks running: every 1m",
      rateLimit: 5000,
      rateRemaining: 4210,
    }),
    main,
    pulls,
    patterns: [
      create(PatternSchema, {
        kind: PatternKind.RUNNER_BACKLOG,
        message: "3 runs queued over 15m: runner backlog, not code",
      }),
    ],
    mergeOrder: [
      create(MergeStepSchema, { number: 640, note: "green, mergeable, approved" }),
      create(MergeStepSchema, { number: 643, note: "waits on lint console" }),
      create(MergeStepSchema, { number: 645 }),
    ],
  });
}
