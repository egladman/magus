package buzz

// LanguageVersion is the Buzz language version gopherbuzz targets. Buzz 0.6.0 is
// unreleased: gopherbuzz tracks buzz-language/buzz `main`, which sits between the
// released 0.5.0 and the eventual 0.6.0, so the honest label is the in-development
// series. gopherbuzz stays compatible with released 0.5.0 and additionally
// implements the 0.6.0-dev conventions present at UpstreamRef -- namespace-decl
// resolution, `=>` arrow-body functions, and the `buzz:` stdlib import scheme.
const LanguageVersion = "0.6.0-dev"

// UpstreamRef pins the exact buzz-language/buzz commit gopherbuzz was synced and
// validated against, as a `git describe`: the 0.5.0 tag plus the commits since
// (0.5.0-<N>-g<shortsha>). Because 0.6.0 is not tagged, a commit -- not a version
// number -- is the only precise statement of "what upstream this is compatible
// with." Bump it, and re-validate against the upstream binary, on every sync.
const UpstreamRef = "0.5.0-251-ged42f47"
