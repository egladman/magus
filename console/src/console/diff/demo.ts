// demo.ts - the Diff surface's daemon-free showcase (the shared #demo fragment).
//
// It supplies the two things the daemon would have supplied - a working-tree patch and an
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
// below are the same figures scenarioInsight reports for those paths, for the same reason.
//
// The fixture is a plain-data module by design (no DOM, no fetch, no protobuf), so
// demo.test.ts can assert the patch and the annotations agree without mounting anything.

import type { DiffSession, SessionOp } from "./session";

// The patch is an array of lines rather than one template literal because Go struct tags are
// backtick-quoted: a template literal would need every one of them escaped, and an escaped
// patch is a patch nobody can check against the tool that would have produced it.
const PATCH_LINES: readonly string[] = [
  // ---- libs/authkit/claims.go: the contract change everything else follows from ----------
  "diff --git a/libs/authkit/claims.go b/libs/authkit/claims.go",
  "index 4c1d9a2..a77f30e 100644",
  "--- a/libs/authkit/claims.go",
  "+++ b/libs/authkit/claims.go",
  "@@ -14,10 +14,12 @@ type Claims struct {",
  " \t// Subject is the account the token was minted for.",
  ' \tSubject string `json:"sub"`',
  " \t// Issuer is the authority that signed it.",
  ' \tIssuer string `json:"iss"`',
  "-\t// Scope is the space-separated permission list.",
  '-\tScope string `json:"scope"`',
  // The stub the failing run at 92m read as empty: one audience, never populated, no doc
  // comment. Deleting it beside the field that replaces it is what the story turns on.
  '-\tAudience string `json:"aud"`',
  "+\t// Audience is every service this token may be presented to. Asserted on",
  "+\t// verify, never logged: it names the systems the holder can reach.",
  '+\tAudience []string `json:"aud"`',
  "+\t// IssuedAt is when the authority signed the token, in epoch seconds.",
  '+\tIssuedAt int64 `json:"iat"`',
  " \t// ExpiresAt is when the token stops verifying.",
  ' \tExpiresAt int64 `json:"exp"`',
  " }",
  "@@ -41,8 +43,11 @@ func (c Claims) Valid(now time.Time) error {",
  ' \tif c.Subject == "" {',
  " \t\treturn ErrNoSubject",
  " \t}",
  "-\tif c.ExpiresAt < now.Unix() {",
  '-\t\treturn errors.New("token: expired")',
  "+\tif c.ExpiresAt <= now.Unix() {",
  "+\t\treturn ErrExpired",
  " \t}",
  "+\tif len(c.Audience) == 0 {",
  "+\t\treturn ErrNoAudience",
  "+\t}",
  " \treturn nil",
  " }",

  // ---- services/identity: the verifier that broke, and its fix --------------------------
  "diff --git a/services/identity/internal/token/verify.go b/services/identity/internal/token/verify.go",
  "index 9b0c145..2e83aa7 100644",
  "--- a/services/identity/internal/token/verify.go",
  "+++ b/services/identity/internal/token/verify.go",
  "@@ -28,12 +28,17 @@ func (v *Verifier) Verify(ctx context.Context, raw string) (*authkit.Claims, error) {",
  " \tclaims, err := v.keys.Decode(ctx, raw)",
  " \tif err != nil {",
  ' \t\treturn nil, fmt.Errorf("token: decode: %w", err)',
  " \t}",
  '-\tif claims.Scope == "" {',
  '-\t\treturn nil, errors.New("token: no scope")',
  "+\tif !slices.Contains(claims.Audience, v.audience) {",
  "+\t\t// The audience is the whole point of the claim: a token minted for the",
  "+\t\t// admin app must not verify here just because the signature checks out.",
  '+\t\treturn nil, fmt.Errorf("token: audience %q not accepted", v.audience)',
  " \t}",
  " \tif err := claims.Valid(v.now()); err != nil {",
  " \t\treturn nil, err",
  " \t}",
  "+\tif claims.IssuedAt > v.now().Add(leeway).Unix() {",
  "+\t\treturn nil, ErrFromTheFuture",
  "+\t}",
  " \treturn claims, nil",
  " }",

  // ---- apps/dashboard: the same change on the web side ----------------------------------
  "diff --git a/apps/dashboard/src/api/session.ts b/apps/dashboard/src/api/session.ts",
  "index 0d5f7b1..c4a9e33 100644",
  "--- a/apps/dashboard/src/api/session.ts",
  "+++ b/apps/dashboard/src/api/session.ts",
  "@@ -38,7 +38,8 @@ export interface SessionClaims {",
  "   readonly subject: string;",
  "-  readonly scope: string;",
  "+  readonly audience: readonly string[];",
  "+  readonly issuedAt: number;",
  "   readonly expiresAt: number;",
  " }",
  " export function canReach(claims: SessionClaims, service: string): boolean {",
  '-  return claims.scope.split(" ").includes(service);',
  "+  return claims.audience.includes(service);",
  " }",

  // ---- a file added on this branch: no history, no coverage, nothing measured -----------
  "diff --git a/libs/authkit/audience.go b/libs/authkit/audience.go",
  "new file mode 100644",
  "index 0000000..5f1b2c9",
  "--- /dev/null",
  "+++ b/libs/authkit/audience.go",
  "@@ -0,0 +1,13 @@",
  "+package authkit",
  "+",
  "+// Audiences is the set a verifier accepts. Ordered, so a token minted for two",
  "+// services verifies the same way whichever one reads it first.",
  "+type Audiences []string",
  "+",
  "+// Accepts reports whether aud names a service in the set.",
  "+func (a Audiences) Accepts(aud string) bool {",
  "+\treturn slices.Contains(a, aud)",
  "+}",
  "+",
  "+// ErrNoAudience is what Claims.Valid returns for a token carrying no audience.",
  '+var ErrNoAudience = errors.New("token: no audience")',

  // ---- a deletion: the pre-audience workaround the branch retires -----------------------
  "diff --git a/services/identity/internal/token/legacy_audience.go b/services/identity/internal/token/legacy_audience.go",
  "deleted file mode 100644",
  "index 8ad0f21..0000000",
  "--- a/services/identity/internal/token/legacy_audience.go",
  "+++ /dev/null",
  "@@ -1,13 +0,0 @@",
  "-package token",
  "-",
  "-// legacyAudience read the audience out of the scope string, which is where it",
  "-// lived before authkit.Claims carried one. Nothing mints scope-encoded",
  "-// audiences any more.",
  "-func legacyAudience(scope string) string {",
  "-\tfor _, s := range strings.Fields(scope) {",
  '-\t\tif strings.HasPrefix(s, "aud:") {',
  '-\t\t\treturn strings.TrimPrefix(s, "aud:")',
  "-\t\t}",
  "-\t}",
  '-\treturn ""',
  "-}",

  // ---- the test that named the failure, updated -----------------------------------------
  "diff --git a/services/identity/internal/token/verify_test.go b/services/identity/internal/token/verify_test.go",
  "index 71c0aa8..b9d4e51 100644",
  "--- a/services/identity/internal/token/verify_test.go",
  "+++ b/services/identity/internal/token/verify_test.go",
  "@@ -140,9 +140,16 @@ func TestVerifyAudienceMismatch(t *testing.T) {",
  ' \tv := newTestVerifier(t, "acme-dashboard")',
  ' \tclaims, err := v.Verify(t.Context(), mint(t, "acme-admin"))',
  "-\tif err != nil {",
  '-\t\tt.Fatalf("Verify(admin token) error = %v, want nil", err)',
  "+\tif !errors.Is(err, token.ErrAudience) {",
  '+\t\tt.Fatalf("Verify(admin token) error = %v, want %v", err, token.ErrAudience)',
  " \t}",
  '-\tif claims.Audience != "acme-dashboard" {',
  '-\t\tt.Fatalf("claims.Audience = %q, want %q", claims.Audience, "acme-dashboard")',
  "+\tif claims != nil {",
  '+\t\tt.Fatalf("Verify(admin token) claims = %v, want nil", claims)',
  " \t}",
  " }",
  "+",
  "+func TestVerifyFromTheFuture(t *testing.T) {",
  '+\tv := newTestVerifier(t, "acme-dashboard")',
  '+\tif _, err := v.Verify(t.Context(), mintAt(t, "acme-dashboard", future)); err == nil {',
  '+\t\tt.Fatal("Verify(future token) error = nil, want token: issued in the future")',
  "+\t}",
  "+}",

  // ---- a rename plus an edit, in a file no project declares -----------------------------
  "diff --git a/docs/auth/jwt.md b/docs/auth/tokens.md",
  "similarity index 74%",
  "rename from docs/auth/jwt.md",
  "rename to docs/auth/tokens.md",
  "index 2c9e004..6b1f8ad 100644",
  "--- a/docs/auth/jwt.md",
  "+++ b/docs/auth/tokens.md",
  "@@ -1,9 +1,10 @@",
  " # Tokens",
  " ",
  "-A token carries a subject, an issuer, a scope and an expiry. The scope is a",
  "-space-separated permission list, and every service reads it to decide what a",
  "-caller may do.",
  "+A token carries a subject, an issuer, an audience and an expiry. The audience",
  "+names every service the token may be presented to, and a service not named in",
  "+it must refuse the token even when the signature verifies.",
  " ",
  " ## Verifying",
  " ",
  "-Call `authkit.Claims.Valid` and then check the scope yourself.",
  "+Call `authkit.Claims.Valid`, then check the audience with `Audiences.Accepts`.",
  "+A token carrying no audience at all is refused by `Valid` - see `ErrNoAudience`.",

  // ---- declared outputs: folded away, because reading them is reading a restatement -----
  "diff --git a/libs/protocol/gen/token_pb.go b/libs/protocol/gen/token_pb.go",
  "index 33ab901..cd7e412 100644",
  "--- a/libs/protocol/gen/token_pb.go",
  "+++ b/libs/protocol/gen/token_pb.go",
  "@@ -211,6 +211,7 @@ func (x *Token) GetClaims() *Claims {",
  " type Claims struct {",
  " \tstate         protoimpl.MessageState",
  ' \tSubject       string   `protobuf:"bytes,1,opt,name=subject,proto3" json:"subject,omitempty"`',
  '-\tScope         string   `protobuf:"bytes,2,opt,name=scope,proto3" json:"scope,omitempty"`',
  '+\tAudience      []string `protobuf:"bytes,2,rep,name=audience,proto3" json:"audience,omitempty"`',
  '+\tIssuedAt      int64    `protobuf:"varint,3,opt,name=issued_at,json=issuedAt,proto3" json:"issued_at,omitempty"`',
  ' \tExpiresAt     int64    `protobuf:"varint,4,opt,name=expires_at,json=expiresAt,proto3" json:"expires_at,omitempty"`',
  " }",
  "diff --git a/apps/dashboard/src/gen/session_pb.ts b/apps/dashboard/src/gen/session_pb.ts",
  "index a71ff30..e0c4b18 100644",
  "--- a/apps/dashboard/src/gen/session_pb.ts",
  "+++ b/apps/dashboard/src/gen/session_pb.ts",
  "@@ -64,5 +64,6 @@",
  "   fields: [",
  '     { no: 1, name: "subject", kind: "scalar", T: 9 },',
  '-    { no: 2, name: "scope", kind: "scalar", T: 9 },',
  '+    { no: 2, name: "audience", kind: "scalar", T: 9, repeated: true },',
  '+    { no: 3, name: "issued_at", kind: "scalar", T: 3 },',
  '     { no: 4, name: "expires_at", kind: "scalar", T: 3 },',
  "   ],",
  "diff --git a/docs/gen/auth/tokens.html b/docs/gen/auth/tokens.html",
  "index 5504e1b..9c2f7a6 100644",
  "--- a/docs/gen/auth/tokens.html",
  "+++ b/docs/gen/auth/tokens.html",
  "@@ -18,3 +18,3 @@",
  "   <h1>Tokens</h1>",
  "-  <p>A token carries a subject, an issuer, a scope and an expiry.</p>",
  "+  <p>A token carries a subject, an issuer, an audience and an expiry.</p>",
  "   <h2>Verifying</h2>",
];

// DEMO_PATCH is the working-tree patch, in the shape /api/v1/diff/patch returns.
export const DEMO_PATCH = `${PATCH_LINES.join("\n")}\n`;

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
// Files are listed in READING ORDER, which is what the surface honours: the shared library's
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
