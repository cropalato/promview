import { safeExternalUrl } from '../alerts/detail';
import type { AlertDetail } from '../alerts/detail';
import { formatTimestamp } from '../alerts/format';
import { AcknowledgeButton } from './AcknowledgeButton';
import { CopyButton } from './CopyButton';
import { ExternalLinkIcon, SeverityIcon } from './icons';

interface AlertDetailOverviewProps {
  detail: AlertDetail;
  /**
   * Runs the acknowledge toggle. The Actions section renders only when both
   * this handler and the server-provided per-alert permission are present.
   */
  onAcknowledge?: (acknowledged: boolean) => Promise<void>;
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
export function AlertDetailOverview({ detail, onAcknowledge }: AlertDetailOverviewProps) {
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

      {detail.actions.canAcknowledge && onAcknowledge !== undefined ? (
        <section className="detail-section" aria-label="Actions">
          <h3 className="detail-section-title">Actions</h3>
          <AcknowledgeButton acknowledged={detail.acknowledged} onAcknowledge={onAcknowledge} />
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
