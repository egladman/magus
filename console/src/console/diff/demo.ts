// demo.ts - the Diff surface's daemon-free showcase (the shared #demo fragment).
//
// It supplies the two things the daemon would have supplied - a parsed changeset and an
// annotated session - and nothing else changes: order(), buildRows(), the ranking, the
// generated fold, the rail and the comment stream are all the production paths. So what a
// reader meets at /console/diff/#demo is the real surface with fabricated input, not a
// screenshot of one.
//
// The changeset is the SAME story every other showcase tells (demo-scenario.ts): the
// uncommitted `feat/authkit-claims` branch of the fictional acme monorepo, where one shared
// library's claims type grew an audience and took down a Go token verifier and a TypeScript
// web client with it. The trail's reindex-after-checkout beat, the failing
// services/identity:test run, and the apps/dashboard typecheck diagnostics at
// src/api/session.ts:42 are all THIS diff, seen from the other side - so a reader who opens
// two surfaces finds the same names, files and reasons in both. The churn and reach numbers
// below are the same figures scenarioInsight reports for those paths, and demo.test.ts asserts
// that rather than trusting this sentence - a reader who compares the Insight surface with this
// one and finds different numbers has caught the showcase lying.
//
// The fixture is a plain-data module by design (no DOM, no fetch, no protobuf), so
// demo.test.ts can assert the patch and the annotations agree without mounting anything.

import type { DiffSession, ReviewInfo, SessionOp } from "./session";

// The patch is an array of lines rather than one template literal because Go struct tags are
// backtick-quoted: a template literal would need every one of them escaped, and an escaped
// patch is a patch nobody can check against the tool that would have produced it.

// The role hints are magus's OWN sentences, verbatim from describe file (types.FileEntry.Hint),
// because that is where a live session gets them - a paraphrase here would be a second wording
// of a rule the workspace already states one way.
const HINT_SOURCE =
  "declared source: edits invalidate the owning project's cache keys and pull it into the affected set";
const HINT_OUTPUT =
  "generated: never hand-edit or read its diff; change the source of truth, regenerate (magus run generate), and commit it with the source change";
const HINT_UNCLAIMED =
  "no project declares this path: it invalidates no cache key, but directory containment still seeds its owning project into the affected set, so touching it reruns work; declare it, or ignore it deliberately";

// The agent that wrote most of this branch, and the transcript the story rows point at. magus
// never opens the transcript - the path is shown so the reader can open their own host's log.
const AGENT = {
  host: "claude-code",
  session: "sess-7f21",
  transcript: "~/.claude/projects/acme/7f21.jsonl",
} as const;

// demoSession returns the annotated changeset and the paired-review state.
//
// Files are listed in READING ORDER, which is what the surface honors: the shared library's
// contract change first because it is what everything else here is a consequence of, then the
// two consumers it broke, then the new and deleted files, then the test and the doc. The
// generated three sort out of the reading order entirely by role.
export function demoSession(): DiffSession {
  return {
    id: "rev-3f1c8a2e",
    base: "working",
    cursor: { path: "libs/authkit/claims.go", hunk: 0 },
    viewed: [],
    diff: {
      base: "working",
      seed_projects: ["libs/authkit", "services/identity", "apps/dashboard", "docs"],
      affected_projects: [
        { path: "libs/authkit", seed: true },
        { path: "services/identity", seed: true },
        { path: "apps/dashboard", seed: true },
        { path: "docs", seed: true },
        { path: "libs/protocol", seed: false },
        { path: "libs/ui", seed: false },
        { path: "services/gateway", seed: false },
        { path: "services/ledger", seed: false },
        { path: "services/catalog", seed: false },
        { path: "apps/admin", seed: false },
        { path: "tools/migrate", seed: false },
      ],
      notes: [
        "history lens walked the last 214 commits: libs/authkit/audience.go appears in none of them, so it carries no churn and no hotspot rank",
        "no coverage measured for docs (the project declares no coverage-producing target)",
      ],
      files: [
        {
          path: "libs/authkit/claims.go",
          project: "libs/authkit",
          role: "source",
          hint: HINT_SOURCE,
          surface: "public",
          reach: 38,
          symbols: [
            {
              id: "github.com/acme/acme/libs/authkit.Claims",
              label: "Claims",
              ref_count: 214,
              file_count: 38,
              external_projects: ["services/identity", "apps/dashboard", "services/gateway"],
              external_file_count: 31,
              module_api: true,
            },
            {
              id: "github.com/acme/acme/libs/authkit.Claims.Valid",
              label: "Claims.Valid",
              ref_count: 41,
              file_count: 12,
              external_projects: ["services/identity", "services/gateway"],
              external_file_count: 9,
              module_api: true,
            },
          ],
          coverage: { ratio: 0.71, covered_stmts: 129, total_stmts: 182 },
          churn: { commits: 46, authors: 2, score: 14260, rank: 1, project_trend: 16 },
          touches: [
            {
              ...AGENT,
              read: [
                "services/identity/internal/token/verify.go",
                "apps/dashboard/src/api/session.ts",
                "libs/authkit/keyset.go",
              ],
              ran: ["services/identity:test"],
            },
          ],
        },
        {
          path: "services/identity/internal/token/verify.go",
          project: "services/identity",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 22,
          symbols: [
            {
              id: "github.com/acme/acme/services/identity/internal/token.Verifier.Verify",
              label: "Verifier.Verify",
              ref_count: 63,
              file_count: 22,
              external_file_count: 0,
            },
          ],
          coverage: { ratio: 0.84, covered_stmts: 88, total_stmts: 105 },
          churn: { commits: 31, authors: 3, score: 6355, rank: 3, project_trend: 11 },
        },
        {
          path: "apps/dashboard/src/api/session.ts",
          project: "apps/dashboard",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 14,
          symbols: [
            {
              id: "apps/dashboard/src/api/session.ts#canReach",
              label: "canReach",
              ref_count: 27,
              file_count: 14,
              external_file_count: 0,
            },
          ],
          // Measured zero, not unmeasured: the typecheck target runs here, the test target does
          // not, so this is the one file in the changeset the surface can honestly call untested.
          coverage: { ratio: 0, covered_stmts: 0, total_stmts: 96 },
          churn: { commits: 19, authors: 2, score: 7638, rank: 2, project_trend: -5 },
          touches: [
            {
              ...AGENT,
              read: ["libs/authkit/claims.go", "apps/dashboard/src/gen/session_pb.ts"],
              ran: ["apps/dashboard:typecheck"],
            },
          ],
        },
        {
          path: "libs/authkit/audience.go",
          project: "libs/authkit",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 0,
        },
        {
          // The third consumer the reach numbers above have always named and the changeset
          // never contained: Claims lists services/gateway among its external projects, so a
          // contract change that left the gateway untouched was a blast radius contradicting
          // its own annotation.
          path: "services/gateway/internal/mint/token.go",
          project: "services/gateway",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 9,
          churn: { commits: 12, authors: 3, score: 1284 },
          touches: [{ host: AGENT.host, session: AGENT.session }],
        },
        {
          path: "services/gateway/internal/mint/token_test.go",
          project: "services/gateway",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 0,
        },
        {
          // A BINARY file: no hunks, and the surface has to say so rather than render an empty
          // diff, which reads as "nothing changed". A golden fixture regenerated because the
          // type it captures moved is the most ordinary way one appears in a review.
          path: "libs/authkit/testdata/claims.golden",
          project: "libs/authkit",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 0,
          churn: { commits: 4, authors: 2, score: 96 },
        },
        {
          // A MODE change and nothing else: no hunks either, for a different reason. A script
          // becoming executable is a real reviewable event that renders as an empty entry
          // unless the surface reads the mode.
          path: "tools/migrate/backfill.sh",
          project: "tools/migrate",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 0,
          churn: { commits: 2, authors: 1, score: 18 },
        },
        {
          path: "services/identity/internal/token/legacy_audience.go",
          project: "services/identity",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 0,
          churn: { commits: 7, authors: 2, score: 210 },
        },
        {
          path: "services/identity/internal/token/verify_test.go",
          project: "services/identity",
          role: "source",
          hint: HINT_SOURCE,
          surface: "internal",
          reach: 3,
          churn: { commits: 24, authors: 2, score: 2304, rank: 4 },
          // No reads recorded, so the story row stops at the author rather than inventing a
          // reason - the case worth having in the showcase.
          touches: [{ host: AGENT.host, session: AGENT.session }],
        },
        {
          path: "docs/auth/tokens.md",
          project: "docs",
          role: "unclaimed",
          hint: HINT_UNCLAIMED,
          surface: "unknown",
          // Prose defines no indexed symbol, so reach is unmeasured rather than zero.
          reach: null,
          churn: { commits: 3, authors: 1, score: 96 },
        },
        {
          path: "libs/protocol/gen/token_pb.go",
          project: "libs/protocol",
          role: "output",
          hint: HINT_OUTPUT,
          surface: "unknown",
          reach: 0,
        },
        {
          path: "apps/dashboard/src/gen/session_pb.ts",
          project: "apps/dashboard",
          role: "output",
          hint: HINT_OUTPUT,
          surface: "unknown",
          reach: 0,
        },
        {
          path: "docs/gen/auth/tokens.html",
          project: "docs",
          role: "output",
          hint: HINT_OUTPUT,
          surface: "unknown",
          reach: 0,
        },
      ],
    },
    comments: [
      {
        id: "cm1",
        path: "libs/authkit/claims.go",
        hunk: 0,
        // The new-side line the remark hangs on, which is what a host anchors an inline
        // comment to. Inside hunk 0's range (+14,14), so the batch can place it.
        line: 22,
        author: "human",
        body: "Audience is repeated on the wire. Does a token minted for two services verify at both, or is the first one authoritative?",
        resolved: false,
      },
      {
        id: "cm2",
        path: "libs/authkit/claims.go",
        hunk: 1,
        author: "agent",
        agent_name: "claude-code",
        body: "Valid now rejects an empty audience, so every mint path has to set one. tools/migrate still mints scope-only tokens.",
        resolved: false,
      },
      {
        id: "cm3",
        path: "apps/dashboard/src/api/session.ts",
        hunk: 0,
        line: 41,
        author: "human",
        body: "canReach was the only reader of scope, so this is the whole web-side change.",
        resolved: true,
        // Already sent, so the showcase has both states side by side: what the reader still
        // holds and what the world has seen. A surface where those look the same is one where
        // pressing send is a guess.
        published: true,
      },
    ],
    suggestions: [
      {
        id: "sg1",
        path: "services/identity/internal/token/legacy_audience.go",
        hunk: -1,
        agent_name: "claude-code",
        reason:
          "this delete drops the last caller of authkit.LegacyAudience, which is now unreferenced",
        accepted: false,
        declined: false,
      },
      {
        id: "sg2",
        path: "docs/auth/tokens.md",
        hunk: 0,
        agent_name: "claude-code",
        reason: "the rename leaves docs/auth/jwt.md linked from docs/index.md",
        accepted: false,
        declined: false,
      },
    ],
  };
}

// demoReview is the pull request the acme branch has open, and what has already been said on
// it. Same story as everything else in this file: the claims contract grew an audience, and
// the people who consume it are asking about it in public.
//
// The four threads cover every way a remark can land, which is the point of having four. Two
// sit on hunks the reader can see. One is on a file in this changeset but a line outside its
// hunks, because the code moved after the remark was written. One is on a file this changeset
// does not touch at all, because a review covers commits a working diff does not.
//
// The last two are why placement is not just a lookup: a surface that dropped them would be
// telling the reader a colleague said nothing, which is the worst thing a review can say.
export function demoReview(): ReviewInfo {
  return {
    id: "482",
    repo: "acme/acme",
    host: "github.com",
    threads: [
      {
        id: "th1",
        path: "libs/authkit/claims.go",
        line: 24,
        // The placement the daemon would have computed, stated here because the showcase has no
        // daemon - the same reason the fixture carries hunk digests rather than hashing them.
        // Hunk 0 of claims.go covers new-side lines 14 to 27.
        hunk: 0,
        author: "priya",
        body: "Audience as a slice means a token can verify at two services. Was that the intent, or should it be one?",
      },
      {
        id: "th2",
        path: "services/identity/internal/token/verify.go",
        line: 34,
        hunk: 0,
        author: "marcus",
        body: "Membership is right, but this is the third hand-rolled audience check. Worth a helper on Claims before a fourth.",
      },
      {
        id: "th4",
        path: "services/gateway/internal/mint/token.go",
        line: 204,
        // In the changeset, but line 204 is outside its only hunk (new-side 52 to 61): the code
        // moved after the remark was written, so it renders under the file heading.
        hunk: -1,
        author: "marcus",
        body: "Whatever we settle on here, the minter's docstring still describes the single-audience shape.",
      },
      {
        id: "th3",
        path: "services/gateway/internal/proxy/auth.go",
        line: 88,
        // On a file this changeset does not touch at all, so there is nothing to place it
        // against and the surface lists it instead.
        hunk: -1,
        author: "priya",
        body: "Gateway mints scope-only tokens on the health path. It will start failing Valid the moment this lands.",
      },
    ],
  };
}

// applyDemoPublish is the showcase's send: the drafts stop being drafts.
//
// It marks rather than removes, because that is what publishing does - the remark is still
// yours and still beside the code, it has simply also left. A showcase that deleted them would
// teach the reader that sending loses their work.
export function applyDemoPublish(session: DiffSession): DiffSession {
  return {
    ...session,
    comments: (session.comments ?? []).map((c) =>
      c.author === "human" ? { ...c, published: true } : c,
    ),
  };
}

// applyDemoReply is the showcase's reply: the answer joins the thread it answers.
//
// Appended directly after the thread rather than at the end, because that is where a reply
// belongs in a conversation, and a showcase that piled every answer at the bottom would teach
// the reader a shape the real surface does not have.
export function applyDemoReply(
  review: ReviewInfo | null,
  thread: string,
  body: string,
): ReviewInfo | null {
  if (!review) return review;
  const at = review.threads.findIndex((t) => t.id === thread);
  if (at < 0) return review;
  const answered = review.threads[at];
  if (!answered) return review;
  const threads = [...review.threads];
  threads.splice(at + 1, 0, {
    id: `${thread}-r`,
    path: answered.path,
    line: answered.line,
    // A reply lands where the thread it answers landed, which is what keeps it beside the same
    // code rather than jumping to the file heading.
    hunk: answered.hunk,
    author: "you",
    body,
  });
  return { ...review, threads };
}

// applyDemoOp is the showcase's stand-in for the daemon's session store: the reader's writes
// land in memory instead of over HTTP.
//
// It exists so the showcase is the surface rather than a picture of it - marking a hunk read,
// posting a comment, resolving one and skipping a suggestion all have to WORK, or the reader
// meets a rail whose buttons do nothing and concludes the feature is broken. Pure, so
// demo.test.ts can pin each op without a DOM.
export function applyDemoOp(session: DiffSession, op: SessionOp): DiffSession {
  switch (op.op) {
    case "cursor":
      return { ...session, cursor: { path: op.path, hunk: op.hunk } };
    case "seen": {
      // The showcase has no watermark to keep - demoReview() rebuilds the conversation on every
      // load - so this records nothing. It is handled rather than defaulted so the switch stays
      // exhaustive and a future op cannot fall through it silently.
      return session;
    }
    case "viewed": {
      const viewed = new Set(session.viewed ?? []);
      if (op.on) viewed.add(op.digest);
      else viewed.delete(op.digest);
      return { ...session, viewed: [...viewed] };
    }
    case "comment": {
      const comments = session.comments ?? [];
      return {
        ...session,
        // Author is "human" by construction, the same way the daemon stamps it from the
        // transport: this op arrived from the console, and nothing in the payload can say
        // otherwise.
        comments: [
          ...comments,
          {
            id: `cm${comments.length + 1}`,
            path: op.path,
            hunk: op.hunk,
            line: op.line,
            author: "human",
            body: op.body,
            resolved: false,
          },
        ],
      };
    }
    case "discard":
      // Only an unsent remark of the reader's own leaves, matching the store: a published one
      // cannot be unsaid by deleting the local copy, and an agent's is not theirs to remove.
      return {
        ...session,
        comments: (session.comments ?? []).filter(
          (c) => !(c.id === op.id && c.author === "human" && !c.published),
        ),
      };
    case "resolve":
      return {
        ...session,
        comments: (session.comments ?? []).map((c) =>
          c.id === op.id ? { ...c, resolved: op.on } : c,
        ),
      };
    case "answer":
      return {
        ...session,
        suggestions: (session.suggestions ?? []).map((s) =>
          s.id === op.id ? { ...s, accepted: op.on, declined: !op.on } : s,
        ),
      };
  }
}
