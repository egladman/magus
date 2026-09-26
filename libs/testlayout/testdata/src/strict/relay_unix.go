// A source file, reported for its name alone. The toolchain reads no constraint from
// _unix, so this file builds everywhere and the test below runs on any host.
package strict // want `relay_unix.go is named for unix, which a file name does not constrain; .* relay_linux.go and relay_darwin.go, with relay_other.go`

func relay() int { return 0 }
