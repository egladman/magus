package main

// fixedSizeTerminal answers as a terminal of a fixed size.
type fixedSizeTerminal struct{ w, h int }

func (fixedSizeTerminal) IsTerminal(uintptr) bool          { return true }
func (f fixedSizeTerminal) Size(uintptr) (int, int, error) { return f.w, f.h, nil }
