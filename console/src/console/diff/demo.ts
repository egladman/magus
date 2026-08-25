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

import type { DiffSession, SessionOp } from "./session";

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
        author: "human",
        body: "canReach was the only reader of scope, so this is the whole web-side change.",
        resolved: true,
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
            author: "human",
            body: op.body,
            resolved: false,
          },
        ],
      };
    }
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
