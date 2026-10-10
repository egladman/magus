package quiet

import (
	"errors"
	"fmt"
)

func calls(err error) {
	_ = errors.New("lock held; retry later")
	_ = errors.New("no workspace. Run magus init")
	_ = fmt.Errorf("open %w", err) // want `error-wrap: %w only opens the format as "%w: " or closes it as ": %w"; see the runbook`
	_ = errors.New("one\ntwo")
}
