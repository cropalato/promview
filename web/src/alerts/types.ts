import type { Severity } from './severity';

/**
 * Shape of a row in the alert table, mapped from `GET /api/v1/alerts`
 * responses in `alerts/api.ts`. The default console columns come from the
 * project plan, so fields the query API does not expose yet (assignee,
 * notes) stay optional and render as placeholders.
 */
/**
 * `expired` is the console's own conclusion, not the source's: the source went
 * quiet for longer than its window, which is a weaker claim than `resolved`.
 */
export type AlertState = 'firing' | 'resolved' | 'expired';

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
  /**
   * Every label the alert carries, so a column bound to an arbitrary label can
   * render without the label having to be given console-wide meaning first.
   */
  labels: Record<string, string>;
  /** A silence or inhibition is holding this alert back at the source. */
  suppressed: boolean;
  /**
   * Ids of the silences currently matching. Suppressed with none of these
   * means an inhibition is holding it back instead, which the console shows
   * differently: an inhibition lifts itself when its parent clears, a silence
   * was somebody's decision and has an expiry.
   */
  silencedBy: string[];
  /** When the source last reported the alert, which is what expiry measures. */
  lastSeen: string;
}
