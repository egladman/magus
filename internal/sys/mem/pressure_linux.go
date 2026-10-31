//go:build linux

package mem

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// psiFullBadPercent is a starting figure with no Linux measurement behind it.
// The 2026-10-31 panic on macOS is the evidence that anything must refuse.
// `some` is not read: one stalled task is a machine using its memory.
const psiFullBadPercent = 25

// psiPaths are read in order, like cgroupLimitPaths: a container's own stall
// first, since /proc/pressure may describe a shared host. The root cgroup has
// no memory.pressure, so a process outside a container reaches /proc.
var psiPaths = []string{
	"/sys/fs/cgroup/memory.pressure",
	"/proc/pressure/memory",
}

// ReadPressure reads Pressure Stall Information. An error means no file
// answered, as on a kernel without CONFIG_PSI or one booted with psi=0.
func ReadPressure(_ context.Context) (Pressure, error) {
	var errs []error
	for _, path := range psiPaths {
		p, err := readPSI(path)
		if err == nil {
			return p, nil
		}
		errs = append(errs, err)
	}
	return Pressure{}, fmt.Errorf("the kernel has no readable pressure stall information: %w", errors.Join(errs...))
}

func readPSI(path string) (Pressure, error) {
	f, err := os.Open(path)
	if err != nil {
		return Pressure{}, err
	}
	defer f.Close()
	p, err := parsePSI(f)
	if err != nil {
		return Pressure{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// parsePSI judges a PSI file's full avg10, a percentage. A file without it
// is an error rather than a calm reading.
func parsePSI(r io.Reader) (Pressure, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || fields[0] != "full" {
			continue
		}
		raw, ok := strings.CutPrefix(fields[1], "avg10=")
		if !ok {
			return Pressure{}, fmt.Errorf("the full line has no avg10: %q", sc.Text())
		}
		avg, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return Pressure{}, fmt.Errorf("the full line's avg10: %w", err)
		}
		state := fmt.Sprintf("every runnable task was stalled on memory for %.1f%% of the last 10 s", avg)
		return Pressure{Bad: avg >= psiFullBadPercent, State: state}, nil
	}
	if err := sc.Err(); err != nil {
		return Pressure{}, err
	}
	return Pressure{}, errors.New("no full line")
}
