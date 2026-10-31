//go:build !linux && !darwin

package mem

import (
	"context"
	"errors"
)

// ReadPressure always errors here: no portable call reports memory pressure.
func ReadPressure(_ context.Context) (Pressure, error) {
	return Pressure{}, errors.New("this platform has no memory-pressure reading")
}
