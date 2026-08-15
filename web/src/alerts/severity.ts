export const SEVERITIES = ['critical', 'warning', 'info'] as const;

export type Severity = (typeof SEVERITIES)[number];

export const SEVERITY_LABELS: Record<Severity, string> = {
  critical: 'Critical',
  warning: 'Warning',
  info: 'Info',
};

/**
 * Buckets an arbitrary severity string from the query API into the three
 * visual severities. Unknown values map to info so unfamiliar Alertmanager
 * sources never break rendering; pair with `severityLabelFor` to keep the
 * original text visible in the UI.
 */
export function normalizeSeverity(value: string): Severity {
  const lowered = value.trim().toLowerCase();
  return (SEVERITIES as readonly string[]).includes(lowered) ? (lowered as Severity) : 'info';
}

/**
 * Display text for a severity tag: the standard label for known severities,
 * or the preserved source text for unknown ones (which render with info
 * styling) so their meaning is not lost.
 */
export function severityLabelFor(raw: string, normalized: Severity): string {
  const trimmed = raw.trim();
  if (trimmed === '' || trimmed.toLowerCase() === normalized) {
    return SEVERITY_LABELS[normalized];
  }
  return trimmed;
}
