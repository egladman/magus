### Fixed

- **`budget.Limiter.Seat` of fewer than one slot gives back the slot it took.**
- **`Limiter.Yield` releases no more slots than the pool holds, and reports a failed
  re-acquire.**
- **A fired slot-watch grace timer no longer clears the timer that replaced it.**
