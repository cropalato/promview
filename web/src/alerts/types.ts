import type { Severity } from './severity';

/**
 * Shape of a row in the alert table, mapped from `GET /api/v1/alerts`
 * responses in `alerts/api.ts`. The default console columns come from the
 * project plan, so fields the query API does not expose yet (assignee,
 * notes) stay optional and render as placeholders.
 */
export type AlertState = 'firing' | 'resolved';

export interface AlertSummary {
  id: string;
  severity: Severity;
  /**
   * Text shown in the severity tag. Preserves the source severity when it
   * falls outside the three visual buckets (those all render as info); when
   * undefined the standard label for `severity` is used.
   */
  severityLabel?: string;
  state: AlertState;
  name: string;
  summary: string;
  team?: string;
  instance?: string;
  source: string;
  startsAt: string;
  assignee?: string;
  notes: number;
}
