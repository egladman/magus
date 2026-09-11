import { createLogger } from '../logging/index.mjs';

export class ConfigError extends Error {
  constructor(message, key) {
    super(message);
    this.name = 'ConfigError';
    this.key = key;
  }
}

function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

// Returns a new object; neither argument is mutated. Nested plain objects merge
// recursively, every other value replaces. Only `undefined` is skipped, so an
// override of false, 0 or '' still wins over the base.
export function mergeConfig(base, override) {
  const merged = { ...base };
  for (const key of Object.keys(override ?? {})) {
    const value = override[key];
    if (value === undefined) continue;
    merged[key] = isPlainObject(value) && isPlainObject(base?.[key])
      ? mergeConfig(base[key], value)
      : value;
  }
  return merged;
}

// Applies source over defaults and throws ConfigError, carrying the offending key,
// when a key required by defaults resolves to null or undefined.
export function loadConfig(defaults, source, required = []) {
  const log = createLogger('config');
  const resolved = mergeConfig(defaults, source);
  for (const key of required) {
    if (resolved[key] === undefined || resolved[key] === null) {
      throw new ConfigError(`missing required config key: ${key}`, key);
    }
  }
  log.log('info', 'config loaded', { keys: Object.keys(resolved).sort().join(',') });
  return resolved;
}
