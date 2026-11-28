package wake

// End is why a wake ended, "" while it has not.
type End string

const (
	// EndStopped is a person's [Store.Stop].
	EndStopped End = "stopped"
	// EndLapsed is a turn started and not reported by the deadline. Its outcome is not
	// guessed.
	EndLapsed End = "lapsed"
	// EndDone is a report that acted on nothing and left nothing pending.
	EndDone End = "done"
	// EndSpend is the reports' spend reaching MaxUSD. The report that reaches it is kept,
	// so a wake can pass the bound by its last turn's spend.
	EndSpend End = "spend"
	// EndIdle is MaxIdle reports in a row that acted on nothing.
	EndIdle End = "idle"
	// EndCount is MaxWakes reports filed.
	EndCount End = "count"
	// EndUntil is the deadline passing with no turn open.
	EndUntil End = "until"
)

// Next is [Decide]'s answer. Every time is a unix second.
type Next struct {
	// Again is true when another turn may start, at NotBefore or later. NotBefore may
	// fall past Terms.Until, and a start then finds the wake ended by the deadline.
	Again     bool
	NotBefore int64
	// Open is the N of a turn started and not yet reported, 0 for none. Again is false
	// while one is open.
	Open int
	// End is why the wake ended and EndedAt when, both zero while it has not.
	End     End
	EndedAt int64
	// WakesLeft, SecondsLeft and USDLeft are what each bound still allows, never below 0.
	// USDLeft is 0 under no spend bound.
	WakesLeft   int
	SecondsLeft int64
	USDLeft     float64
}

// Decide answers whether w may start another turn at now and not before when, or why it
// ended. It is pure, and the only place the rules live. When several ends apply, the
// first of stopped, lapsed, done, spend, idle, count and until wins. An end stays put:
// the store refuses every write to an ended wake.
func Decide(w Wake, now int64) Next {
	t := w.Terms
	next := Next{
		NotBefore:   t.At,
		Open:        w.Open.N,
		WakesLeft:   max(0, t.MaxWakes-w.N),
		SecondsLeft: max(0, t.Until-now),
	}
	if w.N > 0 {
		next.NotBefore = w.Last.Ended + t.MinGap
	}
	if t.MaxUSD > 0 {
		next.USDLeft = max(0, t.MaxUSD-w.SpentUSD)
	}
	next.End, next.EndedAt = ended(w, now)
	next.Again = next.End == "" && w.Open.N == 0
	return next
}

func ended(w Wake, now int64) (End, int64) {
	t, last := w.Terms, w.Last
	switch {
	case w.Stopped.Wake != "":
		return EndStopped, w.Stopped.At
	case w.Open.N != 0 && now >= t.Until:
		return EndLapsed, t.Until
	case w.Open.N != 0:
		return "", 0
	case w.N > 0 && last.Acted == 0 && last.Pending == 0:
		return EndDone, last.Ended
	case t.MaxUSD > 0 && w.SpentUSD >= t.MaxUSD:
		return EndSpend, last.Ended
	case w.Idle >= t.MaxIdle:
		return EndIdle, last.Ended
	case w.N >= t.MaxWakes:
		return EndCount, last.Ended
	case now >= t.Until:
		return EndUntil, t.Until
	}
	return "", 0
}
