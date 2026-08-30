// demo.ts - the Plan surface's daemon-free showcase (the shared #demo fragment).
//
// It supplies the one thing the daemon would have supplied - a lease ledger - and nothing
// else changes: buildPlan(), treeOrder(), layoutPlan(), the overlap warnings, the staleness rule
// and the detail pane are all the production paths. So what a reader meets at /console/plan/#demo
// is the real surface with fabricated input, not a screenshot of one.
//
// It is the SAME story every other showcase tells (demo-scenario.ts), seen from the lease
// side: the acme monorepo's shared token library grew an audience on its claims type, and the
// blast radius took out a Go verifier and a TypeScript web client. The diff surface shows the
// resulting patch; the activity trail shows the failing services/identity:test run at 92m and the
// apps/dashboard typecheck diagnostics; THIS shows the plan an agent cut to do the work, with the
// same leases owning the same paths that appear as changed files over there. A reader who opens
// two surfaces finds the same names, files and reasons in both.
//
// It is written to exercise the surface honestly rather than flatteringly. All five states are
// present, including the two nobody wants: a lease that FAILED and one that NEVER RETURNED, which
// is the state the surface exists to surface - nobody is coming to tell you about that one. There
// is a real overlap (two leases whose declared paths intersect) and a stale non-terminal lease, so
// both warnings the surface draws are visible on arrival instead of only in a fault a demo never
// reaches. They land on different rows by necessity: a terminal row draws neither, so the stale
// one has to be a lease still nominally in flight.
//
// A pure function of an injected `now` with FIXED offsets and no Math.random, matching
// demo-scenario.ts: determinism is the point, so demo.test.ts can assert the plan's shape and the
// ages stay put across reads.

import type { Lease, LeaseOverlap } from "./ledger";

const MIN = 60;

// The paths are the diff showcase's changed files, lease by lease. That correspondence is the whole
// reason both fixtures are worth having: the plan says who was told to touch what, and the diff
// shows what they did to it.
export function demoLeases(nowMs: number): Lease[] {
  const now = Math.floor(nowMs / 1000);
  const at = (minsAgo: number): number => now - minsAgo * MIN;

  return [
    // The root. Still running because one child never reported - see dashboard-client below.
    {
      id: "claims-audience",
      goal: "Carry an audience through the token path, and fix every reader of the old field",
      checkpoint: "services/identity:test and apps/dashboard:typecheck both green",
      tier: "epic",
      state: "running",
      validation: "magus affected ci",
      created: at(104),
      updated: at(3),
    },
    // Done, and the source of everything below it: it released claims.go for the readers to build
    // on, which is what the release digest records.
    {
      id: "authkit-type",
      parent: "claims-audience",
      goal: "Replace Claims.Scope with an Audience slice and document the wire contract",
      checkpoint: "libs/authkit builds and its own tests pass",
      tier: "unit",
      state: "pass",
      validation: "magus run test libs/authkit",
      owned_paths: ["libs/authkit/claims.go", "libs/authkit/audience.go"],
      forbidden_paths: ["services/**", "apps/**"],
      created: at(104),
      updated: at(88),
      releases: [
        {
          path: "libs/authkit/claims.go",
          digest: "a77f30e2c1d94b6f8e0a5c3b7d21f4e9a8c06b5d3f2e1a09c7b4d6e8f0a2c5b31",
          released_at: at(88),
        },
        {
          path: "libs/authkit/audience.go",
          digest: "3f2e1a09c7b4d6e8f0a2c5b31a77f30e2c1d94b6f8e0a5c3b7d21f4e9a8c06b5d",
          released_at: at(88),
        },
      ],
    },
    // The Go reader. Green, and it waited on the type above.
    {
      id: "identity-verify",
      parent: "claims-audience",
      goal: "Assert on the audience in the token verifier instead of the removed scope",
      checkpoint: "services/identity:test green",
      tier: "unit",
      state: "pass",
      validation: "magus run test services/identity",
      depends_on: ["authkit-type"],
      owned_paths: ["services/identity/internal/token/"],
      created: at(96),
      updated: at(80),
    },
    // The overlap's other half: it claims one file INSIDE the directory the lease above claims. Both
    // declarations are legitimate and the surface does not rule on it - it draws the intersection
    // and the reader decides whether the plan meant it.
    {
      id: "verify-tests",
      parent: "claims-audience",
      goal: "Cover the two-service audience case the old scope test could not express",
      checkpoint: "a test that fails before authkit-type and passes after",
      tier: "unit",
      state: "fail",
      validation: "magus run test services/identity",
      depends_on: ["identity-verify"],
      owned_paths: ["services/identity/internal/token/verify_test.go"],
      created: at(96),
      updated: at(58),
    },
    // The one that needs a human: it never reported at all. No staleness warning rides along -
    // no_return is TERMINAL, and the surface deliberately draws neither warning on a terminal row.
    // The stale badge belongs to docs-rename below, which is non-terminal and untouched.
    {
      id: "dashboard-client",
      parent: "claims-audience",
      goal: "Read the audience array in the web client's session claims",
      checkpoint: "apps/dashboard:typecheck clean",
      tier: "unit",
      state: "no_return",
      validation: "magus run typecheck apps/dashboard",
      depends_on: ["authkit-type"],
      owned_paths: ["apps/dashboard/src/api/session.ts"],
      created: at(96),
      updated: at(84),
    },
    // Never started: the work is declared and waiting on the two readers landing.
    {
      id: "docs-rename",
      parent: "claims-audience",
      goal: "Rename the JWT page to tokens and fix the links that point at the old name",
      checkpoint: "no link in docs/ resolves to docs/auth/jwt.md",
      tier: "chore",
      state: "declared",
      read_only: false,
      depends_on: ["identity-verify", "dashboard-client"],
      owned_paths: ["docs/auth/"],
      created: at(104),
      updated: at(104),
    },
    // A read-only lease: it inspects and reports, and owns nothing. Its presence is what keeps
    // "owns no paths" a rendered case rather than a theoretical one.
    {
      id: "reach-survey",
      parent: "claims-audience",
      goal: "Report every project referencing the claims type before anything is renamed",
      checkpoint: "a list the parent can plan from",
      tier: "survey",
      state: "pass",
      read_only: true,
      created: at(104),
      updated: at(99),
    },
  ];
}

// The intersection the two Go leases declared. Reported as the route reports it: each side's own
// declaration, because "services/identity/internal/token/" and the one _test.go file inside it are
// rarely the same string and a reader who cannot tell which lease claimed which has nothing to act
// on.
export function demoOverlaps(): LeaseOverlap[] {
  return [
    {
      lease_a: "identity-verify",
      lease_b: "verify-tests",
      paths_a: ["services/identity/internal/token/"],
      paths_b: ["services/identity/internal/token/verify_test.go"],
    },
  ];
}
