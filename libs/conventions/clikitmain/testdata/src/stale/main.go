package main // want `the baseline allows stale/main.go 3 hand-parsed command-line reads and it holds 1: lower clikitmain.baseline in analyzers.yaml to 1`

import "flag"

func main() { flag.Parse() }
