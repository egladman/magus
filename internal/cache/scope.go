package cache

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Description returns the cache header's facts: which tiers this run can reach, and
// whether it may write to them.
//
// It names the backend rather than probing it. Active() is a spell op on the real
// implementation (arbitrary Buzz, with the whole host surface), and calling it here put
// that on the path before the first line of output, where a slow probe stalls the run
// with nothing on screen to explain the pause. Presence and name are known without
// asking; whether the backend engages is the run's business, not the header's.
func (c *Cache) Description() (tier, mode string) {
	tier = "local"
	if c.remote != nil {
		name := c.remote.Name()
		if name == "" {
			name = "remote"
		}
		tier = name + " + local"
	}
	mode = "read-only"
	if c.mutable {
		mode = "read+write"
	}
	return tier, mode
}

// Collapsing reports whether the cache is withholding per-project subprocess output
// until failure (collapse-on-success). Callers use it to decide whether to attach a
// stage observer that prints progress lines for the otherwise-hidden work.
func (c *Cache) Collapsing() bool { return c.collapse }

// MemoryPressure is what the run-time watchdog observed. Data, not a sentence: a
// warning assembled across two packages has no single owner for its wording.
type MemoryPressure struct {
	AvailableBytes int64
	TotalBytes     int64
	// SwapUsedBytes and SwapGrowthBytes are the machine's swap and how much of it
	// this run added. Growth is the attributable half: a machine up for weeks
	// carries swap that predates the run, and reporting the level alone would
	// blame this run for it. Both 0 where the platform cannot report swap.
	SwapUsedBytes   int64
	SwapGrowthBytes int64
	// SwapTriggered reports that swap growth is why the watchdog spoke, as opposed to
	// falling headroom. See mem.Reading.
	SwapTriggered bool
	// BuzzObjects and BuzzPeak are the script VM's heap counts. Its heap never
	// frees, so a magusfile can consume the machine with no subprocess looking
	// guilty; 0 means the caller did not measure.
	BuzzObjects int
	BuzzPeak    int
	// BuzzHotSite is the source position responsible for the most heap growth,
	// as "source:line", or "" when nothing was sampled.
	BuzzHotSite string
}

// LogMemoryPressure warns that the host is running out of memory, routed through
// the cache logger like every other header.
//
// Warn rather than Info because a killed runner never lets magus reach its
// summary. Only what magus already streamed survives, and this is the line that
// explains an otherwise unattributable shutdown signal.
func (c *Cache) LogMemoryPressure(ctx context.Context, p MemoryPressure) {
	attrs := []any{
		slog.Int64("available_mb", p.AvailableBytes>>20),
		slog.Int64("total_mb", p.TotalBytes>>20),
	}
	msg := fmt.Sprintf("memory headroom low: %dMB available of %dMB total; a target here is close to taking the machine down",
		p.AvailableBytes>>20, p.TotalBytes>>20)

	// Swap leads when swap growth is what the watchdog fired on, because on darwin it
	// is the reading that moves: free, inactive and speculative pages do not fall
	// while the compressor and the swap file absorb the pressure, so headroom can
	// still look survivable on a machine that is already thrashing.
	//
	// Gated on the watchdog's own verdict rather than on the figure being non-zero. A
	// headroom warning routinely carries a few megabytes of growth, and switching on
	// that would replace the headroom sentence with one announcing 0MB paged out.
	if p.SwapTriggered {
		attrs = append(attrs,
			slog.Int64("swap_used_mb", p.SwapUsedBytes>>20),
			slog.Int64("swap_growth_mb", p.SwapGrowthBytes>>20))
		msg = fmt.Sprintf("this run has pushed %dMB into swap (%dMB used in total, %dMB of %dMB memory available); paging is what makes a machine stop responding rather than a target fail",
			p.SwapGrowthBytes>>20, p.SwapUsedBytes>>20, p.AvailableBytes>>20, p.TotalBytes>>20)
	}

	// Name what is running. The inflight registry already tracks this and already
	// survives a SIGKILL, so a warning that made the reader guess was withholding
	// an answer magus had on hand.
	if running := c.inflight.Running(); len(running) > 0 {
		names := make([]string, 0, len(running))
		for _, t := range running {
			names = append(names, t.Project+":"+t.Target)
		}
		attrs = append(attrs, slog.String("running", strings.Join(names, ", ")))
		msg += "; running: " + strings.Join(names, ", ")
	}
	if p.BuzzPeak > buzzHeapNoteworthy {
		attrs = append(attrs,
			slog.Int("buzz_objects", p.BuzzObjects),
			slog.Int("buzz_peak", p.BuzzPeak))
		msg += fmt.Sprintf("; buzz heap: %d objects (peak %d)", p.BuzzObjects, p.BuzzPeak)
		if p.BuzzHotSite != "" {
			attrs = append(attrs, slog.String("buzz_hot_site", p.BuzzHotSite))
			msg += ", most of it from " + p.BuzzHotSite
		} else {
			msg += " - an append-only heap this large usually means a magusfile is building a value in a loop"
		}
	}
	c.log.WarnContext(ctx, "cache.memory", append([]any{slog.String("msg", msg)}, attrs...)...)
}

// buzzHeapNoteworthy is the object count past which the Buzz heap is worth
// naming. Measured, not chosen: the case that motivated this reached tens of
// millions, while a healthy run of this repo stays orders of magnitude below.
const buzzHeapNoteworthy = 1_000_000
