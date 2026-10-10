package job

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("job: not found")

func Load(id string) error {
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}
