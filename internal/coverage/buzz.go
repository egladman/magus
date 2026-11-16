package coverage

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/egladman/magus/libs/clikit/exit"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// BuzzScript is one program a `magus buzz -t` run tests, with the name its
// diagnostics carry.
type BuzzScript struct {
	Code, Name string
	Args       []string
}

// BuzzReader resolves program text from an -e snippet and positional args, as
// `magus buzz` does for a single script.
type BuzzReader func(eval string, args []string) (code, name string, scriptArgs []string, err error)

// BuzzTestSources resolves what -t tests: an -e snippet, piped stdin, or every
// path given. Args after -- go to a single script, never to several. Misuse is
// an exit.Usage error.
func BuzzTestSources(read BuzzReader, eval string, paths, forwarded []string) ([]BuzzScript, error) {
	if len(paths) > 1 && len(forwarded) > 0 {
		return nil, exit.Usagef("magus buzz: args after -- go to one script, and -t was given %d", len(paths))
	}
	if eval != "" {
		code, name, _, err := read(eval, paths)
		return []BuzzScript{{Code: code, Name: name, Args: forwarded}}, err
	}
	if len(paths) == 0 {
		return stdinScript(read, forwarded)
	}
	scripts := make([]BuzzScript, 0, len(paths))
	for _, p := range paths {
		code, name, _, err := read("", []string{p})
		if err != nil {
			return nil, err
		}
		scripts = append(scripts, BuzzScript{Code: code, Name: name, Args: forwarded})
	}
	return scripts, nil
}

// stdinScript reads the script from stdin only when one is piped. A terminal
// would block, and /dev/null would pass having tested nothing.
func stdinScript(read BuzzReader, forwarded []string) ([]BuzzScript, error) {
	if !stdinIsPiped() {
		return nil, exit.Usagef("magus buzz: -t <file>... names the scripts to test; none was given and nothing is piped on stdin")
	}
	code, name, _, err := read("", nil)
	return []BuzzScript{{Code: code, Name: name, Args: forwarded}}, err
}

// stdinIsPiped reports whether stdin is a pipe or a regular file, the two that
// carry a script. A terminal and /dev/null are character devices.
func stdinIsPiped() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeNamedPipe != 0 || info.Mode().IsRegular()
}

// BuzzTestScripts runs every script through run, each in its own session, so a
// failing one does not hide the next. A non-empty profile names one LCOV file
// that holds every script's lines; run receives the shared collector, nil
// without one.
func BuzzTestScripts(ctx context.Context, profile string, scripts []BuzzScript, run func(context.Context, BuzzScript, *buzz.LineCoverage) error) error {
	var cov *buzz.LineCoverage
	if profile != "" {
		cov = buzz.NewLineCoverage()
	}
	errs := make([]error, 0, len(scripts)+1)
	for _, s := range scripts {
		errs = append(errs, run(ctx, s, cov))
	}
	if cov != nil {
		if err := os.WriteFile(profile, []byte(cov.Report()), 0o644); err != nil {
			errs = append(errs, fmt.Errorf("magus buzz: write --coverprofile: %w", err))
		}
	}
	return errors.Join(errs...)
}
