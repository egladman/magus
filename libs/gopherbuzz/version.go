package buzz

// LanguageVersion is the Buzz language version gopherbuzz targets. Buzz 0.6.0 is
// unreleased: gopherbuzz tracks buzz-language/buzz `main`, which sits between the
// released 0.5.0 and the eventual 0.6.0, so the honest label is the in-development
// series. gopherbuzz stays compatible with released 0.5.0 and additionally
// implements the 0.6.0-dev conventions present at UpstreamRef -- namespace-decl
// resolution, `=>` arrow-body functions, and the `buzz:` stdlib import scheme.
const LanguageVersion = "0.6.0-dev"

// UpstreamRef pins the exact buzz-language/buzz commit gopherbuzz is measured
// against, as a `git describe`: the 0.5.0 tag plus the commits since
// (0.5.0-<N>-g<shortsha>). Because 0.6.0 is not tagged, a commit -- not a version
// number -- is the only precise statement of which upstream this is compared with.
//
// It is a comparison point, NOT a compatibility claim. gopherbuzz implements a
// subset: 26 of upstream's 83 behavior tests passed when this was last measured.
// The conformance test (conformance_test.go) is what enforces that score against
// this ref; the README's parity section carries the running record. Bump this and
// re-run the conformance target on every sync, so the score always describes the
// upstream actually being tracked.
const UpstreamRef = "0.5.0-251-ged42f47"
