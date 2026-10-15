//go:build !wasm

package std

var nativeModules = []Module{
	Archive, Fs, HTTP, Magus, Net, Os, Pipe, Proc, Term, Vcs,
}
