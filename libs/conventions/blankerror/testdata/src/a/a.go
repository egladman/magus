package a

import "os"

func untouched() {
	_ = os.Remove("x")
}

func edited() {
	_ = os.Remove("x")
	println("after")
}

func adds() {
	println("before")
	_ = os.Remove("flagged")
	_, _ = os.Open("flagged")
}

func reasoned() {
	println("before")
	_ = os.Remove("x") // the file may be gone already
}

func fixes() {
	println("before")
	_ = len("x")
	var n int
	_ = n
}
