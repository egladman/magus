import { createLogger } from '../logging/index.mjs';
import { ConfigError, mergeConfig } from '../config/index.mjs';

const DEFAULTS = { method: 'GET', timeoutMs: 1000, retries: 0 };

export class HttpError extends Error {
  constructor(status, message) {
    super(message);
    this.name = 'HttpError';
    this.status = status;
  }
}

// Appends query as a sorted, percent-encoded query string. An empty or absent query
// returns base unchanged. Keys and values are both encoded.
export function buildUrl(base, query) {
  const keys = Object.keys(query ?? {}).sort();
  if (keys.length === 0) return base;
  const pairs = keys.map((key) => `${encodeURIComponent(key)}=${encodeURIComponent(String(query[key]))}`);
  return `${base}?${pairs.join('&')}`;
}

// Resolves options over the defaults and returns the request descriptor
// { url, method, timeoutMs, retries, log }. Opens no socket: this package models the
// request, and sending it is the caller's job. Throws ConfigError on an empty base.
export function request(base, options = {}) {
  if (typeof base !== 'string' || base.length === 0) {
    throw new ConfigError('base url is required', 'base');
  }
  const resolved = mergeConfig(DEFAULTS, options);
  const logger = createLogger('http');
  const url = buildUrl(base, resolved.query);
  logger.log('info', 'request', { method: resolved.method, url });
  return {
    url,
    method: resolved.method,
    timeoutMs: resolved.timeoutMs,
    retries: resolved.retries,
    log: logger.entries,
  };
}
