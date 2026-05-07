// coverage-badge renders a badge SVG to stdout from a label, message, and
// color via github.com/narqo/go-badge. It carries no logic of its own: the
// magusfile's coverage target computes the coverage total and picks the color,
// then writes this command's stdout to coverage.svg. narqo is a library with no
// CLI, so this is the minimal main that lets the Buzz target reach it.
package main

import (
	"flag"
	"log"
	"os"

	badge "github.com/narqo/go-badge"
)

func main() {
	label := flag.String("label", "coverage", "left-hand label")
	message := flag.String("message", "", "right-hand value")
	color := flag.String("color", "", "right-hand color (go-badge name or hex)")
	flag.Parse()

	if err := badge.Render(*label, *message, badge.Color(*color), os.Stdout); err != nil {
		log.Fatal(err)
	}
}
