package spells

// CoverageFormat names the format of a spell's coverage profile.
type CoverageFormat string

const (
	// CoverageFormatNone is the zero value, refused at decode: a report
	// whose format magus guesses would read as 0% or 100% covered.
	CoverageFormatNone CoverageFormat = ""
	// CoverageFormatLCOV is the LCOV tracefile; magus reads its DA lines.
	CoverageFormatLCOV CoverageFormat = "lcov"
	// CoverageFormatGoProfile is `go test -coverprofile` output.
	CoverageFormatGoProfile CoverageFormat = "go-coverprofile"
)

// Coverage is what mgs_getCoverage declares: where a project's tests leave
// their line coverage report. Exporting it IS the capability. magus runs no
// coverage of its own: the project target that declares Profile among its
// outputs writes it, and a reader freshens that target before reading. A
// report credits a line only to the tests of the package that holds it.
type Coverage struct {
	Format CoverageFormat `json:"format"`
	// Profile is the report's path, relative to the project directory.
	Profile string `json:"profile"`
}
