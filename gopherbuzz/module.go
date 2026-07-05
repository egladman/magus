package buzz

import (
	"context"
	"fmt"
	"io"
)

// Module describes one importable Buzz module for registration on a session: the
// bare name a program imports, free-form Labels that classify it (provenance,
// WASM-safety, ...), and a Bind hook that wires it onto a session -- installing a
// fresh module, or merging onto one an earlier Module provided under the same name.
//
// It is the single shape gopherbuzz's stdlib and a host embedder (e.g. magus's
// os/vcs/http surface) both use to describe a module, so a session's whole import
// surface is one ordered, labeled list: Session.Provide applies it, and a caller
// filters by label to derive a subset (the WASM playground, a strict-conformance
// run, a docs index).
//
// This is the *registration* descriptor -- how to install a module -- and is
// distinct from a host's richer *API* descriptor (magus/std.Module carries a
// module's methods and fields for documentation and binding codegen). A given
// module may have both: one says how to install it, the other what it exposes.
type Module struct {
	// Name is the bare string a program imports, e.g. "os" in `import "os"`.
	Name string
	// Labels classify the module for filtering. See the Label* constants for the
	// vocabulary gopherbuzz applies to its own stdlib; a host adds its own
	// (e.g. "host", "wasm").
	Labels []string
	// Bind wires the module onto sess. It may install a fresh module (via
	// Session.SetSyntheticModule / SetSourceModule) or read back and extend one an
	// earlier Module already provided under Name (host methods over the stdlib).
	Bind func(sess *Session, env ModuleEnv) error
}

// ModuleEnv carries what a Bind hook may need beyond the session itself: the
// context a host module captures, and the writer std's `print` should target. A
// Module ignores the fields it does not use.
type ModuleEnv struct {
	Ctx context.Context
	Out io.Writer
}

// Well-known module labels. Labels are free-form strings; these are the vocabulary
// gopherbuzz applies to its own stdlib. A host defines additional labels as needed.
const (
	// LabelUpstream marks a clean-room reimplementation of a module in upstream
	// Buzz's standard library (names, signatures, and semantics track upstream).
	LabelUpstream = "upstream"
	// LabelExtension marks a gopherbuzz-original module with no upstream counterpart.
	LabelExtension = "extension"
)

// HasLabel reports whether m carries label.
func (m Module) HasLabel(label string) bool {
	for _, l := range m.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// Provide binds each module onto the session in order. Order is significant: a
// later module may merge onto an earlier one that shares its Name (a host layering
// methods onto the stdlib), so lower-precedence modules come first. Provide stops
// at the first Bind error and returns it.
func (s *Session) Provide(env ModuleEnv, mods ...Module) error {
	for _, m := range mods {
		if m.Bind == nil {
			continue
		}
		if err := m.Bind(s, env); err != nil {
			return fmt.Errorf("buzz: provide module %q: %w", m.Name, err)
		}
	}
	return nil
}
