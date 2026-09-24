### Removed

- **Breaking for SDK callers: `ReportWriter`, `NewReportWriter`, `WithReport`,
  `WithReportWriter`, `Magus.LogScope`, `LogCharms`, `LogCache` and `LogBase`.** Build one
  `Sink` with `NewSink(format, stdout, stderr)` for the invocation's `-o` format, emit
  headers through it, pass it with `WithSink` and close it after the run.
