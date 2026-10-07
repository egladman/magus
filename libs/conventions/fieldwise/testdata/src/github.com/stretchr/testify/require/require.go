// Package require stubs the testify functions the analyzer recognizes.
package require

type TestingT interface {
	Errorf(format string, args ...any)
	FailNow()
}

func Equal(t TestingT, expected, actual any, msgAndArgs ...any) {}
func NoError(t TestingT, err error, msgAndArgs ...any)          {}

type Assertions struct{ t TestingT }

func New(t TestingT) *Assertions { return &Assertions{t} }

func (a *Assertions) Equal(expected, actual any, msgAndArgs ...any)   {}
func (a *Assertions) Exactly(expected, actual any, msgAndArgs ...any) {}
