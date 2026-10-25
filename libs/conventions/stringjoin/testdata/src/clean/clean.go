package clean

import "fmt"

const wrapped = "a long message " +
	"wrapped across " +
	"lines"

const prefix = "magus: "

func pair(a, b string) string {
	return a + b
}

func constants() string {
	return prefix + "constant " + "only"
}

func numbers(a, b, c int) int {
	return a + b + c
}

func formatted(name string) string {
	return fmt.Sprintf("hello, %s!", name)
}

func grouped(a, b, c string) string {
	return pair(a, b) + c
}

func url(host string) string {
	return "https://" + host
}

var _ = wrapped
