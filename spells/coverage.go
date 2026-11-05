package spells

// CoverageFormat names the report format a spell's coverage op writes.
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

// CoverageOp is the op magus registers a declared Coverage under. A spell
// authoring an op of this name is refused at load.
const CoverageOp = "coverage"

// CoverageDirEnvVar names the directory magus hands a coverage op for its
// report, fresh per run. A spell never sets it.
const CoverageDirEnvVar = "MAGUS_COVERAGE_DIR"

// Coverage is what mgs_getCoverage declares: how the spell runs its tests
// with line coverage on, and the op whose scope narrows it. Exporting it IS
// the capability; a spell that does not export it cannot be scored.
type Coverage struct {
	Format CoverageFormat `json:"format"`
	// File is the report's name under MAGUS_COVERAGE_DIR, a plain file name.
	File string `json:"file"`
	// Command writes the report. It gets the scope a change's touched files
	// narrow Test to, so coverage meets the tests that reach them.
	Command Command `json:"command"`
	// Test is an op in mgs_listTargets whose test_scope narrows the report.
	Test string `json:"test"`
	// Uses names tools from mgs_getTools whose version keys the op.
	Uses []string `json:"uses,omitempty"`
}
