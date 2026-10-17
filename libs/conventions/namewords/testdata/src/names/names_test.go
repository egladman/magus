package names

import "testing"

// Entry points read as sentences.
func TestStageFor(t *testing.T) {}

func BenchmarkParseSrc(b *testing.B) {}

func FuzzValueOf(f *testing.F) {}

func ExampleServableTo() {}

func Example_relativeTo() {}

func Test(t *testing.T) {}

// A helper is judged like any other function, and so is a name that only
// opens with an entry point's prefix, or a method.
func hashOf(t *testing.T) string { return "" } // want `hashOf ends in the word "Of"`

func Testify() {}

func TestifyFor() {} // want `TestifyFor ends in the word "For"`

type suite struct{}

func (suite) TestRunsFor() {} // want `TestRunsFor ends in the word "For"`
