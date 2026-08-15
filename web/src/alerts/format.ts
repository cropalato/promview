/**
 * Full UTC timestamp for the detail drawer (e.g. "2026-08-14 10:00:00 UTC").
 * Unparseable input renders as an em dash instead of throwing.
 */
export function formatTimestamp(value: string): string {
  const time = Date.parse(value);
  if (Number.isNaN(time)) {
    return '—';
  }
  return new Date(time)
    .toISOString()
    .replace('T', ' ')
    .replace(/\.\d{3}Z$/, ' UTC');
}

/** Compact relative age for the alert table's Age column (e.g. "45s", "3h"). */
export function formatAge(startsAt: string, now: Date = new Date()): string {
  const started = Date.parse(startsAt);
  if (Number.isNaN(started)) {
    return '—';
  }
  const seconds = Math.max(0, Math.floor((now.getTime() - started) / 1000));
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 48) {
    return `${hours}h`;
  }
  return `${Math.floor(hours / 24)}d`;
}
