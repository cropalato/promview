import { useState } from 'react';
import { safeExternalUrl } from '../alerts/detail';
import type { AlertDetail, AlertSilenceRecord } from '../alerts/detail';
import type { LabelMatcher } from '../alerts/filter';
import { formatTimestamp } from '../alerts/format';
import { AcknowledgeButton } from './AcknowledgeButton';
import { CopyButton } from './CopyButton';
import { ExternalLinkIcon, SeverityIcon } from './icons';

interface AlertDetailOverviewProps {
  detail: AlertDetail;
  /**
   * The silences promview created that are holding this alert back. Empty
   * where the alert is suppressed by a silence made elsewhere, or by an
   * inhibition, and the drawer says which rather than guessing an author.
   */
  silences?: readonly AlertSilenceRecord[];
  /**
   * Runs the acknowledge toggle. The Actions section renders only when both
   * this handler and the server-provided per-alert permission are present.
   */
  onAcknowledge?: (acknowledged: boolean) => Promise<void>;
  /** Opens the silence dialog for this alert; enables the gated action. */
  onSilence?: () => void;
  /**
   * Lifts one silence holding this alert back. Absent where the deployment
   * cannot remove silences, where the server is too old to offer the endpoint,
   * or where this operator may not act on this alert; the control is then not
   * rendered rather than rendered to fail.
   */
  onRemoveSilence?: (silenceId: string) => Promise<void>;
  /**
   * Upserts a label matcher into the console filter and applies it. When
   * present, every label row gains include (`key="value"`) and exclude
   * (`key!="value"`) buttons.
   */
  onFilterLabel?: (matcher: LabelMatcher) => void;
}

function byKey([a]: [string, string], [b]: [string, string]): number {
  return a.localeCompare(b);
}

/**
 * Overview tab of the alert detail drawer: the operational facts (severity,
 * state, acknowledgement, source, occurrence, repeat count, timestamps),
 * safe links out to the generator/Alertmanager, and the full
 * label/annotation sets with copy controls. Mutating operator actions are
 * gated on the server-provided per-alert permissions; open mode (anonymous
 * viewer) never receives them, so it never sees the controls.
 */
export function AlertDetailOverview({
  detail,
  silences = [],
  onAcknowledge,
  onSilence,
  onRemoveSilence,
  onFilterLabel,
}: AlertDetailOverviewProps) {
  const labels = Object.entries(detail.labels).sort(byKey);
  const annotations = Object.entries(detail.annotations).sort(byKey);

  return (
    <div className="detail-overview">
      <dl className="detail-facts">
        <div className="detail-fact">
          <dt>Severity</dt>
          <dd>
            <span className={`sev-tag sev-${detail.severity}`}>
              <SeverityIcon severity={detail.severity} />
              <span>{detail.severityLabel}</span>
            </span>
          </dd>
        </div>
        <div className="detail-fact">
          <dt>State</dt>
          <dd>
            <span className={`state-chip state-${detail.status}`}>{detail.status}</span>
          </dd>
        </div>
        <div className="detail-fact">
          <dt>Acknowledged</dt>
          <dd>
            {detail.acknowledged ? (
              <>
                <span className="state-chip state-acknowledged">acknowledged</span>{' '}
                <span className="detail-mono">{acknowledgementNote(detail)}</span>
              </>
            ) : (
              <span className="detail-mono">No</span>
            )}
          </dd>
        </div>
        <div className="detail-fact">
          <dt>Suppressed</dt>
          <dd>
            <SuppressionNote
              detail={detail}
              silences={silences}
              onRemoveSilence={onRemoveSilence}
            />
          </dd>
        </div>
        <div className="detail-fact">
          <dt>Source</dt>
          <dd className="detail-mono">{detail.source}</dd>
        </div>
        <div className="detail-fact">
          <dt>Occurrence</dt>
          <dd className="detail-mono">{detail.occurrence}</dd>
        </div>
        <div className="detail-fact">
          <dt>Repeat count</dt>
          <dd className="detail-mono">{detail.repeatCount}</dd>
        </div>
        <div className="detail-fact">
          <dt>Fingerprint</dt>
          <dd className="detail-mono detail-break">{detail.fingerprint || '—'}</dd>
        </div>
      </dl>

      {(detail.actions.canAcknowledge && onAcknowledge !== undefined) ||
      (detail.actions.canSilence && onSilence !== undefined) ? (
        <section className="detail-section" aria-label="Actions">
          <h3 className="detail-section-title">Actions</h3>
          {detail.actions.canAcknowledge && onAcknowledge !== undefined ? (
            <AcknowledgeButton acknowledged={detail.acknowledged} onAcknowledge={onAcknowledge} />
          ) : null}
          {detail.actions.canSilence && onSilence !== undefined ? (
            <div className="detail-action">
              {/* Opens a confirmation rather than acting: a silence hides alerts
                  on a system promview does not own, and the matchers are worth
                  reading before it does. */}
              <button type="button" className="button" onClick={onSilence}>
                Silence alert…
              </button>
            </div>
          ) : null}
        </section>
      ) : null}

      <section className="detail-section" aria-label="Timestamps">
        <h3 className="detail-section-title">Timestamps</h3>
        <dl className="detail-times">
          <div className="detail-time">
            <dt>Started</dt>
            <dd className="detail-mono">{formatTimestamp(detail.startsAt)}</dd>
          </div>
          <div className="detail-time">
            <dt>Ended</dt>
            <dd className="detail-mono">
              {detail.endsAt !== null ? formatTimestamp(detail.endsAt) : 'Ongoing'}
            </dd>
          </div>
          <div className="detail-time">
            <dt>First seen</dt>
            <dd className="detail-mono">{formatTimestamp(detail.firstSeen)}</dd>
          </div>
          <div className="detail-time">
            <dt>Last seen</dt>
            <dd className="detail-mono">{formatTimestamp(detail.lastSeen)}</dd>
          </div>
        </dl>
      </section>

      <section className="detail-section" aria-label="External references">
        <h3 className="detail-section-title">Links</h3>
        <dl className="detail-times">
          <ExternalRef label="Generator URL" value={detail.generatorURL} />
          <ExternalRef label="Alertmanager URL" value={detail.externalURL} />
        </dl>
      </section>

      <section className="detail-section" aria-label="Labels">
        <h3 className="detail-section-title">Labels</h3>
        {labels.length === 0 ? (
          <p className="detail-empty-note">No labels.</p>
        ) : (
          <ul className="kv-list">
            {labels.map(([key, value]) => (
              <li key={key} className="kv-row">
                <span className="kv-key">{key}</span>
                <span className="kv-value">{value}</span>
                {onFilterLabel !== undefined ? (
                  <span className="kv-filter-actions">
                    <button
                      type="button"
                      className="kv-filter"
                      aria-label={`Filter to ${key}="${value}"`}
                      title={`Filter to ${key}="${value}"`}
                      onClick={() => onFilterLabel({ name: key, op: '=', value })}
                    >
                      +
                    </button>
                    <button
                      type="button"
                      className="kv-filter"
                      aria-label={`Exclude ${key}="${value}"`}
                      title={`Exclude ${key}="${value}"`}
                      onClick={() => onFilterLabel({ name: key, op: '!=', value })}
                    >
                      −
                    </button>
                  </span>
                ) : null}
                <CopyButton value={value} label={`Copy ${key}`} />
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="detail-section" aria-label="Annotations">
        <h3 className="detail-section-title">Annotations</h3>
        {annotations.length === 0 ? (
          <p className="detail-empty-note">No annotations.</p>
        ) : (
          <ul className="kv-list">
            {annotations.map(([key, value]) => (
              <li key={key} className="kv-row">
                <span className="kv-key">{key}</span>
                <span className="kv-value">{value}</span>
                <CopyButton value={value} label={`Copy ${key}`} />
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

/**
 * Human note next to the acknowledged chip: actor and/or timestamp, whichever
 * the API provided. Empty when neither is known.
 */
function acknowledgementNote(detail: AlertDetail): string {
  const parts: string[] = [];
  if (detail.acknowledgedBy !== '') {
    parts.push(`by ${detail.acknowledgedBy}`);
  }
  if (detail.acknowledgedAt !== null) {
    parts.push(`at ${formatTimestamp(detail.acknowledgedAt)}`);
  }
  return parts.join(' ');
}

/**
 * One external reference row. Only http/https URLs render as links (opened in
 * a new tab with `noopener`); anything else stays plain text so crafted
 * payloads cannot turn into javascript: links.
 */
function ExternalRef({ label, value }: { label: string; value: string }) {
  const safe = safeExternalUrl(value);
  return (
    <div className="detail-time">
      <dt>{label}</dt>
      <dd>
        {value.trim() === '' ? (
          <span className="detail-mono">—</span>
        ) : safe === null ? (
          <span className="detail-mono detail-break">{value}</span>
        ) : (
          <a className="detail-link" href={safe} target="_blank" rel="noopener noreferrer external">
            <ExternalLinkIcon className="detail-link-icon" />
            <span className="detail-break">{value}</span>
          </a>
        )}
      </dd>
    </div>
  );
}

/**
 * Says why an alert is not notifying, and how confidently.
 *
 * Four cases, and collapsing them would hide the one that matters: not
 * suppressed at all; held by an inhibition, which nobody chose and which lifts
 * itself; held by a silence promview created, where the author, expiry and
 * reason are known; and held by a silence made straight on the Alertmanager,
 * which is just as real and about which promview honestly knows nothing but
 * the id.
 */
function SuppressionNote({
  detail,
  silences,
  onRemoveSilence,
}: {
  detail: AlertDetail;
  silences: readonly AlertSilenceRecord[];
  onRemoveSilence?: (silenceId: string) => Promise<void>;
}) {
  // Which silence is mid-removal, and what went wrong with the last attempt.
  // Keyed by id rather than a single flag: an alert can be held by more than
  // one silence, and a failure against one says nothing about the others.
  const [removing, setRemoving] = useState<string | null>(null);
  const [failures, setFailures] = useState<Record<string, string>>({});

  if (!detail.suppressed) {
    return <span className="detail-mono">No</span>;
  }
  if (detail.silencedBy.length === 0) {
    return (
      <>
        <span className="state-chip state-suppressed state-inhibited">inhibited</span>{' '}
        <span className="detail-mono">
          Held back by an inhibition rule, not a silence. It lifts itself when its parent alert
          clears.
        </span>
      </>
    );
  }
  const known = new Map(silences.map((record) => [record.silenceId, record]));

  const remove = (id: string) => {
    if (onRemoveSilence === undefined) {
      return;
    }
    setRemoving(id);
    // The previous attempt's message goes with the retry; leaving it up beside
    // a "Removing…" control would report two contradictory states at once.
    setFailures((current) =>
      Object.fromEntries(Object.entries(current).filter(([key]) => key !== id)),
    );
    onRemoveSilence(id)
      // Success needs nothing here: the alert's own detail refreshes, and the
      // suppression row re-renders from it rather than from local state that
      // could disagree with the server.
      .catch((error: unknown) => {
        setFailures((current) => ({
          ...current,
          [id]: error instanceof Error ? error.message : String(error),
        }));
      })
      .finally(() => setRemoving(null));
  };

  return (
    <>
      <span className="state-chip state-suppressed">silenced</span>
      <ul className="detail-silences">
        {detail.silencedBy.map((id) => {
          const record = known.get(id);
          const failure = failures[id];
          return (
            <li key={id}>
              <span className="detail-mono detail-break">{id}</span>
              {record === undefined ? (
                // Created outside promview and not yet seen by a sync: the
                // silence is real, the reasoning is not ours to report.
                <span> — created outside Promview</span>
              ) : (
                <span>
                  {` — by ${record.createdBy || 'unknown'} until ${formatTimestamp(record.endsAt)}`}
                  {record.comment === '' ? '' : `: ${record.comment}`}
                </span>
              )}
              {onRemoveSilence === undefined ? null : (
                <>
                  {' '}
                  <button
                    type="button"
                    className="detail-silence-remove"
                    onClick={() => remove(id)}
                    disabled={removing !== null}
                    title="Lift this silence on its Alertmanager, so the alerts it holds back are shown again"
                  >
                    {removing === id ? 'Removing…' : 'Remove'}
                  </button>
                </>
              )}
              {failure === undefined ? null : (
                <p className="detail-silence-error" role="alert">
                  {failure}
                </p>
              )}
            </li>
          );
        })}
      </ul>
    </>
  );
}
