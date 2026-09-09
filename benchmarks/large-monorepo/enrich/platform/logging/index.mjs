// Deliberately clock-free: log lines are compared byte-for-byte by the tests and by
// the generated API summaries, so nothing here may vary between runs.

export const LogLevel = { debug: 10, info: 20, warn: 30, error: 40 };

// Renders one line as "LEVEL message key=value ...", fields sorted by key.
// An unknown level is rendered as given; fields may be null or omitted.
export function formatEntry(level, message, fields) {
  const parts = [String(level).toUpperCase(), message];
  for (const key of Object.keys(fields ?? {}).sort()) parts.push(`${key}=${String(fields[key])}`);
  return parts.join(' ');
}

// Returns { log, entries }. log(level, message, fields) renders the line, appends it
// to entries and returns it, or returns null when level is below minLevel. entries is
// the live array, not a copy.
export function createLogger(name, minLevel = 'info') {
  const floor = LogLevel[minLevel] ?? LogLevel.info;
  const entries = [];
  const log = (level, message, fields) => {
    if ((LogLevel[level] ?? 0) < floor) return null;
    const line = formatEntry(level, `[${name}] ${message}`, fields);
    entries.push(line);
    return line;
  };
  return { log, entries };
}
