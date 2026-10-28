//go:build !wasm

package std

var nativeModules = []Module{
	Archive, Fs, HTTP, Image, Magus, Net, Os, Pipe, Proc, Term, Vcs,
}
