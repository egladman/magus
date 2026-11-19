package main

import (
	"flag"
	"fmt"
)

func main() {
	out := flag.String("out", "", "where") // want `over/main.go holds 2 hand-parsed command-line reads, past the 1 the baseline allows`
	flag.Parse()                           // want `over/main.go holds 2`
	fmt.Println(*out)
}
