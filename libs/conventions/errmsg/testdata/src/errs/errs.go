package errs

import (
	"diag"
	"errors"
	"fmt"
)

const joined = "lock held; retry later"

const prefix = "parse config"

func join(name string, err error) {
	_ = errors.New("lock held; retry later")    // want `error-join: a clause joined by "; "`
	_ = fmt.Errorf("read %s - %w", name, err)   // want `error-join: a clause joined by " - "` `error-wrap`
	_ = fmt.Errorf("read %s — not found", name) // want `error-join: a clause joined by "\x{2014}"`
	_ = errors.New(joined)                      // want `error-join`
	_ = errors.New(prefix + "; " + name)        // want `error-join`
	_ = errors.New("lock held, retry later")
	_ = fmt.Errorf("want %q", "a; b")
	_ = errors.New(`flag "a; b" is malformed`)
	_ = fmt.Errorf("range 1-%d", 3)
}

func newline(name string) {
	_ = fmt.Errorf("bad input:\n%s", name) // want `error-newline`
	_ = errors.New("one\ntwo")             // want `error-newline`
}

func sentences(path string, err error) {
	_ = errors.New("no workspace. Run magus init") // want `error-sentences`
	_ = fmt.Errorf("stale index. %w", err)         // want `error-sentences` `error-wrap`
	_ = errors.New("pass a path, e.g. ./cmd")
	_ = errors.New("one of tar, zip, etc. is required")
	_ = fmt.Errorf("version %d.%d is too old", 1, 2)
	_ = fmt.Errorf("width %.2f exceeds %s", 1.5, path)
	_ = errors.New(`file "a. b" is missing`)
	_ = errors.New("waiting... done")
	_ = fmt.Errorf("takes %d args (incl. ctx)", 2)
	_ = errors.New("two devs. Then more") // want `error-sentences`
	_ = errors.New("reading main.go failed")
}

func wrap(path string, err error) {
	_ = fmt.Errorf("open %s: %w", path, err)
	_ = fmt.Errorf("open %[1]s: %[2]w", path, err)
	_ = fmt.Errorf("%w: open %s", err, path)
	_ = fmt.Errorf("%w: %w", err, err)
	_ = fmt.Errorf("open %s %w", path, err)           // want `error-wrap: %w only opens the format as "%w: " or closes it as ": %w"`
	_ = fmt.Errorf("open %s: %w: %w", path, err, err) // want `error-wrap`
	_ = fmt.Errorf("%w open %s", err, path)           // want `error-wrap`
	_ = fmt.Errorf("100%% done: %w", err)
	_ = errors.New("prints %w literally - as text") // want `error-join`
}

// A coded diagnostic's constructor is no plain error, but a plain error built
// to wrap inside one is.
func diagnostics(err error) {
	_ = diag.Errorf(1, "lock held; retry later")
	_ = diag.Wrap(1, fmt.Errorf("lock held; retry later: %w", err)) // want `error-join`
	_ = diag.Wrap(1, errors.New("one\ntwo"))                        // want `error-newline`
}

type lockErr struct{ path string }

func (e lockErr) Error() string {
	return "lock held on " + e.path + "; retry later" // want `error-join`
}

type usageErr struct{ flag string }

func (e *usageErr) Error() string {
	if e.flag == "" {
		return "bad usage. See help" // want `error-sentences`
	}
	return fmt.Sprintf("unknown flag %s\nrun with -h", e.flag) // want `error-newline`
}

type wrapErr struct{ err error }

func (e wrapErr) Error() string {
	describe := func() string { return "not judged; a closure returns for itself" }
	_ = describe
	return "open: " + e.err.Error()
}

// Error here is no error method: it takes an argument.
type logger struct{}

func (logger) Error(msg string) string { return msg + "; logged" }
