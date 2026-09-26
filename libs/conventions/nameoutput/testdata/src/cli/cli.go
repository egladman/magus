package cli

import "fmt"

const (
	outputJSON = "json"
	outputName = "name"
)

func emitNames(names []string) error { return nil }

func emitProjectNames(ps []string) error { return emitNames(ps) }

func ls(format string, names []string) error {
	switch format {
	case outputName:
		return emitNames(names)
	}
	return nil
}

func projects(format string, ps []string) error {
	switch format {
	case outputName:
		return emitProjectNames(ps)
	}
	return nil
}

func status(format string, v string) error {
	switch format {
	case outputName: // want "this `case outputName:` arm must render through emitNames"
		fmt.Println(v)
	}
	return nil
}

func shared(format string, v string) {
	switch format {
	case outputJSON, outputName:
		fmt.Println(v)
	}
}
