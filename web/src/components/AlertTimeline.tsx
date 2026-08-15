import type { AlertHistoryEvent } from '../alerts/detail';
import { formatTimestamp } from '../alerts/format';

interface AlertTimelineProps {
  history: readonly AlertHistoryEvent[];
}

/** Tone classes exist for the known lifecycle types; anything else is neutral. */
const KNOWN_TONES = new Set(['created', 'updated', 'resolved', 'reopened', 'imported']);

function toneFor(type: string): string {
  const normalized = type.trim().toLowerCase();
  return KNOWN_TONES.has(normalized) ? normalized : 'other';
}

interface OccurrenceGroup {
  occurrence: number;
  events: AlertHistoryEvent[];
}

/**
 * Timeline tab: the immutable lifecycle history, newest first, grouped under
 * occurrence headers so repeated firings of the same alert stay distinct.
 */
export function AlertTimeline({ history }: AlertTimelineProps) {
  const sorted = [...history].sort(
    (a, b) => Date.parse(b.occurredAt) - Date.parse(a.occurredAt) || b.id - a.id,
  );

  if (sorted.length === 0) {
    return <p className="detail-empty-note">No history events recorded yet.</p>;
  }

  const groups: OccurrenceGroup[] = [];
  for (const event of sorted) {
    const last = groups[groups.length - 1];
    if (last !== undefined && last.occurrence === event.occurrence) {
      last.events.push(event);
    } else {
      groups.push({ occurrence: event.occurrence, events: [event] });
    }
  }

  return (
    <ol className="timeline-groups">
      {groups.map((group) => (
        <li key={group.occurrence}>
          <h3 className="timeline-occurrence">Occurrence {group.occurrence}</h3>
          <ol className="timeline">
            {group.events.map((event) => (
              <TimelineEvent key={event.id} event={event} />
            ))}
          </ol>
        </li>
      ))}
    </ol>
  );
}

function TimelineEvent({ event }: { event: AlertHistoryEvent }) {
  const meta = [event.actor, event.sourceStatus].filter((part) => part !== '').join(' · ');
  return (
    <li className={`timeline-event timeline-${toneFor(event.type)}`}>
      <span className="timeline-dot" aria-hidden="true" />
      <div className="timeline-head">
        <span className="timeline-type">{event.typeLabel}</span>
        <time className="timeline-time" dateTime={event.occurredAt}>
          {formatTimestamp(event.occurredAt)}
        </time>
      </div>
      {event.message !== '' ? <p className="timeline-message">{event.message}</p> : null}
      {meta !== '' ? <p className="timeline-meta">{meta}</p> : null}
    </li>
  );
}
