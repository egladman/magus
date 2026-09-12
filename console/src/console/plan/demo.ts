// demo.ts - the Jobs view's daemon-free showcase (the shared #demo fragment).
//
// It supplies the one thing the daemon would have supplied - a job listing - and nothing else
// changes: buildJobTree(), treeOrder(), layoutNodes(), the overlap warnings, the staleness rule and
// the detail are all the production paths. So what a reader meets at #demo is the real view with
// fabricated input, not a screenshot of one.
//
// Both holders are here, because that is the claim the view makes: the daemon's own maintenance jobs
// and the work a session was handed are ONE list, told apart by their holder rather than by living
// on two different screens.
//
// It is the SAME story every other showcase tells (demo-scenario.ts), seen from the work side: the
// acme monorepo's shared token library grew an audience on its claims type, and the blast radius
// took out a Go verifier and a TypeScript web client. The diff surface shows the resulting patch;
// the activity trail shows the failing services/identity:test run at 92m and the apps/dashboard
// typecheck diagnostics; THIS shows the work an agent cut to do it, with the same jobs owning the
// same paths that appear as changed files over there.
//
// It is written to exercise the view honestly rather than flatteringly. All five states are
// present, including the two nobody wants: a job that FAILED and one that NEVER RETURNED, which is
// the state the view exists to surface - nobody is coming to tell you about that one. There is a
// real overlap (two jobs whose declared paths intersect) and a stale non-terminal job, so both
// warnings are visible on arrival instead of only in a fault a demo never reaches. They land on
// different rows by necessity: a terminal job draws neither, so the stale one has to be still
// nominally in flight.
//
// A pure function of an injected `now` with FIXED offsets and no Math.random, matching
// demo-scenario.ts: determinism is the point, so demo.test.ts can assert the shape and the ages stay
// put across reads.

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import {
  JobHolder,
  JobOverlapSchema,
  JobSchema,
  type Job,
  type JobOverlap,
} from "@wire/job/v1alpha1/job_pb";

const MIN = 60;

// session builds one job a session holds, filling the two fields every caller would otherwise
// repeat: the resource name the id implies, and the holder that makes it session work.
function session(id: string, fields: MessageInitShape<typeof JobSchema>): Job {
  return create(JobSchema, {
    ...fields,
    name: "jobs/" + id,
    id,
    holder: JobHolder.SESSION,
  });
}

// The paths are the diff showcase's changed files, job by job. That correspondence is the whole
// reason both fixtures are worth having: this says who was told to touch what, and the diff shows
// what they did to it.
export function demoJobs(nowMs: number): Job[] {
  const now = Math.floor(nowMs / 1000);
  const at = (minsAgo: number): bigint => BigInt(now - minsAgo * MIN);

  return [
    // The daemon's own catalog: no goal, no parent, nothing declared it - the daemon maintains its
    // own house, and a reader sees that beside the work a session was handed.
    create(JobSchema, {
      name: "jobs/rotate-activities",
      id: "rotate-activities",
      holder: JobHolder.DAEMON,
      description: "Trim the activity trail to its cap",
      state: "pass",
      target: { sizeBytes: 2_310_144n, itemCount: 4_812n },
      lastRun: { endTime: { seconds: at(26) }, ok: true },
      created: at(720),
      updated: at(26),
    }),
    create(JobSchema, {
      name: "jobs/clear-cache",
      id: "clear-cache",
      holder: JobHolder.DAEMON,
      description: "Invalidate cached build entries",
      state: "running",
      running: true,
      target: { sizeBytes: 981_467_136n, itemCount: 1_207n },
      lastRun: { endTime: { seconds: at(190) }, ok: true },
      created: at(720),
      updated: at(1),
    }),
    // The root of the session work. Still running because one child never reported - see
    // dashboard-client below.
    session("claims-audience", {
      goal: "Carry an audience through the token path, and fix every reader of the old field",
      checkpoint: "services/identity:test and apps/dashboard:typecheck both green",
      model: "principal",
      state: "running",
      check: "magus affected ci",
      created: at(104),
      updated: at(3),
    }),
    // Done, and the source of everything below it: it released claims.go for the readers to build
    // on, which is what the release digest records.
    session("authkit-type", {
      parent: "claims-audience",
      goal: "Replace Claims.Scope with an Audience slice and document the wire contract",
      checkpoint: "libs/authkit builds and its own tests pass",
      model: "standard",
      state: "pass",
      check: "magus run test libs/authkit",
      writePaths: ["libs/authkit/claims.go", "libs/authkit/audience.go"],
      denyPaths: ["services/**", "apps/**"],
      created: at(104),
      updated: at(88),
      releases: [
        {
          path: "libs/authkit/claims.go",
          digest: "a77f30e2c1d94b6f8e0a5c3b7d21f4e9a8c06b5d3f2e1a09c7b4d6e8f0a2c5b31",
          releasedAt: at(88),
        },
        {
          path: "libs/authkit/audience.go",
          digest: "3f2e1a09c7b4d6e8f0a2c5b31a77f30e2c1d94b6f8e0a5c3b7d21f4e9a8c06b5d",
          releasedAt: at(88),
        },
      ],
    }),
    // The Go reader. Green, and it waited on the type above.
    session("identity-verify", {
      parent: "claims-audience",
      goal: "Assert on the audience in the token verifier instead of the removed scope",
      checkpoint: "services/identity:test green",
      model: "standard",
      state: "pass",
      check: "magus run test services/identity",
      dependsOn: ["authkit-type"],
      writePaths: ["services/identity/internal/token/"],
      created: at(96),
      updated: at(80),
    }),
    // The overlap's other half: it claims one file INSIDE the directory the job above claims. Both
    // declarations are legitimate and the view does not rule on it - it draws the intersection and
    // the reader decides whether the work was meant to be cut that way.
    session("verify-tests", {
      parent: "claims-audience",
      goal: "Cover the two-service audience case the old scope test could not express",
      checkpoint: "a test that fails before authkit-type and passes after",
      model: "standard",
      state: "fail",
      check: "magus run test services/identity",
      dependsOn: ["identity-verify"],
      writePaths: ["services/identity/internal/token/verify_test.go"],
      created: at(96),
      updated: at(58),
    }),
    // The one that needs a human: it never reported at all. No staleness warning rides along -
    // no_return is TERMINAL, and the view deliberately draws neither warning on a terminal job. The
    // stale mark belongs to docs-rename below, which is non-terminal and untouched.
    session("dashboard-client", {
      parent: "claims-audience",
      goal: "Read the audience array in the web client's session claims",
      checkpoint: "apps/dashboard:typecheck clean",
      model: "standard",
      state: "no_return",
      check: "magus run typecheck apps/dashboard",
      dependsOn: ["authkit-type"],
      writePaths: ["apps/dashboard/src/api/session.ts"],
      created: at(96),
      updated: at(84),
    }),
    // Never started: the work is declared and waiting on the two readers landing.
    session("docs-rename", {
      parent: "claims-audience",
      goal: "Rename the JWT page to tokens and fix the links that point at the old name",
      checkpoint: "no link in docs/ resolves to docs/auth/jwt.md",
      model: "economy",
      state: "declared",
      dependsOn: ["identity-verify", "dashboard-client"],
      writePaths: ["docs/auth/"],
      created: at(104),
      updated: at(104),
    }),
    // A read-only job: it inspects and reports, and owns nothing. Its presence is what keeps "owns
    // no paths" a rendered case rather than a theoretical one.
    session("reach-survey", {
      parent: "claims-audience",
      goal: "Report every project referencing the claims type before anything is renamed",
      checkpoint: "a list the parent can work from",
      model: "economy",
      state: "pass",
      readOnly: true,
      readPaths: ["libs/authkit/", "services/", "apps/"],
      created: at(104),
      updated: at(99),
    }),
  ];
}

// The intersection the two Go jobs declared. Reported the way the daemon reports it: each side's own
// declaration, because "services/identity/internal/token/" and the one _test.go file inside it are
// rarely the same string and a reader who cannot tell which job claimed which has nothing to act on.
export function demoOverlaps(): JobOverlap[] {
  return [
    create(JobOverlapSchema, {
      jobA: "identity-verify",
      jobB: "verify-tests",
      pathsA: ["services/identity/internal/token/"],
      pathsB: ["services/identity/internal/token/verify_test.go"],
    }),
  ];
}
