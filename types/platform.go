package types

// PlatformStyle names the naming convention a platform\arch or platform\os call
// renders its canonical answer in.
//
// A named type with a declared case list rather than a bare string, for the same
// reason as [SignAlgorithm]: renderPlatform rejects an unknown style at RUN time
// ("unknown style %q (want go|uname)"), which lands halfway through whatever
// release or container job asked for it. Declared here, a typo is a checker error
// at load. The registry between here and Buzz is cmd/magus-utils/boundary_types.go;
// adding a case there and here is what makes the validator, the error message, and
// the Buzz mirror all follow.
//
// The zero value means "unset" and renders the Go form, which is why the argument
// stays optional: platform\arch("x86_64") and platform\arch("x86_64", .go) agree.
type PlatformStyle string

const (
	// PlatformStyleGo renders canonical Go GOOS/GOARCH spellings (darwin, amd64).
	// It is the default, and what the rest of magus indexes platforms by.
	PlatformStyleGo PlatformStyle = "go"
	// PlatformStyleUname renders the spellings uname -m / uname -s report
	// (x86_64, aarch64, Darwin) - the form a shell script or a download URL
	// built for a release asset usually wants.
	PlatformStyleUname PlatformStyle = "uname"
)
