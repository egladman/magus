package broker

import (
	"errors"
	"fmt"
	"os"

	"example.com/m/job"
)

const lead = "job: "

type broker struct{}

func (broker) dial() error { return errors.New("broker: refused") }

func connect() error { return errors.New("broker: no socket") }

func origin(id string) {
	_ = errors.New("job: not found")        // want `error-origin: a "job: " prefix names another package of this module`
	_ = fmt.Errorf("job: %s not found", id) // want `error-origin`
	_ = errors.New(lead + id)               // want `error-origin`
	_ = errors.New("broker: not found")
	_ = errors.New("run: stopped")
	_ = errors.New("usage: broker <id>")
	_ = errors.New("os: not a package of this module")
	_ = errors.New("job not found")
	_ = errors.New("job:not found")
	_ = fmt.Errorf("%s: job: not found", id)
}

type loadErr struct{ id string }

func (e loadErr) Error() string {
	return "job: " + e.id + " not found" // want `error-origin`
}

func stutter(b broker, path string) error {
	err := connect()
	if err != nil {
		return fmt.Errorf("broker: connect: %w", err) // want `error-stutter: a "broker: " prefix on an error this package already returned`
	}
	if err := b.dial(); err != nil {
		return fmt.Errorf("broker: %w", err) // want `error-stutter`
	}
	if err := connect(); err != nil {
		return fmt.Errorf("broker: %[2]s: %[1]w", err, path) // want `error-stutter`
	}
	_ = fmt.Errorf("broker: dial: %w", b.dial())
	var late = connect()
	_ = fmt.Errorf("broker: %w", late) // want `error-stutter`

	if _, err := os.Open(path); err != nil {
		return fmt.Errorf("broker: open %s: %w", path, err)
	}
	if err := job.Load(path); err != nil {
		return fmt.Errorf("broker: load: %w", err)
	}
	err = connect()
	err = job.Load(path)
	_ = fmt.Errorf("broker: load: %w", err)
	if err := connect(); err != nil {
		return fmt.Errorf("connect %s: %w", path, err)
	}
	return nil
}
