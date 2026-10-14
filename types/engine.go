package types

import "fmt"

// EngineNotLinkedError reports an attempt to evaluate a magusfile in a program that does not
// link the Buzz engine. magus never returns an empty result in its place: a workspace loaded
// without its magusfiles would look like one that declares no targets.
type EngineNotLinkedError struct {
	// Op names the evaluation that needed the engine, such as "evaluate /repo/magusfile.buzz".
	Op string
}

func (e *EngineNotLinkedError) Error() string {
	return FormatDiagnostic(EngineNotLinked, fmt.Sprintf(
		"cannot %s: this program does not link the Buzz engine; add the blank import "+
			`_ "github.com/egladman/magus/buzzengine" to its main package`, e.Op))
}
