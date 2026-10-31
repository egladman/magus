//go:build !linux && !darwin

package mem

import (
	"context"
	"errors"
)

// ReadPressure always errors here: no portable call reports memory pressure.
func ReadPressure(_ context.Context) (string, error) {
	return "", errors.New("this platform has no memory-pressure reading")
}
