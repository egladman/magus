package renamed // want "no `case outputName:` arm found in renamed"

const outputNames = "name"

func ls(format string) {
	switch format {
	case outputNames:
	}
}
