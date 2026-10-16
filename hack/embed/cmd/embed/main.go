package main

import (
	"fmt"
	"os"
)

const usage = `usage: embed <command> [flags]

commands:
  fetch           download the pinned inputs named in inputs.jsonl and verify their sha256
  teacher         embed a texts file with a teacher model into resumable, content-addressed shards
  teacher-parity  check the Go teacher against the reference vectors and report texts/s

run "embed <command> -h" for a command's flags`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "fetch":
		err = runFetch(os.Args[2:])
	case "teacher":
		err = runTeacher(os.Args[2:])
	case "teacher-parity":
		err = runParity(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "embed %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}
