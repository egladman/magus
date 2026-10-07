// Package assert stubs the testify functions the analyzer recognizes.
package assert

type TestingT interface{ Errorf(format string, args ...any) }

func Equal(t TestingT, expected, actual any, msgAndArgs ...any) bool        { return true }
func Equalf(t TestingT, expected, actual any, msg string, args ...any) bool { return true }
func EqualValues(t TestingT, expected, actual any, msgAndArgs ...any) bool  { return true }
func Exactly(t TestingT, expected, actual any, msgAndArgs ...any) bool      { return true }
func NotEqual(t TestingT, expected, actual any, msgAndArgs ...any) bool     { return true }
func Len(t TestingT, object any, length int, msgAndArgs ...any) bool        { return true }
func NoError(t TestingT, err error, msgAndArgs ...any) bool                 { return true }

type Assertions struct{ t TestingT }

func New(t TestingT) *Assertions { return &Assertions{t} }

func (a *Assertions) Equal(expected, actual any, msgAndArgs ...any) bool { return true }
