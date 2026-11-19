package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	out := flag.String("out", "", "where") // want `flag.String parses the command line by hand`
	fs := flag.NewFlagSet("sub", flag.ContinueOnError)
	all := fs.Bool("all", false, "every one") // want `\(\*flag.FlagSet\).Bool parses the command line by hand`
	flag.Parse()                              // want `flag.Parse parses the command line by hand`
	if len(os.Args) > 1 {                     // want `os.Args parses the command line by hand`
		fmt.Println(*out, *all, flag.ErrHelp)
	}
}
