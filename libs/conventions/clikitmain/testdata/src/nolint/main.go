package main // want `a //nolint cannot exempt clikitmain`

import "flag"

func main() {
	//nolint:clikitmain // the fixture pins that a directive excuses nothing
	flag.Parse() // want `flag.Parse parses the command line by hand`
}
