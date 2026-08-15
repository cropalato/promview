import type { AlertSummary } from './types';

/**
 * Client-side text matcher for the filter input. It only covers the alert
 * rows already loaded in the browser (the table footer states this
 * limitation); server-side label filtering replaces it in a later phase.
 */
export function matchesFilter(alert: AlertSummary, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (needle === '') {
    return true;
  }
  const haystack = [
    alert.name,
    alert.summary,
    alert.team ?? '',
    alert.instance ?? '',
    alert.source,
    alert.severity,
    alert.state,
  ]
    .join('\n')
    .toLowerCase();
  return haystack.includes(needle);
}
