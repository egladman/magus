package main

import (
	"flag"
	"fmt"
)

func main() {
	out := flag.String("out", "", "where")
	flag.Parse()
	fmt.Println(*out)
}
